package node

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubectl/pkg/drain"
)

// temp type for wrapping the zap logger to be io.Writer compatible
// this is needed for the drain helper to use the zap logger
type logger struct {
	level string
	log   *zap.SugaredLogger
}

func (l *logger) Write(p []byte) (n int, err error) {
	msg := string(p)

	if strings.HasPrefix("WARNING", msg) {
		l.log.Warn(string(p))
		return len(p), nil
	}
	if l.level == "error" {
		l.log.Error(string(p))
		return len(p), nil
	}
	l.log.Info(string(p))
	return len(p), nil
}

func cordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) (bool, error) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// check if our node is cordoned, which throws our app state out of sync
	if node.Spec.Unschedulable {
		if !vals.State.IsCordoned {
			// the node is unschedulable but our state is not in sync - check if we did it, and reconcile cordoned state.
			if _, ok := node.GetLabels()["mechanic.cordoned"]; ok {
				vals.State.IsCordoned = true
				log.Warnw("Node is cordoned, but our state is not in sync. Reconciling state.", "traceCtx", ctx)
			} else {
				log.Infow("Node is cordoned, but we aren't responsible for the cordon.", "node", node.Name, "traceCtx", ctx)
				// we could still benefit from the cordon and don't need to cordon again, so sync state
				vals.State.IsCordoned = true
			}
		}
		log.Infow("Node is already cordoned", "node", node.Name, "state", vals.State.IsCordoned, "traceCtx", ctx)
		return true, nil
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		n, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// update the labels to show mechanic cordoned the node and cordon the node
		n.Spec.Unschedulable = true
		labels := n.GetLabels()
		labels["mechanic.cordoned"] = "true"
		n.SetLabels(labels)
		log.Debugw("Node object updated with unschedulable set to true and mechanic.cordoned label", "traceCtx", ctx)

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to cordon node - retry error encountered", "node", node.Name, "error", retryErr, "traceCtx", ctx)
		return false, retryErr
	}

	res_node, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		log.Warnw("Failed to get node after cordon - returning without updating state", "node", node.Name, "error", err, "traceCtx", ctx)
		return false, err
	}

	// validate result node state
	if !res_node.Spec.Unschedulable {
		log.Errorw("Node was not cordoned", "node", node.Name, "traceCtx", ctx)
		return false, errors.New("node was not cordoned")
	}

	if res_node.GetLabels()["mechanic.cordoned"] != "true" {
		log.Errorw("Node was not labeled as cordoned by mechanic", "node", node.Name, "traceCtx", ctx)
		return false, errors.New("node was not labeled as cordoned by mechanic")
	}

	// successfully cordoned
	log.Infow("Node cordoned", "node", node.Name, "traceCtx", ctx)
	return true, nil
}

func uncordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) error {
	vals := ctx.Value("values").(*config.ContextValues)

	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "UncordonNode")
	defer span.End()

	log := vals.Logger

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		n, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// update the labels to show mechanic cordoned the node and cordon the node
		n.Spec.Unschedulable = false
		log.Debugw("Unschedulable set to false on node object", "traceCtx", ctx)

		labels := n.GetLabels()
		delete(labels, "mechanic.cordoned")
		n.SetLabels(labels)
		log.Debugw("Labels updated on node object with mechanic.cordoned label removed", "traceCtx", ctx)

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to uncordon node - retry error encountered", "node", node.Name, "error", retryErr, "traceCtx", ctx)
		return retryErr
	}

	vals.State.IsCordoned = false
	return nil
}

func drainNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) (bool, error) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "DrainNode")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// drain the node
	log.Infow("Beginning node drain", "node", node.Name, "traceCtx", ctx)

	// hack: use the logger wrapper to make the zap logger compatible with the drain helper
	errWrap := &logger{log: log, level: "error"}
	logWrap := &logger{log: log, level: "info"}

	drainHelper := &drain.Helper{
		Client:              clientset,
		Ctx:                 ctx,
		Force:               true,
		DeleteEmptyDirData:  true,
		IgnoreAllDaemonSets: true,
		GracePeriodSeconds:  -1,
		Out:                 logWrap,
		ErrOut:              errWrap,
	}

	if err := drain.RunNodeDrain(drainHelper, node.Name); err != nil {
		return false, err
	}

	return true, nil
}

