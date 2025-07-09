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
	"github.com/amargherio/mechanic/pkg/condinformer"
	"github.com/amargherio/mechanic/pkg/imds"
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
		go bypass.InitiateBypassLooper(ctx, clientset, cfg, &state, &ic, recorder, stop)
		<-stop
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

				condinformer.HandleNodeUpdate(ctx, clientset, &cfg, &state, &ic, recorder, new)
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
