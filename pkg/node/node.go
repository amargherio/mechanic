package node

import (
	"context"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
)

func CordonNode(ctx context.Context, clientset *kubernetes.Clientset, node *v1.Node) error {
	log := ctx.Value("logger").(*zap.SugaredLogger)

	// check if the node is cordoned
	if node.Spec.Unschedulable {
		log.Infow("Node is already cordoned, skipping cordon", "node", node.Name)
		return nil
	}

	// cordon the node
	node.Spec.Unschedulable = true
	_, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
		return err
	}

	log.Infow("Node cordoned", "node", node.Name)
	return nil
}

func UncordonNode(ctx context.Context, clientset *kubernetes.Clientset, node *v1.Node) error {
	labels := node.GetLabels()
	delete(labels, "mechanic.cordoned")

	node.Spec.Unschedulable = false
	node.SetLabels(labels)

	_, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	return nil
}

func DrainNode(ctx context.Context, clientset *kubernetes.Clientset, node *v1.Node) error {
	log := ctx.Value("logger").(*zap.SugaredLogger)

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

	return nil
}
