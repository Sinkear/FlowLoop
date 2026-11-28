package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/store"
)

// setRuntimeFlags flips the runtime's probe status directly — the claim
// predicate keys on rt.status, and no service call is needed to simulate a
// machine whose probe stopped seeing the CLI.
func setRuntimeAbsent(t *testing.T, st *store.Store, agentID string, absent bool) {
	t.Helper()
	st1 := "ok"
	if absent {
		st1 = "absent"
	}
	if _, err := st.DB().ExecContext(context.Background(),
		`UPDATE runtime SET status=? WHERE id=(SELECT runtime_id FROM agent WHERE id=?)`, st1, agentID); err != nil {
		t.Fatalf("set runtime status: %v", err)
	}
}

// TestClaimFrictionExplainsMachineOffline: a queued run on a machine that
// went offline must be claimable=no AND explain itself. Before ClaimFriction
// the skip was completely silent — a run stuck for hours looked exactly like
// an empty queue in the logs.
func TestClaimFrictionExplainsMachineOffline(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", AssigneeType: "agent", AssigneeID: a,
		Status: "active", DomainID: domID, Category: "hootl"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	r := enqueueFirst(t, rs, g)

	// Machine offline: the claim gate refuses the run.
	ms := NewMachineService(st)
	if _, err := ms.MarkOffline(ctx, "m1"); err != nil {
		t.Fatalf("mark offline: %v", err)
	}
	if q, err := rs.Claim(ctx, []string{a}); err != nil || q != nil {
		t.Fatalf("claim on an offline machine must return nil, got %+v err=%v", q, err)
	}
	total, oldest, blocks, err := rs.ClaimFriction(ctx, []string{a})
	if err != nil {
		t.Fatalf("claim friction: %v", err)
	}
	if total != 1 || oldest == "" {
		t.Fatalf("expected one queued run with a queued_at, got total=%d oldest=%q", total, oldest)
	}
	if len(blocks) != 1 || blocks[0].RunID != r.ID {
		t.Fatalf("expected friction for run %s, got %+v", r.ID, blocks)
	}
	if !strings.Contains(blocks[0].Reason, "machine offline") {
		t.Fatalf("reason must name the offline machine, got %q", blocks[0].Reason)
	}

	// Machine back but its probe no longer sees the CLI: the reason changes.
	if err := ms.Heartbeat(ctx, "m1"); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	setRuntimeAbsent(t, st, a, true)
	_, _, blocks, err = rs.ClaimFriction(ctx, []string{a})
	if err != nil {
		t.Fatalf("claim friction (absent): %v", err)
	}
	if len(blocks) != 1 || !strings.Contains(blocks[0].Reason, "runtime absent") {
		t.Fatalf("reason must name the absent runtime, got %+v", blocks)
	}

	// Nothing ready: the probe still reports the queue depth, with no blocks.
	total, _, blocks, err = rs.ClaimFriction(ctx, nil)
	if err != nil {
		t.Fatalf("claim friction (no ready agents): %v", err)
	}
	if total != 1 || len(blocks) != 0 {
		t.Fatalf("no-ready probe must report the queue without blocks, got total=%d blocks=%+v", total, blocks)
	}

	// Healthy again: the run claims and the probe reports nothing blocked.
	setRuntimeAbsent(t, st, a, false)
	if q, err := rs.Claim(ctx, []string{a}); err != nil || q == nil {
		t.Fatalf("claim on a healthy machine must succeed, got %+v err=%v", q, err)
	}
	if total, _, blocks, err := rs.ClaimFriction(ctx, []string{a}); err != nil || total != 0 || len(blocks) != 0 {
		t.Fatalf("empty queue must produce no friction, got total=%d blocks=%+v err=%v", total, blocks, err)
	}
}
