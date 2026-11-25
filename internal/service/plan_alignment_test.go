package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/events"
)

func TestPlanAlignmentMapsCompiledSpec(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "align-bp", strings.ReplaceAll(bpJSON("align", "second"), "agent:WORKER", "agent:"+worker))
	g, err := gs.Create(ctx, Goal{
		Title: "align", DomainID: domID, AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	al, err := gs.PlanAlignment(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !al.Pinned || !al.Materialized || al.Planned != 3 {
		t.Fatalf("alignment header wrong: %+v", al)
	}
	if al.Mapped != 2 || al.Executed != 2 || al.UnplannedSubGoals != 0 {
		t.Fatalf("node↔subgoal mapping wrong: %+v", al)
	}
	if len(al.Deviations) != 0 {
		t.Fatalf("fresh compiled plan must have no deviations, got %v", al.Deviations)
	}
	_ = worker
}

func TestPlanAlignmentFlagsConsultOnlyExecution(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	owner := seedAgent(t, st, "owner")
	guest := seedAgent(t, st, "guest")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "incident-bp", strings.ReplaceAll(bpJSON("incident", "second"), "agent:WORKER", "agent:"+owner))
	g, err := gs.Create(ctx, Goal{
		Title: "incident", DomainID: domID, AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The incident shape: active, pinned, 0 nodes, work done via a consult.
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='active' WHERE id=?`, g.ID); err != nil {
		t.Fatal(err)
	}
	ownerRun, err := NewRunService(st, events.NewBus()).EnqueueForGoal(ctx, *mustGoal(t, gs, g.ID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO consult_request (id,goal_id,requester_agent_id,requester_run_id,target_agent_id,trigger_comment_id,guest_run_id,response_comment_id,created_at)
		 VALUES (?,?,?,?,?,?,?,'',?)`,
		"cr1", g.ID, owner, ownerRun.ID, guest, "tc1", "gr1", now()); err != nil {
		t.Fatal(err)
	}
	al, err := gs.PlanAlignment(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(al.Deviations, " | ")
	if !strings.Contains(joined, "计划未物化") || !strings.Contains(joined, "咨询化执行") {
		t.Fatalf("incident shape must be flagged, got %v", al.Deviations)
	}
	// The RSI finding carries the same verdict into the training loop.
	f := NewRSIService(st, events.NewBus()).planAlignmentFindings(ctx, g.ID)
	if len(f) != 1 || f[0].Category != "plan_deviation" || f[0].Severity != "critical" {
		t.Fatalf("want one high plan_deviation finding, got %+v", f)
	}
}

func TestEnsurePlanMaterializedIsLazyAndIdempotent(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	ctx := context.Background()
	v := seedPublishedContent(t, bs, "lazy-bp", strings.ReplaceAll(bpJSON("lazy", "second"), "agent:WORKER", "agent:"+owner))
	g, err := gs.Create(ctx, Goal{
		Title: "lazy", DomainID: domID, AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='active' WHERE id=?`, g.ID); err != nil {
		t.Fatal(err)
	}
	compiled, err := gs.EnsurePlanMaterialized(ctx, g.ID)
	if err != nil || !compiled {
		t.Fatalf("lazy compile must run once, got %v (%v)", compiled, err)
	}
	var nodes int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM blueprint_node WHERE goal_id=?`, g.ID).Scan(&nodes); err != nil {
		t.Fatal(err)
	}
	if nodes == 0 {
		t.Fatalf("lazy compile must materialize the plan")
	}
	compiled, err = gs.EnsurePlanMaterialized(ctx, g.ID)
	if err != nil || compiled {
		t.Fatalf("second ensure must be a no-op, got %v (%v)", compiled, err)
	}
}

func TestConsultResponseBackfill(t *testing.T) {
	gs, _, _, st := bpCluster(t)
	agent := seedAgent(t, st, "consultee")
	domID := seedDomain(t, st)
	ctx := context.Background()
	g, err := gs.Create(ctx, Goal{Title: "c", DomainID: domID, AssigneeType: "agent", AssigneeID: agent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO consult_request (id,goal_id,requester_agent_id,requester_run_id,target_agent_id,trigger_comment_id,guest_run_id,response_comment_id,created_at)
		 VALUES ('cr-bf',?,?,?,?,'tc','gr-bf','',?)`, g.ID, agent, "owner-run", agent, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,content,run_id,created_at)
		 VALUES ('c-bf',?,'agent',?,'the answer',?,?)`, g.ID, agent, "gr-bf", now()); err != nil {
		t.Fatal(err)
	}
	gs.backfillConsultResponse(ctx, goalRunContext{GoalID: g.ID, AgentID: agent, Role: "consult", RunID: "gr-bf"})
	var got string
	if err := st.DB().QueryRowContext(ctx, `SELECT response_comment_id FROM consult_request WHERE id='cr-bf'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "c-bf" {
		t.Fatalf("response_comment_id must be back-filled, got %q", got)
	}
}
