package node

import (
	"context"
	"errors"

	"github.com/amargherio/mechanic/internal/config"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
	"k8s.io/kubectl/pkg/drain"
)

func CordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) error {
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
		return nil
	}

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		n, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}

		// update the labels to show mechanic cordoned the node and cordon the node
		n.Spec.Unschedulable = true
		log.Debugw("Unschedulable set to true on node object")

		labels := n.GetLabels()
		labels["mechanic.cordoned"] = "true"
		n.SetLabels(labels)
		log.Debugw("Labels updated on node object with mechanic.cordoned label")

		_, err = clientset.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
		return err
	})
	if retryErr != nil {
		log.Warnw("Failed to cordon node - retry error encountered", "node", node.Name, "error", retryErr)
		return retryErr
	}

	res_node, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		log.Warnw("Failed to get node after cordon - returning without updating state", "node", node.Name, "error", err)
		return err
	}

	// validate result node state
	if !res_node.Spec.Unschedulable {
		log.Errorw("Node was not cordoned", "node", node.Name)
		return errors.New("node was not cordoned")
	}

	if node.GetLabels()["mechanic.cordoned"] != "true" {
		log.Errorw("Node was not labeled as cordoned by mechanic", "node", node.Name)
		return errors.New("node was not labeled as cordoned by mechanic")
	}

	// successfully cordoned
	log.Infow("Node cordoned", "node", node.Name)
	vals.State.IsCordoned = true
	return nil
}

func UncordonNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) error {
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

func DrainNode(ctx context.Context, clientset kubernetes.Interface, node *v1.Node) error {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	// drain the node
	log.Infow("Beginning node drain", "node", node.Name)
	drainHelper := &drain.Helper{
		Client:              clientset,
		Force:               true,
		DeleteEmptyDirData:  true,
		IgnoreAllDaemonSets: true,
		Timeout:             0,
	}

	if err := drain.RunNodeDrain(drainHelper, node.Name); err != nil {
		return err
	}

	vals.State.IsDrained = true
	return nil
}
