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
			// lock the state object so we know we have it exclusively for this function
			// if we can't get the lock, then we skip processing this node update because we're already processing another one
			//
			// todo: this may need cleanup - there's no reads to state outside of processing an node update but it would be good to
			// 	 ensure that we don't end up needing a RWMutex instead.
			didLock := state.Lock.TryLock()
			if !didLock {
				log.Warnw("Failed to lock state object, skipping update", "node", cfg.NodeName)
				return
			}
			log.Debugw("Locked state object", "node", cfg.NodeName, "state", &state)
			defer state.Lock.Unlock()

			node := new.(*v1.Node)
			log.Infow("Node updated, checking for updated conditions", "node", node.Name)

			state.HasEventScheduled = n.CheckNodeConditions(ctx, node)

			log.Infow("Finished checking node conditions and current state.", "node", node.Name, "state", &state)

			if state.HasEventScheduled {
				// early return if the node is already cordoned and drained
				if state.IsCordoned && state.IsDrained {
					log.Infow("Node is already cordoned and drained, no action required", "node", node.Name, "state", &state)
					return
				}

				// query IMDS for more information on the scheduled event
				b, err := imds.CheckIfDrainRequired(ctx, ic, node)
				if err != nil {
					log.Errorw("Failed to query IMDS for scheduled event information. Unable to determine if drain is required.", "error", err, "state", &state)
					return
				}
				state.ShouldDrain = b

				if state.ShouldDrain {
					// cordon the node, then drain
					log.Infow("A drain has been determined as appropriate for the node", "node", node.Name, "state", &state)

					// check state and attempt to cordon if required
					if state.IsCordoned {
						log.Infow("Node is already cordoned, skipping cordon", "node", node.Name)
						recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s is already cordoned, no need to attempt a cordon.", node.Name)
					} else {
						b, err := n.CordonNode(ctx, clientset, node)
						if err != nil {
							log.Errorw("Failed to cordon node", "node", node.Name, "error", err)
							recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
						} else {
							state.IsCordoned = b
							log.Infow("Node cordoned", "node", node.Name, "state", &state)
							recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
						}
					}

					if state.IsDrained {
						log.Infow("Node is already drained, skipping drain", "node", node.Name)
					} else {
						b, err := n.DrainNode(ctx, clientset, node)
						if err != nil {
							log.Errorw("Failed to drain node", "node", node.Name, "error", err)
							recorder.Eventf(node, v1.EventTypeWarning, "DrainNode", "Failed to drain node %s", node.Name)
						} else {
							state.IsDrained = b
							log.Infow("Node drain completed", "node", node.Name, "state", &state)
							recorder.Eventf(node, v1.EventTypeNormal, "DrainNode", "Node %s drained by mechanic", node.Name)
						}
					}
				}
			}
			// finished the event checking, cordon, and drain logic. checking for
			log.Infow("Checking for unneeded cordon", "node", node.Name, "state", &state)
			n.ValidateCordon(ctx, clientset, node, recorder)

			log.Infow("Finished processing node update", "node", node.Name, "state", &state)
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
