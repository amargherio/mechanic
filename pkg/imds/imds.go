package imds

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/amargherio/mechanic/pkg/consts"
	"go.uber.org/zap"
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
	IncarnationID string           `json:"DocumentIncarnation"`
	Events        []ScheduledEvent `json:"Events"`
}

type IMDS interface {
	QueryIMDS(ctx context.Context) (ScheduledEventsResponse, error)
}

type IMDSClient struct{}

func CheckIfDrainRequired(ctx context.Context, ic IMDS, node *v1.Node) (bool, error) {
	log := ctx.Value("logger").(*zap.SugaredLogger)
	log.Debugw("Checking if drain is required for node", "node", node.Name)

	// query IMDS to get scheduled event data
	resp, err := ic.QueryIMDS(ctx)
	if err != nil {
		log.Errorw("Failed to query IMDS", "error", err)
		return false, err
	}

	if len(resp.Events) == 0 {
		log.Debug("No scheduled events found")
		return false, nil
	}

	// since we have things to process, grab the node name without the base36 encoding
	instance, err := getInstanceName(ctx, node)
	if err != nil {
		return false, err
	}

	// for each event in the scheduled events response, check if the event is for the current instance
	for _, event := range resp.Events {
		if event.ResourceType == "VirtualMachine" && slices.Contains(event.Resources, instance) {
			// this event impacts the node we're on. let's see what kind of event it is so we know if we need to take action
			switch event.Type {
			case Reboot, Redeploy, Preempt, Terminate:
				log.Infow("Found event that requires draining the node", "event", event, "eventId", event.EventId)
				return true, nil
			case Freeze:
				// TODO: Freeze event types also indicate an LM which could be critical...how do we differentiate? using description is a poor workaround
				if strings.Contains(event.Description, "memory-preserving Live Migration") {
					log.Infow("Found event that requires draining the node", "event", event, "eventId", event.EventId)
					return true, nil
				} else {
					log.Debugw("Found a freeze event that does not require draining", "event", event, "eventId", event.EventId)
				}
			default:
				log.Debugw("Found an event that targets current node, but does not require draining", "event", event, "eventId", event.EventId)
			}
		}
	}
	return false, nil
}

func getInstanceName(ctx context.Context, node *v1.Node) (string, error) {
	log := ctx.Value("logger").(*zap.SugaredLogger)
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

	log.Debugw("Decoded node name to resolve VMSS instance number", "instanceName", instanceName, "decoded", decoded)
	log.Debugw("Returning generated VMSS resource name", "vm", vm, "instance", decoded)
	return fmt.Sprintf("%s_%d", vm, decoded), nil
}

func (ic *IMDSClient) QueryIMDS(ctx context.Context) (ScheduledEventsResponse, error) {
	log := ctx.Value("logger").(*zap.SugaredLogger)
	log.Debug("Querying IMDS")

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
	if err := json.NewDecoder(resp.Body).Decode(&eventResponse); err != nil {
		log.Errorw("Failed to decode IMDS response", "error", err)
		return ScheduledEventsResponse{}, err
	}

	return eventResponse, nil
}
