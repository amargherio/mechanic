package main

import (
	"context"
	"fmt"
	"github.com/amargherio/mechanic/internal/config"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubectl/pkg/drain"
)

func main() {
	// initialize zap logging - check for prod env and if it's prod, then use the prod logger. otherwise use dev
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()
	log := logger.Sugar()

	ctx := context.WithValue(context.Background(), "logger", log)

	// Read in config
	cfg, err := config.ReadConfiguration(ctx)
	if err != nil {
		log.Fatalw("Failed to read configuration", "error", err)
	}

	// get our kubernnetes client and start an informer on our node
	log.Info("Building the Kubernetes clientset")
	clientset, err := kubernetes.NewForConfig(cfg.KubeConfig)
	if err != nil {
		log.Errorw("Failed to create clientset", "error", err)
	}

	log.Info("Building the informer factory for our node informer client.")
	factory := informers.NewSharedInformerFactoryWithOptions(
		clientset,
		0,
		informers.WithTweakListOptions(func(options *metav1.ListOptions) {
			options.FieldSelector = fmt.Sprintf("metadata.name=%s", cfg.NodeName)
		}),
	)

	ni := factory.Core().V1().Nodes().Informer()
	ni.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		UpdateFunc: func(old, new interface{}) {
			node := new.(*v1.Node)
			log.Infow("Node updated, checking for updated conditions", "node", node.Name)

			conditions := node.Status.Conditions
			hasEventScheduled := false
			for _, condition := range conditions {
				if condition.Type == "VMEventScheduled" && condition.Status == "True" {
					log.Infow("Node has an upcoming scheduled event. Querying IMDS to determine if a drain is required",
						"node", node.Name,
						"lastTransitionTime", condition.LastTransitionTime,
						"reason", condition.Reason,
						"message", condition.Message)

					hasEventScheduled = true
					break
				}
			}

			if hasEventScheduled {
				// query IMDS for more information on the scheduled event
				shouldDrain := checkIfDrainRequired(ctx, node)

				if shouldDrain {
					log.Infow("A drain has been determined as appropriate for the node", "node", node.Name)
					// cordon the node, then drain
					node.Spec.Unschedulable = true
					_, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
					if err != nil {
						log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
					}
					log.Infow("Node cordoned", "node", node.Name)

					// drain the node
					err = drainNode(ctx, clientset, node)
					if err != nil {
						log.Errorw("Failed to drain node", "node", node.Name, "error", err)
					}
					log.Infow("Node drained", "node", node.Name)
				}
			} else {
				log.Infow("No scheduled events found for node in the last update", "node", node.Name)
			}
		},
	})

	stop := make(chan struct{})
	defer close(stop)

	// start the informer
	log.Infow("Starting the informer", "node", cfg.NodeName)
	factory.Start(stop)

	// wait for caches to sync
	if !cache.WaitForCacheSync(stop, ni.HasSynced) {
		log.Errorw("Failed to sync informer caches")
	}

	// block main process
	<-stop
}

func drainNode(ctx context.Context, clientset *kubernetes.Clientset, node *v1.Node) error {
	log := ctx.Value("logger").(*zap.SugaredLogger)

	drainHelper := &drain.Helper{
		Client:              clientset,
		Force:               true,
		IgnoreAllDaemonSets: true,
		DeleteEmptyDirData:  true,
		Timeout:             0,
	}

	if err := drain.RunNodeDrain(drainHelper, node.Name); err != nil {
		return err
	}
	log.Infow("Node drained", "node", node.Name, "drainOptions", drainHelper)

	return nil
}

func checkIfDrainRequired(ctx context.Context, i *v1.Node) bool {
	return false
}
