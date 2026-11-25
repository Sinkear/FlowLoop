package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
)

// Phase C (§8.8): rounds are the unit of improvement, so their numbering must
// compare like with like. A workflow's rounds are numbered per owning squad
// (same workflow across two projects continues the series); an auto-decomposed
// run has no workflow, so its cohort stays the project (the old rule), which
// keeps unrelated shapes from sharing a series.
func TestAnalyzeRoundNumberingScopedByWorkflowOwner(t *testing.T) {
	ctx := context.Background()
	gs, _, bs, st := bpCluster(t)
	bus := events.NewBus()
	rsi := NewRSIService(st, bus)

	leader := seedAgent(t, st, "round-leader-"+newID()[:6])
	domA := seedDomain(t, st)
	domB, err := NewDomainService(st, bus).Create(ctx, Domain{Name: "round-p2-" + newID()[:6], GitURL: "https://example.com/p2.git"})
	if err != nil {
		t.Fatalf("second domain: %v", err)
	}
	sq := insertTestSquad(t, st, leader, "")

	content := strings.ReplaceAll(plainTaskEndJSON, "agent:WORKER", "agent:"+leader)
	bp, err := bs.CreateForSquad(ctx, sq, "round-flow-"+newID()[:6], "", content, "human", "")
	if err != nil {
		t.Fatalf("workflow: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %v", err)
	}
	v, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	mk := func(title, domainID, pin string, assigneeType, assigneeID, mode string) *Goal {
		t.Helper()
		g, err := gs.Create(ctx, Goal{
			Title: title, DomainID: domainID, Status: "backlog",
			AssigneeType: assigneeType, AssigneeID: assigneeID,
			ExecutionMode: mode, ContentCapture: "redacted", SampleRate: 1, BudgetUSD: 2,
			BlueprintVersionID: pin,
		})
		if err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		// bpCluster's GoalService has no RSI wired, so the train-mode session
		// that Create would otherwise open is created here explicitly.
		if err := rsi.EnsureSession(ctx, g); err != nil {
			t.Fatalf("session for %s: %v", title, err)
		}
		return g
	}
	rounds := func(goalID string) []int {
		t.Helper()
		rows, err := st.DB().QueryContext(ctx, `SELECT round_no FROM rsi_round WHERE goal_id=? ORDER BY created_at ASC`, goalID)
		if err != nil {
			t.Fatalf("rounds: %v", err)
		}
		defer rows.Close()
		var out []int
		for rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				t.Fatalf("scan: %v", err)
			}
			out = append(out, n)
		}
		return out
	}
	analyze := func(goalID string, want ...int) {
		t.Helper()
		var sid string
		if err := st.DB().QueryRowContext(ctx, `SELECT id FROM training_session WHERE goal_id=?`, goalID).Scan(&sid); err != nil {
			t.Fatalf("session for %s: %v", goalID, err)
		}
		if err := rsi.Analyze(ctx, sid); err != nil {
			t.Fatalf("analyze %s: %v", goalID, err)
		}
		got := rounds(goalID)
		if len(got) != len(want) {
			t.Fatalf("round series for %s = %v, want %v", goalID, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("round series for %s = %v, want %v", goalID, got, want)
			}
		}
	}

	// Same workflow, same squad, two different projects → one continuous series.
	gA := mk("pinned in project A", domA, v.ID, "squad", sq, "train")
	gB := mk("pinned in project B", domB.ID, v.ID, "squad", sq, "train")
	analyze(gA.ID, 1)
	analyze(gB.ID, 2)

	// A different workflow on the same squad starts its own series.
	bp2, err := bs.CreateForSquad(ctx, sq, "round-flow2-"+newID()[:6], "", content, "human", "")
	if err != nil {
		t.Fatalf("second workflow: %v", err)
	}
	_, v2s, err := bs.Get(ctx, bp2.ID)
	if err != nil || len(v2s) == 0 {
		t.Fatalf("v2: %v", err)
	}
	v2, err := bs.Publish(ctx, v2s[0].ID, "init")
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	gC := mk("other workflow", domA, v2.ID, "squad", sq, "train")
	analyze(gC.ID, 1)

	// Auto-decompose: no workflow, so the cohort is the project. Two runs in
	// the same project share a series; another project starts fresh.
	autoA1 := mk("auto A1", domA, "", "agent", leader, "train")
	autoA2 := mk("auto A2", domA, "", "agent", leader, "train")
	autoB := mk("auto B", domB.ID, "", "agent", leader, "train")
	analyze(autoA1.ID, 1)
	analyze(autoA2.ID, 2)
	analyze(autoB.ID, 1)

	// Re-analysis continues the owner's series rather than restarting it.
	analyze(gA.ID, 1, 3)
}
