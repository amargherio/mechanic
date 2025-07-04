package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"time"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/internal/logging"
	"github.com/amargherio/mechanic/internal/tracing"
	"github.com/amargherio/mechanic/pkg/imds"
	n "github.com/amargherio/mechanic/pkg/node"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
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
)

func main() {
	var logger *zap.Logger
	var ctx context.Context

	state := appstate.State{
		HasDrainableCondition:     false,
		ConditionIsScheduledEvent: false,
		IsCordoned:                false,
		IsDrained:                 false,
		ShouldDrain:               false,
	}

	// tracing bootstrapping
	tp, _ := tracing.InitTracer()
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

	// if BypassNodeProblemDetector is true, we don't set up the informer for node updates
	if cfg.BypassNodeProblemDetector {
		log.Infow("Bypassing Node Problem Detector, not setting up informer and querying IMDS directly", "node", cfg.NodeName)

		// Create a cancellable context for graceful shutdown
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Start periodic IMDS querying with 10s interval and jitter
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Add jitter of ±0.5 seconds
				jitter := time.Duration((rand.Float64() - 0.5) * float64(time.Second))
				time.Sleep(jitter)

				handleIMDSCheck(ctx, clientset, &cfg, &state, &ic, recorder, tracer, log)
			case <-ctx.Done():
				log.Infow("Context cancelled, shutting down IMDS monitoring", "node", cfg.NodeName)
				return
			}
		}
	} else {

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
						"node", cfg.NodeName,
						"traceCtx", ctx)
					return
				}
				log.Debugw("Locked state object", "node", cfg.NodeName,
					"state", &state,
					"traceCtx", ctx)
				defer func() {
					state.Lock.Unlock()
					log.Debugw("Unlocked state object",
						"node", cfg.NodeName,
						"state", &state,
						"traceCtx", ctx)
				}()

				node := new.(*v1.Node)
				log.Infow("Node updated, checking for updated conditions",
					"node", node.Name,
					"traceCtx", ctx)

				state.HasDrainableCondition, state.ConditionIsScheduledEvent = n.CheckNodeConditions(ctx, node, &cfg.ScheduledEventDrainConditions, &cfg.OptionalDrainConditions)

				log.Infow("Finished checking node conditions and current state.", "node", node.Name, "state", &state, "traceCtx", ctx)

				if state.HasDrainableCondition {
					// early return if the node is already cordoned and drained
					if state.IsCordoned && state.IsDrained {
						log.Infow("Node is already cordoned and drained, no action required", "node", node.Name, "state", &state, "traceCtx", ctx)
						return
					}

					state.ShouldDrain = true // setting the drain decision to true unless we can overturn it

					// if the condition is a scheduled event, we need to check and differentiate between a freeze event and a live migration
					if state.ConditionIsScheduledEvent {
						log.Infow("Node has a scheduled event condition, checking for freeze or live migration", "node", node.Name, "state", &state, "traceCtx", ctx)
						isLM, err := imds.CheckIfFreezeOrLiveMigration(ctx, ic, node, &cfg.ScheduledEventDrainConditions)
						if err != nil {
							log.Errorw("Failed to query IMDS for scheduled event information. Unable to determine if drain is required.", "error", err, "state", &state, "traceCtx", ctx)
							return
						}

						if !isLM && !cfg.ScheduledEventDrainConditions.Freeze {
							log.Infow("Node has a freeze event that is not a live migration. We don't currently drain for freeze events, so setting our drain decision to false.", "node", node.Name, "state", &state, "traceCtx", ctx)
							state.ShouldDrain = false
						} else if isLM && !cfg.ScheduledEventDrainConditions.LiveMigration {
							log.Infow("Node has a live migration event but draining for live migration is disabled. Setting our drain decision to false.", "node", node.Name, "state", &state, "traceCtx", ctx)
							state.ShouldDrain = false
						} else {
							log.Infow("Node has a scheduled event condition that is a live migration. We will drain for this event.", "node", node.Name, "state", &state, "traceCtx", ctx)
						}
					}

					if state.ShouldDrain {
						// cordon the node, then drain
						log.Infow("A drain has been determined as appropriate for the node", "node", node.Name, "state", &state, "traceCtx", ctx)

						// check state and attempt to cordon if required
						if state.IsCordoned {
							log.Infow("Node is already cordoned, skipping cordon", "node", node.Name, "state", &state, "traceCtx", ctx)
							recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s is already cordoned, no need to attempt a cordon.", node.Name)
						} else {
							b, err := n.CordonNode(ctx, clientset, node)
							if err != nil {
								log.Errorw("Failed to cordon node", "node", node.Name, "error", err, "traceCtx", ctx)
								recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
							} else {
								state.IsCordoned = b
								log.Infow("Node cordoned", "node", node.Name, "state", &state, "traceCtx", ctx)
								recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
							}
						}

						if state.IsDrained {
							log.Infow("Node is already drained, skipping drain", "node", node.Name, "traceCtx", ctx)
						} else {
							b, err := n.DrainNode(ctx, clientset, node)
							if err != nil {
								log.Errorw("Failed to drain node", "node", node.Name, "error", err, "traceCtx", ctx)
								recorder.Eventf(node, v1.EventTypeWarning, "DrainNode", "Failed to drain node %s", node.Name)
							} else {
								state.IsDrained = b
								log.Infow("Node drain completed", "node", node.Name, "state", &state, "traceCtx", ctx)
								recorder.Eventf(node, v1.EventTypeNormal, "DrainNode", "Node %s drained by mechanic", node.Name)
							}
						}
					}
				}
				// finished the event checking, cordon, and drain logic. checking for unneeded cordons now. grab an updated
				// node object that should reflect all of our changes and use that for the ValidateCordon
				log.Infow("Checking for unneeded cordon", "node", node.Name, "state", &state, "traceCtx", ctx)
				updated, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
				if err != nil {
					log.Errorw("Failed to get updated node object", "node", node.Name, "error", err, "state", &state, "traceCtx", ctx)
					return
				}
				n.ValidateCordon(ctx, clientset, updated, recorder)

				log.Infow("Finished processing node update", "node", node.Name, "state", &state, "traceCtx", ctx)
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
}

