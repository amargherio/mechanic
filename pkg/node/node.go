package node

import (
	"context"
	"errors"
	"github.com/amargherio/mechanic/internal/config"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubectl/pkg/drain"
	"strings"
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

func CordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) (bool, error) {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// check if our node is cordoned, which throws our app state out of sync
	if node.Spec.Unschedulable {
		if !vals.State.IsCordoned {
			// the node is unschedulable but our state is not in sync - check if we did it, and reconcile cordoned state.
			if _, ok := node.GetLabels()["mechanic.cordoned"]; ok {
				vals.State.IsCordoned = true
				log.Warnw("Node is cordoned, but our state is not in sync. Reconciling state.")
			} else {
				log.Infow("Node is cordoned, but we aren't responsible for the cordon.", "node", node.Name)
				// we could still benefit from the cordon and don't need to cordon again, so sync state
				vals.State.IsCordoned = true
			}
		}
		log.Infow("Node is already cordoned", "node", node.Name, "state", vals.State.IsCordoned)
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
		log.Debugw("Node object updated with unschedulable set to true and mechanic.cordoned label")

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to cordon node - retry error encountered", "node", node.Name, "error", retryErr)
		return false, retryErr
	}

	res_node, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		log.Warnw("Failed to get node after cordon - returning without updating state", "node", node.Name, "error", err)
		return false, err
	}

	// validate result node state
	if !res_node.Spec.Unschedulable {
		log.Errorw("Node was not cordoned", "node", node.Name)
		return false, errors.New("node was not cordoned")
	}

	if res_node.GetLabels()["mechanic.cordoned"] != "true" {
		log.Errorw("Node was not labeled as cordoned by mechanic", "node", node.Name)
		return false, errors.New("node was not labeled as cordoned by mechanic")
	}

	// successfully cordoned
	log.Infow("Node cordoned", "node", node.Name)
	return true, nil
}

func UncordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) error {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		n, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// update the labels to show mechanic cordoned the node and cordon the node
		n.Spec.Unschedulable = false
		log.Debugw("Unschedulable set to false on node object")

		labels := n.GetLabels()
		delete(labels, "mechanic.cordoned")
		n.SetLabels(labels)
		log.Debugw("Labels updated on node object with mechanic.cordoned label removed")

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to uncordon node - retry error encountered", "node", node.Name, "error", retryErr)
		return retryErr
	}

	vals.State.IsCordoned = false
	return nil
}

func DrainNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) (bool, error) {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// drain the node
	log.Infow("Beginning node drain", "node", node.Name)

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

func ValidateCordon(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, recorder record.EventRecorder) {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// potential node states:
	// - cordoned and mechanic labeled: we own the cordon as far as we know, so we can manage it
	// - cordoned but not mechanic labeled: we don't own the cordon, so we can't manage it
	// - not cordoned but mechanic labeled: we own the cordon but the node is no longer cordoned, so we need to validate
	// - not cordoned and not mechanic labeled: BAU, no action required
	// - node is cordoned but out state is not in sync: we need to reconcile the state
	// - node is not cordoned but our state is: we need to reconcile the state

	// checking if we have a scheduled event. if we do, we should make sure node and app state is in sync
	if vals.State.HasEventScheduled {
		if vals.State.IsCordoned && !node.Spec.Unschedulable {
			log.Debugw("Node has an upcoming event scheduled, state shows cordoned but node is not. Cordon the node.", "node", node.Name, "state", vals.State)
			isCordoned, err := CordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
				recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
			} else {
				log.Infow("Node cordoned", "node", node.Name)
				recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
				vals.State.IsCordoned = isCordoned
			}
		} else if !vals.State.IsCordoned && node.Spec.Unschedulable {
			log.Debugw("Node has an upcoming event scheduled, state shows not cordoned but node is. Update state to reflect actual configuration.", "node", node.Name, "state", vals.State)
			vals.State.IsCordoned = true
		} else {
			log.Debugw("No need to check for unneeded cordon, event is scheduled", "node", node.Name, "state", vals.State)
		}

		return
	}

	// we don't have an upcoming event, so check if it's cordoned or not
	if vals.State.IsCordoned {
		// did we cordon it? if so, our label should be there and we can uncordon. if the label is missing, we don't touch
		// the cordon because we can't guarantee we're the ones that cordoned it
		if _, ok := node.Labels["mechanic.cordoned"]; ok {
			log.Infow("Node is cordoned by mechanic but no scheduled events found. Uncordoning node and removing the label", "node", node.Name)

			err := UncordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to uncordon node", "node", node.Name, "error", err)
				recorder.Eventf(node, v1.EventTypeWarning, "UncordonNode", "Failed to uncordon node %s", node.Name)
			} else {
				log.Infow("Node uncordoned", "node", node.Name)
				recorder.Eventf(node, v1.EventTypeNormal, "UncordonNode", "Node %s uncordoned by mechanic", node.Name)
				vals.State.IsCordoned = false
			}
		} else {
			vals.State.IsCordoned = true
			log.Infow("Node is cordoned but does not have the mechanic label - no action required to uncordon", "node", node.Name, "state", vals.State)
		}
	} else {
		// our state shows it's not cordoned, so we should check if state is out of sync and reconcile
		if node.Spec.Unschedulable {
			if _, ok := node.Labels["mechanic.cordoned"]; ok {
				log.Warnw("Node is cordoned but our state shows it's not. No upcoming events so uncordoning the node and removing the label", "node", node.Name)
				err := UncordonNode(ctx, clientset, node)
				if err != nil {
					log.Errorw("Failed to uncordon node", "node", node.Name, "error", err)
					recorder.Eventf(node, v1.EventTypeWarning, "UncordonNode", "Failed to uncordon node %s", node.Name)
				} else {
					log.Infow("Node uncordoned", "node", node.Name)
					recorder.Eventf(node, v1.EventTypeNormal, "UncordonNode", "Node %s uncordoned by mechanic", node.Name)
					vals.State.IsCordoned = false
					removeMechanicCordonLabel(ctx, node, clientset)
				}
			} else {
				log.Infow("Node is cordoned but no mechanic label found - no action required", "node", node.Name)
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

func CheckNodeConditions(ctx context.Context, node *v1.Node, drainConditions config.DrainConditions) bool {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// iterate through the DrainConditions fields and build a list of drainable node conditions
	// todo: this feels hacky...should be a better way to do this
	drainableConditions := make([]string, 0)
	drainableConditions = append(drainableConditions, "VMEventScheduled") // always cover a generic `VMEventScheduled` condition

	if drainConditions.DrainOnFreeze {
		drainableConditions = append(drainableConditions, "FreezeScheduled")
	}
	if drainConditions.DrainOnReboot {
		drainableConditions = append(drainableConditions, "RebootScheduled")
	}
	if drainConditions.DrainOnRedeploy {
		drainableConditions = append(drainableConditions, "RedeployScheduled")
	}
	if drainConditions.DrainOnPreempt {
		drainableConditions = append(drainableConditions, "PreemptScheduled")
	}
	if drainConditions.DrainOnTerminate {
		drainableConditions = append(drainableConditions, "TerminateScheduled")
	}

	resp := false
	conditions := node.Status.Conditions
	for _, condition := range conditions {
		if resp {
			break
		} else {
			if slices.Contains(drainableConditions, string(condition.Type)) {
				// check the status of the condition. if it's true, update state.HasEventScheduled to true. if it's false, reset it to false and
				// remove the cordon if we're the ones who cordoned it
				switch condition.Status {
				case "True":
					log.Infow("Node has an upcoming scheduled event. Flagging for impact assessment.",
						"node", node.Name,
						"type", condition.Type,
						"lastTransitionTime", condition.LastTransitionTime,
						"reason", condition.Reason,
						"message", condition.Message)
					resp = true
				case "False":
					log.Infow("Node has no upcoming scheduled events", "node", node.Name)
					resp = false
				}
				break
			}
		}
	}
	return resp
}

func removeMechanicCordonLabel(ctx context.Context, node *v1.Node, clientset kubernetes.Interface) {
	tracer := otel.Tracer("mechanic")
	ctx, span := tracer.Start(ctx, "ReadConfiguration")
	defer span.End()

	vals := ctx.Value("values").(config.ContextValues)
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
		log.Warnw("Failed to remove mechanic label from node - retry error encountered", "node", node.Name, "error", retryErr)
	}
	log.Debugw("Mechanic label removed from node", "node", node.Name)
}