func validateCordon(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, recorder record.EventRecorder) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "ValidateCordon")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// potential node states:
	// - cordoned and mechanic labeled: we own the cordon as far as we know, so we can manage it
	// - cordoned but not mechanic labeled: we don't own the cordon, so we can't manage it
	// - not cordoned but mechanic labeled: we own the cordon but the node is no longer cordoned, so we need to validate
	// - not cordoned and not mechanic labeled: BAU, no action required
	// - node is cordoned but out state is not in sync: we need to reconcile the state
	// - node is not cordoned but our state is: we need to reconcile the state

	// checking if we have a scheduled event. if we do, we should make sure node and app state is in sync
	if vals.State.HasDrainableCondition {
		if vals.State.IsCordoned && !node.Spec.Unschedulable {
			log.Debugw("Node has an upcoming event scheduled, state shows cordoned but node is not. Cordon the node.", "node", node.Name, "state", vals.State, "traceCtx", ctx)
			isCordoned, err := cordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to cordon node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
			} else {
				log.Infow("Node cordoned", "node", node.Name, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
				vals.State.IsCordoned = isCordoned
			}
		} else if !vals.State.IsCordoned && node.Spec.Unschedulable {
			log.Debugw("Node has an upcoming event scheduled, state shows not cordoned but node is. Update state to reflect actual configuration.", "node", node.Name, "state", vals.State, "traceCtx", ctx)
			vals.State.IsCordoned = true
		} else {
			log.Debugw("No need to check for unneeded cordon, event is scheduled", "node", node.Name, "state", vals.State, "traceCtx", ctx)
		}

		return
	}

	// we don't have an upcoming event, so check if it's cordoned or not
	if vals.State.IsCordoned {
		// did we cordon it? if so, our label should be there and we can uncordon. if the label is missing, we don't touch
		// the cordon because we can't guarantee we're the ones that cordoned it
		if _, ok := node.Labels["mechanic.cordoned"]; ok {
			log.Infow("Node is cordoned by mechanic but no scheduled events found. Uncordoning node and removing the label", "node", node.Name, "traceCtx", ctx)

			err := uncordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to uncordon node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "UncordonNode", "Failed to uncordon node %s", node.Name)
			} else {
				log.Infow("Node uncordoned", "node", node.Name, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "UncordonNode", "Node %s uncordoned by mechanic", node.Name)
				vals.State.IsCordoned = false
			}
		} else {
			vals.State.IsCordoned = true
			log.Infow("Node is cordoned but does not have the mechanic label - no action required to uncordon", "node", node.Name, "state", vals.State, "traceCtx", ctx)
		}
	} else {
		// our state shows it's not cordoned, so we should check if state is out of sync and reconcile
		if node.Spec.Unschedulable {
			if _, ok := node.Labels["mechanic.cordoned"]; ok {
				log.Warnw("Node is cordoned but our state shows it's not. No upcoming events so uncordoning the node and removing the label", "node", node.Name, "traceCtx", ctx)
				err := uncordonNode(ctx, clientset, node)
				if err != nil {
					log.Errorw("Failed to uncordon node", "node", node.Name, "error", err, "traceCtx", ctx)
					recorder.Eventf(node, v1.EventTypeWarning, "UncordonNode", "Failed to uncordon node %s", node.Name)
				} else {
					log.Infow("Node uncordoned", "node", node.Name, "traceCtx", ctx)
					recorder.Eventf(node, v1.EventTypeNormal, "UncordonNode", "Node %s uncordoned by mechanic", node.Name)
					vals.State.IsCordoned = false
					removeMechanicCordonLabel(ctx, node, clientset)
				}
			} else {
				log.Infow("Node is cordoned but no mechanic label found - no action required", "node", node.Name, "traceCtx", ctx)
			}
		}
	}

	// at this point we've either left the node cordoned because we didn't cordon it or we've released our cordon.
	// clean up the app state and return
	if vals.State.ShouldDrain {
		vals.State.ShouldDrain = false
	}

	if vals.State.IsDrained {
		vals.State.IsDrained = false
	}
}

