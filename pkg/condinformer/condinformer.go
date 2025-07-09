package condinformer

import (
	"context"

	"github.com/amargherio/mechanic/internal/appstate"
	"github.com/amargherio/mechanic/internal/config"
	"github.com/amargherio/mechanic/pkg/imds"
	n "github.com/amargherio/mechanic/pkg/node"
	"go.opentelemetry.io/otel"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
)

func HandleNodeUpdate(ctx context.Context, clientset kubernetes.Interface, cfg *config.Config, state *appstate.State, ic *imds.IMDSClient, recorder record.EventRecorder, new interface{}) {
	tracer := otel.Tracer("github.com/amargherio/mechanic/pkg/condinformer")
	ctx, span := tracer.Start(ctx, "HandleNodeUpdate")
	defer span.End()

	vals := ctx.Value("values").(*config.ContextValues)
	log := vals.Logger

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

		n.HandleNodeCordonAndDrain(ctx, clientset, node, state, recorder, tracer, log)
	}

	log.Infow("Finished processing node update", "node", node.Name, "state", &state, "traceCtx", ctx)
}
