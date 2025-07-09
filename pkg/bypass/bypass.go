package bypass

import (
	"context"
	"math/rand"
	"time"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/pkg/imds"
	n "github.com/amargherio/mechanic/pkg/node"
	"go.opentelemetry.io/otel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
)

const PollingInterval = 10 * time.Second

// calculateJitteredInterval calculates the next polling interval with jitter
func calculateJitteredInterval(rng *rand.Rand) time.Duration {
	// Add jitter of ±0.5 seconds to the polling interval
	jitter := time.Duration((rng.Float64() - 0.5) * float64(time.Second) * 0.5)
	return PollingInterval + jitter
}

func InitiateBypassLooper(ctx context.Context, clientset kubernetes.Interface, cfg config.Config, state *appstate.State, ic *imds.IMDSClient, recorder record.EventRecorder, stop <-chan struct{}) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/bypass")
	ctx, span := tracer.Start(ctx, "InitiateBypassLooper")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

	// Create a properly seeded random source for jitter values
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	log.Infow("Bypassing Node Problem Detector, not setting up informer and querying IMDS directly", "node", cfg.NodeName)

	// Create a cancellable context for graceful shutdown
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Start with an immediate execution, then use jittered intervals
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

	// Perform initial IMDS check immediately
	handleIMDSCheck(ctx, clientset, &cfg, state, ic, recorder)

	// Calculate first jittered interval
	nextInterval := calculateJitteredInterval(rng)
	timer = time.NewTimer(nextInterval)

	for {
		select {
		case <-timer.C:
			// Perform IMDS check
			handleIMDSCheck(ctx, clientset, &cfg, state, ic, recorder)
			
			// Calculate next jittered interval and reset timer
			nextInterval = calculateJitteredInterval(rng)
			timer.Reset(nextInterval)
			
		case <-ctx.Done():
			log.Infow("Context cancelled, shutting down IMDS monitoring", "node", cfg.NodeName)
			return
		case <-stop:
			log.Infow("Stop signal received, shutting down IMDS monitoring", "node", cfg.NodeName)
			return
		}
	}
}

// handleIMDSCheck performs the IMDS check and node processing logic when bypassing Node Problem Detector
func handleIMDSCheck(ctx context.Context, clientset kubernetes.Interface, cfg *config.Config, state *appstate.State, ic *imds.IMDSClient, recorder record.EventRecorder) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/bypass")
	ctx, span := tracer.Start(ctx, "handleIMDSCheck")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

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

	n.HandleNodeCordonAndDrain(ctx, clientset, node, state, recorder, tracer, log)
}
