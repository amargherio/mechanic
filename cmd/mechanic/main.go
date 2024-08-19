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
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/scheme"
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
		zconfig := zap.NewProductionConfig()
		zconfig.Level = defaultLevel
		logger, _ = zconfig.Build()
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

	// set up our event recorder and add it to the context values.
	broadcaster := record.NewBroadcaster()
	broadcaster.StartLogging(log.Infof)
	broadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
	recorder := broadcaster.NewRecorder(scheme.Scheme, v1.EventSource{Component: "mechanic"})

	// create the IMDS client
	log.Debugw("Getting the IMDS client object")
	ic := imds.IMDSClient{}

	// sync app state with current node status
	node, err := clientset.CoreV1().Nodes().Get(ctx, cfg.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Errorw("Failed to get node", "error", err)
		return
	}

	state.IsCordoned = node.Spec.Unschedulable

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

			state.HasEventScheduled = checkNodeConditions(ctx, node)

			log.Debugw("Finished checking node conditions and current state.", "node", node.Name, "state", state)

			if state.HasEventScheduled {
				// query IMDS for more information on the scheduled event
				b, err := imds.CheckIfDrainRequired(ctx, ic, node)
				if err != nil {
					log.Errorw("Failed to query IMDS for scheduled event information", "error", err)
				}

				state.ShouldDrain = b

				if state.ShouldDrain {
					// cordon the node, then drain
					log.Infow("A drain has been determined as appropriate for the node", "node", node.Name)

					// check state and attempt to cordon if required
					if state.IsCordoned {
						log.Infow("Node is already cordoned, skipping cordon", "node", node.Name)
						recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s is already cordoned, no need to attempt a cordon.", node.Name)
					} else {
						err := n.CordonNode(ctx, clientset, node, &state)
						if err != nil {
							log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
							recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
						} else {
							state.IsCordoned = true
							log.Infow("Node cordoned", "node", node.Name)
							recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
						}
					}

					if state.IsDrained {
						log.Infow("Node is already drained, skipping drain", "node", node.Name)
					} else {
						err := n.DrainNode(ctx, clientset, node, &state)
						if err != nil {
							log.Errorw("Failed to drain node", "node", node.Name, "error", err)
							recorder.Eventf(node, v1.EventTypeWarning, "DrainNode", "Failed to drain node %s", node.Name)
						} else {
							state.IsDrained = true
							log.Infow("Node drained", "node", node.Name)
							recorder.Eventf(node, v1.EventTypeNormal, "DrainNode", "Node %s drained by mechanic", node.Name)
						}
					}
				}
				checkForUnneededCordon(ctx, clientset, node, recorder, &state)
			} else {
				// if we don't have an event scheduled, check if we need to uncordon the node
				checkForUnneededCordon(ctx, clientset, node, recorder, &state)
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

func checkNodeConditions(ctx context.Context, node *v1.Node) bool {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	resp := false
	conditions := node.Status.Conditions
	for _, condition := range conditions {
		if resp == true {
			break
		} else {
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

func checkForUnneededCordon(ctx context.Context, clientset *kubernetes.Clientset, node *v1.Node, recorder record.EventRecorderLogger, state *appstate.State) {
	vals := ctx.Value("values").(config.ContextValues)
	log := vals.Logger

	if state.IsCordoned || node.Spec.Unschedulable {
		// if our state shows cordoned but the node isn't unschedulable, update the state and remove labels if required
		if !node.Spec.Unschedulable {
			log.Infow("Node was not cordoned, state is out of sync. Updating the state and removing labels",
				"node", node.Name,
				"state", state,
			)
			state.IsCordoned = false
			if _, ok := node.Labels["mechanic.cordoned"]; ok {
				delete(node.Labels, "mechanic.cordoned")
				_, err := clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
				if err != nil {
					log.Errorw("Failed to update node labels", "node", node.Name, "error", err)
					recorder.Eventf(node, v1.EventTypeWarning, "UpdateNodeLabels", "Failed to update labels on node %s", node.Name)
				} else {
					log.Infow("Node labels updated", "node", node.Name)
					recorder.Eventf(node, v1.EventTypeNormal, "UpdateNodeLabels", "Node %s labels updated by mechanic", node.Name)
				}
			}
		} else {
			// the node is cordoned, so check for our label and if we don't need to cordon it anymore, remove the label and uncordon
			if _, ok := node.Labels["mechanic.cordoned"]; ok {
				log.Infow("Node is cordoned by mechanic but no scheduled events found. Uncordoning node and removing the label", "node", node.Name)

				err := n.UncordonNode(ctx, clientset, node, state)
				if err != nil {
					log.Errorw("Failed to uncordon node", "node", node.Name, "error", err)
					recorder.Eventf(node, v1.EventTypeWarning, "UncordonNode", "Failed to uncordon node %s", node.Name)
				} else {
					log.Infow("Node uncordoned", "node", node.Name)
					recorder.Eventf(node, v1.EventTypeNormal, "UncordonNode", "Node %s uncordoned by mechanic", node.Name)
				}
			} else {
				log.Infow("Node is cordoned but does not have the mechanic label - no action required to uncordon", "node", node.Name)
			}
		}
	}
}
