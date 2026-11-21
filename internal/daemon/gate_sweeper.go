package daemon

import (
	"context"
	"time"

	"github.com/eushing/agentwork/internal/logging"
)

// gateSweepInterval is how often the daemon times out held blueprint gates
// (docs/BLUEPRINT-PROPOSAL.md §4.2/§8 Q1): a gate whose held_since has run
// past its declared timeout.after auto-resolves with timeout.action,
// decidedBy=system. The timeout is part of the gate's CONTRACT (declared in
// the spec, validated at publish) — the sweeper only enforces the clock;
// it never invents a decision the spec did not declare.
const gateSweepInterval = time.Minute

// runGateSweeper keeps the gate-timeout sweeper on a 1-minute cadence until
// ctx is cancelled. The startup pass runs synchronously (a daemon restart
// must not silently extend a gate's deadline past its timeout).
func (d *Daemon) runGateSweeper(ctx context.Context) {
	tick := time.NewTicker(gateSweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			d.sweepBlueprintGates(ctx)
		}
	}
}

// sweepBlueprintGates runs ONE gate-timeout pass. Errors are logged, never
// fatal: a transient DB error must not take the daemon down, and the next
// tick retries. ResolveBlueprintGate itself is transactional — a crash
// mid-resolve leaves the gate held and the next pass re-times it out.
func (d *Daemon) sweepBlueprintGates(ctx context.Context) {
	if d.goalSvc == nil {
		return
	}
	resolved, err := d.goalSvc.SweepBlueprintGates(ctx)
	if err != nil {
		logging.Errorf("daemon: gate sweep: %v", err)
		return
	}
	if resolved > 0 {
		logging.Infof("daemon: gate sweep: resolved %d held gate(s) by timeout", resolved)
	}
}
