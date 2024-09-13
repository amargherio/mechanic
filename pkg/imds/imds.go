package imds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/pkg/consts"
	v1 "k8s.io/api/core/v1"
)

type ScheduledEventType string
type ScheduledEventStatus string
type ScheduledEventSource string

const (
	Reboot    ScheduledEventType   = "Reboot"
	Redeploy  ScheduledEventType   = "Redeploy"
	Freeze    ScheduledEventType   = "Freeze"
	Preempt   ScheduledEventType   = "Preempt"
	Terminate ScheduledEventType   = "Terminate"
	Scheduled ScheduledEventStatus = "Scheduled"
	Started   ScheduledEventStatus = "Started"
	Platform  ScheduledEventSource = "Platform"
	User      ScheduledEventSource = "User"
)

// The next two structs are documented at https://learn.microsoft.com/en-us/azure/virtual-machines/linux/scheduled-events#event-properties

// ScheduledEvent represents a single event returned as part of the full response returned from the IMDS scheduled events API
type ScheduledEvent struct {
	EventId      string             `json:"EventId"`
	Type         ScheduledEventType `json:"EventType"`
	ResourceType string             `json:"ResourceType"`
	Resources    []string           `json:"Resources"`
	EventStatus  ScheduledEventStatus
	NotBefore    time.Time            `json:"NotBefore"` // time in UTC
	Description  string               `json:"Description"`
	EventSource  ScheduledEventSource `json:"EventSource"`
	Duration     time.Duration        `json:"DurationInSeconds"`
}

// ScheduledEventsResponse represents the full response returned from the IMDS scheduled events API
type ScheduledEventsResponse struct {
	IncarnationID float64          `json:"DocumentIncarnation"`
	Events        []ScheduledEvent `json:"Events"`
}

type IMDS interface {
	QueryIMDS(ctx context.Context) (ScheduledEventsResponse, error)
}

type IMDSClient struct{}

// CheckIfDrainRequired checks if the node should be drained based on scheduled events from IMDS.
func CheckIfDrainRequired(ctx context.Context, ic IMDS, node *v1.Node) (bool, error) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	log.Infow("Checking if drain is required for node", "node", node.Name)
	shouldDrain := false // setting the default drain response to false

	// query IMDS to get scheduled event data
	resp, err := ic.QueryIMDS(ctx)
	if err != nil {
		log.Errorw("Failed to query IMDS", "error", err)
		return shouldDrain, err
	}

	if len(resp.Events) == 0 {
		log.Debug("No scheduled events found")
		return shouldDrain, err
	}

	// for each event in the scheduled events response, check if the event is for the current instance
	for _, event := range resp.Events {
		impacted, err := isNodeImpacted(ctx, node, event)
		if err != nil {
			return shouldDrain, err
		}

		if impacted {
			// this event impacts the node we're on. let's see what kind of event it is so we know if we need to take action
			switch event.Type {
			case Reboot, Redeploy, Preempt, Terminate:
				log.Infow("Found event that requires draining the node", "event", event, "eventId", event.EventId)
				shouldDrain = true
				return shouldDrain, nil
			case Freeze:
				// TODO: Freeze event types also indicate an LM which could be critical...how do we differentiate? using description is a poor workaround
				if strings.Contains(event.Description, "memory-preserving Live Migration") {
					log.Infow("Found event that requires draining the node", "event", event, "eventId", event.EventId)
					shouldDrain = true
					return shouldDrain, nil
				} else {
					log.Debugw("Found a freeze event that does not require draining", "event", event, "eventId", event.EventId)
				}
			default:
				log.Debugw("Found an event that targets current node, but does not require draining", "event", event, "eventId", event.EventId)
			}
		}
	}
	log.Infow("Did not find any events that require draining the node", "node", node.Name)
	return shouldDrain, nil
}

func isNodeImpacted(ctx context.Context, node *v1.Node, event ScheduledEvent) (bool, error) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger
	log.Debugw("Checking if node is impacted by event", "node", node.Name, "event", event.EventId)

	// get the instance name for the node
	instance, err := getInstanceName(ctx, node)
	if err != nil {
		return false, err
	}

	// check if the event impacts the node
	if event.ResourceType == "VirtualMachine" {
		for _, value := range event.Resources {
			if value == instance || strings.Contains(value, instance) {
				log.Infow("Node is impacted by event", "node", node.Name, "event", event.EventId)
				return true, nil
			}
		}
	}

	log.Debugw("Node is not impacted by event", "node", node.Name, "event", event.EventId)
	return false, nil
}

