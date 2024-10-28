package main

import (
	"context"
	"fmt"
	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/internal/logging"
	"github.com/amargherio/mechanic/internal/tracing"
	"github.com/amargherio/mechanic/pkg/imds"
	n "github.com/amargherio/mechanic/pkg/node"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubectl/pkg/scheme"
	"os"
)

func main() {
	var logger *zap.Logger
	var ctx context.Context

	state := appstate.State{
		HasEventScheduled: false,
		IsCordoned:        false,
		IsDrained:         false,
		ShouldDrain:       false,
	}

	// tracing bootstrapping
	tp, err := tracing.InitTracer()
	// todo: should we defer TracerProvider shutdown here?
	tracer := otel.Tracer("github.com/amargherio/mechanic")

	// initial log bootstrapping
	var defaultLevel zap.AtomicLevel = zap.NewAtomicLevel()
	defaultLevel.SetLevel(zap.InfoLevel)

	enc := zap.NewProductionEncoderConfig()
	enc.EncodeTime = zapcore.ISO8601TimeEncoder

	baseCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(enc),
		os.Stdout,
		defaultLevel)
	traceCore := logging.NewTraceCore(baseCore, &ctx, tp)
	logger = zap.New(traceCore)
	defer logger.Sync()
	log := logger.Sugar()

	// building app context and contextvalues structs
	vals := config.ContextValues{
		Logger: logger.Sugar(),
		State:  &state,
		Tracer: &tracer,
	}
	ctx = context.WithValue(context.Background(), "values", &vals)

	cfg, err := config.ReadConfiguration(ctx)
	if err != nil {
		logger.Sugar().Warnw("Failed to read configuration", "error", err)
		return
	}

	// adjust the log level based on the config value
	if cfg.RuntimeEnv != "prod" {
		defaultLevel.SetLevel(zap.DebugLevel)
	}

	// get our kubernetes client and start an informer on our node
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
			ctx, span := tracer.Start(ctx, "nodeUpdateHandler")
			defer span.End()
			// lock the state object so we know we have it exclusively for this function
			// if we can't get the lock, then we skip processing this node update because we're already processing another one
			//
			// todo: this may need cleanup - there's no reads to state outside of processing an node update but it would be good to
			// 	 ensure that we don't end up needing a RWMutex instead.
			didLock := state.Lock.TryLock()
			if !didLock {
				log.Warnw("Failed to lock state object, skipping update",
					"node", cfg.NodeName)
				return
			}
			log.Debugw("Locked state object", "node", cfg.NodeName,
				"state", &state)
			defer func() {
				state.Lock.Unlock()
				log.Debugw("Unlocked state object",
					"node", cfg.NodeName,
					"state", &state)
			}()

			node := new.(*v1.Node)
			log.Infow("Node updated, checking for updated conditions",
				"node", node.Name)

			state.HasEventScheduled = n.CheckNodeConditions(ctx, node, cfg.DrainConditions)

			log.Infow("Finished checking node conditions and current state.", "node", node.Name, "state", &state)

			if state.HasEventScheduled {
				// early return if the node is already cordoned and drained
				if state.IsCordoned && state.IsDrained {
					log.Infow("Node is already cordoned and drained, no action required", "node", node.Name, "state", &state)
					return
				}

				// query IMDS for more information on the scheduled event
				b, err := imds.CheckIfDrainRequired(ctx, ic, node, &cfg.DrainConditions)
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
			// finished the event checking, cordon, and drain logic. checking for unneeded cordons now. grab an updated
			// node object that should reflect all of our changes and use that for the ValidateCordon
			log.Infow("Checking for unneeded cordon", "node", node.Name, "state", &state)
			updated, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
			if err != nil {
				log.Errorw("Failed to get updated node object", "node", node.Name, "error", err, "state", &state)
				return
			}
			n.ValidateCordon(ctx, clientset, updated, recorder)

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