// handleIMDSCheck performs the IMDS check and node processing logic when bypassing Node Problem Detector
func handleIMDSCheck(ctx context.Context, clientset kubernetes.Interface, cfg *config.Config, state *appstate.State, ic *imds.IMDSClient, recorder record.EventRecorder, tracer trace.Tracer, log *zap.SugaredLogger) {
	ctx, span := tracer.Start(ctx, "handleIMDSCheck")
	defer span.End()

	// lock the state object so we know we have it exclusively for this function
	didLock := state.Lock.TryLock()
	if !didLock {
		log.Warnw("Failed to lock state object, skipping IMDS check",
			"node", cfg.NodeName,
			"traceCtx", ctx)
		return
	}
	log.Debugw("Locked state object for IMDS check", "node", cfg.NodeName,
		"state", state,
		"traceCtx", ctx)
	defer func() {
		state.Lock.Unlock()
		log.Debugw("Unlocked state object after IMDS check",
			"node", cfg.NodeName,
			"state", state,
			"traceCtx", ctx)
	}()

	// Get current node state
	node, err := clientset.CoreV1().Nodes().Get(ctx, cfg.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Errorw("Failed to get node during IMDS check", "error", err, "node", cfg.NodeName, "traceCtx", ctx)
		return
	}

	log.Infow("Performing IMDS check for node", "node", node.Name, "traceCtx", ctx)

	// Update cordon state from current node status
	state.IsCordoned = node.Spec.Unschedulable

	// Check IMDS directly for drain requirements
	shouldDrain, err := imds.CheckIfDrainRequired(ctx, ic, node, &cfg.ScheduledEventDrainConditions, &cfg.OptionalDrainConditions)
	if err != nil {
		log.Errorw("Failed to check if drain is required from IMDS", "error", err, "node", node.Name, "traceCtx", ctx)
		return
	}

	// Update state based on IMDS check
	state.HasDrainableCondition = shouldDrain
	state.ShouldDrain = shouldDrain

	log.Infow("Finished IMDS check", "node", node.Name, "shouldDrain", shouldDrain, "state", state, "traceCtx", ctx)

	if state.HasDrainableCondition && state.ShouldDrain {
		// early return if the node is already cordoned and drained
		if state.IsCordoned && state.IsDrained {
			log.Infow("Node is already cordoned and drained, no action required", "node", node.Name, "state", state, "traceCtx", ctx)
			return
		}

		log.Infow("IMDS check determined drain is required for the node", "node", node.Name, "state", state, "traceCtx", ctx)

		// check state and attempt to cordon if required
		if state.IsCordoned {
			log.Infow("Node is already cordoned, skipping cordon", "node", node.Name, "state", state, "traceCtx", ctx)
			recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s is already cordoned, no need to attempt a cordon.", node.Name)
		} else {
			b, err := n.CordonNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to cordon node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "CordonNode", "Failed to cordon node %s", node.Name)
			} else {
				state.IsCordoned = b
				log.Infow("Node cordoned", "node", node.Name, "state", state, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "CordonNode", "Node %s cordoned by mechanic", node.Name)
			}
		}

		if state.IsDrained {
			log.Infow("Node is already drained, skipping drain", "node", node.Name, "traceCtx", ctx)
		} else {
			b, err := n.DrainNode(ctx, clientset, node)
			if err != nil {
				log.Errorw("Failed to drain node", "node", node.Name, "error", err, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeWarning, "DrainNode", "Failed to drain node %s", node.Name)
			} else {
				state.IsDrained = b
				log.Infow("Node drain completed", "node", node.Name, "state", state, "traceCtx", ctx)
				recorder.Eventf(node, v1.EventTypeNormal, "DrainNode", "Node %s drained by mechanic", node.Name)
			}
		}
	}

	// Check for unneeded cordon
	log.Infow("Checking for unneeded cordon", "node", node.Name, "state", state, "traceCtx", ctx)
	updated, err := clientset.CoreV1().Nodes().Get(ctx, node.Name, metav1.GetOptions{})
	if err != nil {
		log.Errorw("Failed to get updated node object", "node", node.Name, "error", err, "state", state, "traceCtx", ctx)
		return
	}
	n.ValidateCordon(ctx, clientset, updated, recorder)

	log.Infow("Finished processing IMDS check", "node", node.Name, "state", state, "traceCtx", ctx)
}
