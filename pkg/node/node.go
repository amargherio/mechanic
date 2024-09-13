package node

import (
	"context"
	"errors"
	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

func CordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, state *appstate.State) (bool, error) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// check if our node is cordoned, which throws our app state out of sync
	if node.Spec.Unschedulable {
		if !state.IsCordoned {
			// the node is unschedulable but our state is not in sync - check if we did it, and reconcile cordoned state.
			if _, ok := node.GetLabels()["mechanic.cordoned"]; ok {
				state.IsCordoned = true
				log.Warnw("Node is cordoned, but our state is not in sync. Reconciling state.")
			} else {
				log.Infow("Node is cordoned, but we aren't responsible for the cordon.", "node", node.Name)
				// we could still benefit from the cordon and don't need to cordon again, so sync state
				state.IsCordoned = true
			}
		}
		log.Infow("Node is already cordoned", "node", node.Name, "state", state.IsCordoned)
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

func UncordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node, state *appstate.State) error {
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

	state.IsCordoned = false
	return nil
}

func DrainNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) (bool, error) {
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
