package main

import (
	"context"
	"fmt"
	"os"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/pkg/imds"
	n "github.com/amargherio/mechanic/pkg/node"
	"go.uber.org/zap"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

func main() {
	// initialize zap logging - check for prod env and if it's prod, then use the prod logger. otherwise use dev
	var logger *zap.Logger
	var defaultLevel zap.AtomicLevel = zap.NewAtomicLevel()
	defaultLevel.SetLevel(zap.InfoLevel)
	env := os.Getenv("ENV")
	if env == "" {
		env = "dev"
	}

	// build out logger based on the environment
	if env == "prod" {
		config := zap.NewProductionConfig()
		config.Level = defaultLevel
		logger, _ = config.Build()
	} else {
		logger, _ = zap.NewDevelopment()
	}

	defer logger.Sync()
	log := logger.Sugar()

	// continue with app startup
	state := appstate.State{
		HasEventScheduled: false,
		IsCordoned:        false,
		IsDrained:         false,
		ShouldDrain:       false,
	}

	vals := config.ContextValues{
		Logger: log,
		State:  &state,
	}

	ctx := context.WithValue(context.Background(), "values", vals)

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

	// create the IMDS client
	log.Debugw("Getting the IMDS client object")
	ic := imds.IMDSClient{}

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
		conditionsLoop:
			for _, condition := range conditions {
				if condition.Type == "VMEventScheduled" {
					// check the status of the condition. if it's true, update state.HasEventScheduled to true. if it's false, reset it to false and
					// remove the cordon if we're the ones who cordoned it
					switch condition.Status {
					case "True":
						log.Infow("Node has an upcoming scheduled event. Querying IMDS to determine if a drain is required",
							"node", node.Name,
							"lastTransitionTime", condition.LastTransitionTime,
							"reason", condition.Reason,
							"message", condition.Message)
						state.HasEventScheduled = true
						break conditionsLoop
					case "False":
						log.Infow("Node has no upcoming scheduled events", "node", node.Name)
						state.HasEventScheduled = false
						break conditionsLoop
					}
				}
			}

			log.Debugw("Finished checking node conditions and current state.", "node", node.Name, "state", state)

			if state.HasEventScheduled {
				// query IMDS for more information on the scheduled event
				err := imds.CheckIfDrainRequired(ctx, ic, node)
				if err != nil {
					log.Errorw("Failed to query IMDS for scheduled event information", "error", err)
				}

				if state.ShouldDrain {
					// cordon the node, then drain
					log.Infow("A drain has been determined as appropriate for the node", "node", node.Name)

					// attempt to cordon the node
					err := n.CordonNode(ctx, clientset, node)
					if err != nil {
						log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
					}

					if state.IsCordoned {
						log.Infow("Node is already cordoned, skipping cordon", "node", node.Name)
					} else {
						err := n.CordonNode(ctx, clientset, node)
						if err != nil {
							log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
						} else {
							state.IsCordoned = true
							log.Infow("Node cordoned", "node", node.Name)
						}
					}

					if state.IsDrained {
						log.Infow("Node is already drained, skipping drain", "node", node.Name)
					} else {
						err := n.DrainNode(ctx, clientset, node)
						if err != nil {
							log.Errorw("Failed to drain node", "node", node.Name, "error", err)
						} else {
							state.IsDrained = true
							log.Infow("Node drained", "node", node.Name)
						}
					}
				}
			} else {
				if state.IsCordoned {
					// check for the mechanic cordoned label - if it's there and there's no event scheduled, uncordon the node and remove the label
					if _, ok := node.Labels["mechanic.cordoned"]; ok {
						log.Infow("Node is cordoned by mechanic but no scheduled events found. Uncordoning node and removing the label", "node", node.Name)

						err := n.UncordonNode(ctx, clientset, node)
						if err != nil {
							log.Errorw("Failed to uncordon node", "node", node.Name, "error", err)
						} else {
							log.Infow("Node uncordoned", "node", node.Name)
						}
					} else {
						log.Infow("Node is cordoned but does not have the mechanic label - no action required to uncordon", "node", node.Name)
					}
				}
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
