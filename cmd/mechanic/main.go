package main

import (
	"context"
	"fmt"
	"os"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/internal/logging"
	"github.com/amargherio/mechanic/internal/tracing"
	"github.com/amargherio/mechanic/pkg/bypass"
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

	// Create stop channel for graceful shutdown
	stop := make(chan struct{})
	defer close(stop)

	// if BypassNodeProblemDetector is true, we don't set up the informer for node updates
	if cfg.BypassNodeProblemDetector {
		bypass.InitiateBypassLooper(ctx, clientset, cfg, &state, &ic, recorder, stop)
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

					n.HandleNodeCordonAndDrain(ctx, clientset, node, &state, recorder, tracer, log)
				}

				log.Infow("Finished processing node update", "node", node.Name, "state", &state, "traceCtx", ctx)
			},
		})

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