func getInstanceName(ctx context.Context, node *v1.Node) (string, error) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger
	log.Debugw("Getting instance name for node", "node", node.Name)

	// get the last six characters of the node name
	instanceName := node.Name[len(node.Name)-6:]
	vm := node.Name[:len(node.Name)-6]

	// base36 decode the instanceName to get the VMSS instance number
	decoded, err := strconv.ParseInt(instanceName, 36, 64)
	if err != nil {
		log.Errorw("Failed to decode instance name", "error", err)
		return "", err
	}

	decodedInstanceName := fmt.Sprintf("%s_%d", vm, decoded)
	log.Debugw("Decoded node name to resolve VMSS instance number", "instanceName", decodedInstanceName, "nodeName", node.Name)
	return decodedInstanceName, nil
}

// QueryIMDS queries the Instance Metadata Service (IMDS) for scheduled events.
// It returns a ScheduledEventsResponse containing the events and an error if any occurred during the query.
func (ic IMDSClient) QueryIMDS(ctx context.Context) (ScheduledEventsResponse, error) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger
	log.Debug("Querying IMDS for scheduled event data")

	// query IMDS for scheduled events
	var eventResponse ScheduledEventsResponse
	client := http.Client{
		Transport: &http.Transport{Proxy: nil},
	}

	req, _ := http.NewRequest("GET", consts.IMDS_SCHEDULED_EVENTS_API_ENDPOINT, nil)
	req.Header.Add("Metadata", "true")
	q := req.URL.Query()
	q.Add("api-version", "2020-07-01")

	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		log.Errorw("Failed to query IMDS", "error", err)
		return ScheduledEventsResponse{}, err
	}

	defer resp.Body.Close()

	var generic map[string]interface{}
	log.Debugw("IMDS response", "status", resp.Status)
	if err := json.NewDecoder(resp.Body).Decode(&generic); err != nil {
		log.Errorw("Failed to decode IMDS response", "error", err)
		return ScheduledEventsResponse{}, err
	}

	eventResponse = ScheduledEventsResponse{}
	buildEventResponse(ctx, generic, &eventResponse)

	return eventResponse, nil
}

func buildEventResponse(ctx context.Context, generic map[string]interface{}, eventResponse *ScheduledEventsResponse) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger
	log.Debugw("Creating event response from IMDS response", "response", generic)

	eventResponse.IncarnationID = generic["DocumentIncarnation"].(float64)
	events := generic["Events"].([]interface{})
	for _, e := range events {
		event := ScheduledEvent{}
		eventMap := e.(map[string]interface{})

		event.EventId = eventMap["EventId"].(string)
		event.Type = ScheduledEventType(eventMap["EventType"].(string))
		event.ResourceType = eventMap["ResourceType"].(string)
		event.EventStatus = ScheduledEventStatus(eventMap["EventStatus"].(string))
		event.Description = eventMap["Description"].(string)
		event.EventSource = ScheduledEventSource(eventMap["EventSource"].(string))

		// "resources" is going to be initially typed as []interface{} so we have to do special things to convert it to
		// []string
		event.Resources = make([]string, len(eventMap["Resources"].([]interface{})))
		for i, v := range eventMap["Resources"].([]interface{}) {
			event.Resources[i] = v.(string)
		}

		// handle time and duration parsing
		if eventMap["NotBefore"] != nil || eventMap["DurationInSeconds"] != "" {
			parsed, err := time.Parse("Mon, 02 Jan 2006 15:04:05 GMT", eventMap["NotBefore"].(string))
			if err != nil {
				log.Warnw("Failed to parse NotBefore time", "error", err)
			}
			event.NotBefore = parsed
			event.Duration = time.Duration(eventMap["DurationInSeconds"].(float64)) * time.Second
		} else {
			log.Debug("No NotBefore or DurationInSeconds found in event details from IMDS")
		}

		log.Debugw("Adding parsed event to event slice", "event", event)

		eventResponse.Events = append(eventResponse.Events, event)
	}

	log.Debugw(fmt.Sprintf("Returning an event response with %d events", len(eventResponse.Events)), "eventCount", len(eventResponse.Events), "eventId", eventResponse.IncarnationID)
}