func CheckNodeConditions(ctx context.Context, node *v1.Node, eventDrainConditions *config.ScheduledEventDrainConditions, optDrainConditions *config.OptionalDrainConditions) (bool, bool) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "CheckNodeConditions")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// iterate through the DrainConditions fields and build a list of drainable node conditions
	// todo: this feels hacky...should be a better way to do this
	eventShouldDrain := make([]string, 0)
	optDrainable := make([]string, 0)
	eventShouldDrain = append(eventShouldDrain, "VMEventScheduled") // always cover a generic `VMEventScheduled` condition

	// use the different calls to DrainableConditions to get the full list of conditions we're configured to drain for
	eventShouldDrain = append(eventShouldDrain, eventDrainConditions.DrainableConditions()...)
	optDrainable = append(optDrainable, optDrainConditions.OptionalDrainableConditions()...)

	drainableResp := false
	eventResp := false
	conditions := node.Status.Conditions

	for _, condition := range conditions {
		if eventResp && drainableResp {
			// we've checked and have a drainable condition and an event scheduled condition, so we can stop checking
			log.Debugw("Node has both a drainable condition and an event scheduled condition. No need to check further.", "node", node.Name, "traceCtx", ctx)
			break
		} else {
			if !eventResp && slices.Contains(eventShouldDrain, string(condition.Type)) {
				// check the status of the condition. if it's true, update state.HasEventScheduled to true. if it's false, reset it to false and
				// remove the cordon if we're the ones who cordoned it
				if condition.Status == v1.ConditionTrue {
					log.Infow("Node has an upcoming scheduled event. Flagging for impact assessment.",
						"node", node.Name,
						"type", condition.Type,
						"lastTransitionTime", condition.LastTransitionTime,
						"reason", condition.Reason,
						"message", condition.Message,
						"traceCtx", ctx)
					eventResp = true
					drainableResp = true
				} else {
					log.Debugw("Condition doesn't align with a VMScheduledEvent condition.", "condition", condition.Type, "node", node.Name, "traceCtx", ctx)
					eventResp = false
				}
			}
			if !drainableResp && slices.Contains(optDrainable, string(condition.Type)) {
				// check the status of the condition. if it's true, update state.HasDrainableCondition to true. if it's false, reset it to false and
				// remove the cordon if we're the ones who cordoned it
				if condition.Status == v1.ConditionTrue {
					log.Infow("Node has a drainable condition. Flagging for impact assessment.",
						"node", node.Name,
						"type", condition.Type,
						"lastTransitionTime", condition.LastTransitionTime,
						"reason", condition.Reason,
						"message", condition.Message,
						"traceCtx", ctx)
					drainableResp = true
				} else {
					log.Debugw("Condition doesn't align with a drainable condition.", "condition", condition.Type, "node", node.Name, "traceCtx", ctx)
					drainableResp = false
				}
			}
		}
	}

	return drainableResp, eventResp
}

func removeMechanicCordonLabel(ctx context.Context, node *v1.Node, clientset kubernetes.Interface) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "removeMechanicCordonLabel")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		n, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		labels := n.GetLabels()
		delete(labels, "mechanic.cordoned")
		n.SetLabels(labels)

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to remove mechanic label from node - retry error encountered", "node", node.Name, "error", retryErr, "traceCtx", ctx)
	}
	log.Debugw("Mechanic label removed from node", "node", node.Name, "traceCtx", ctx)
}

// handleNodeCordonAndDrain handles the shared logic for cordoning and draining a node
func HandleNodeCordonAndDrain(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, state *appstate.State, recorder record.EventRecorder, tracer trace.Tracer, log *zap.SugaredLogger) {
	ctx, span := tracer.Start(ctx, "handleNodeCordonAndDrain")
	defer span.End()

	if state.HasDrainableCondition && state.ShouldDrain {
		// early return if the node is already cordoned and drained
		if state.IsCordoned && state.IsDrained {
			log.Infow("Node is already cordoned and drained, no action required", "node", node.Name, "state", state, "traceCtx", ctx)
			return
		}

		log.Infow("Determined drain is required for the node", "node", node.Name, "state", state, "traceCtx", ctx)

		// check state and attempt to cordon if required
		if state.IsCordoned {
			log.Infow("Node is already cordoned, skipping cordon", "node", node.Name, "state", state, "traceCtx", ctx)
			recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s is already cordoned, no need to attempt a cordon.", node.Name)
		} else {
			b, err := cordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to cordon node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
			} else {
				state.IsCordoned = b
				log.Infow("Node cordoned", "node", node.Name, "state", state, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
			}
		}

		if state.IsDrained {
			log.Infow("Node is already drained, skipping drain", "node", node.Name, "traceCtx", ctx)
		} else {
			b, err := drainNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to drain node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "DrainNode", "Failed to drain node %s", node.Name)
			} else {
				state.IsDrained = b
				log.Infow("Node drain completed", "node", node.Name, "state", state, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "DrainNode", "Node %s drained by mechanic", node.Name)
			}
		}
	}

	// Check for unneeded cordon
	log.Infow("Checking for unneeded cordon", "node", node.Name, "state", state, "traceCtx", ctx)
	updated, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		log.Errorw("Failed to get updated node object", "node", node.Name, "error", err, "state", state, "traceCtx", ctx)
		return
	}
	validateCordon(ctx, clientset, updated, recorder)

	log.Infow("Finished processing node cordon and drain", "node", node.Name, "state", state, "traceCtx", ctx)
}

func CheckOptionalDrainConditions(ctx context.Context, node *v1.Node, optDrainConditions *config.OptionalDrainConditions) (bool, error) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/node")
	ctx, span := tracer.Start(ctx, "CheckOptionalDrainConditions")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// Check if the node matches any of the optional drain conditions
	nodeConditions := node.Status.Conditions
	optionalDrains := optDrainConditions.OptionalDrainableConditions()
	for _, cond := range nodeConditions {
		if slices.Contains(optionalDrains, string(cond.Type)) {
			if cond.Status == v1.ConditionTrue {
				log.Infow("Node matches optional drain condition", "node", node.Name, "condition", cond.Type, "traceCtx", ctx)
				return true, nil
			}
		}
	}

	return false, nil
}
