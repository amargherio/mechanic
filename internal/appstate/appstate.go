package appstate

import (
	"context"
	"github.com/amargherio/mechanic/internal/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type State struct {
	HasEventScheduled bool
	IsCordoned        bool
	IsDrained         bool
	ShouldDrain       bool
}

func (s State) SyncNodeStatus(ctx context.Context, clientset *kubernetes.Clientset, name string) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	node, err := clientset.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		log.Errorw("Failed to get node", "error", err)
		return
	}

	s.IsCordoned = node.Spec.Unschedulable
}
