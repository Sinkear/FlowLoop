package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/store"
)

func insertTestSquad(t *testing.T, st *store.Store, leader, defaultWorkflow string) string {
	t.Helper()
	id := newID()
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO squad (id,name,description,leader_id,instructions,default_workflow_id,created_at)
		 VALUES (?,?,?,?,?,?,?)`, id, "sq-"+id[:8], "", leader, "", defaultWorkflow, now()); err != nil {
		t.Fatalf("insert squad: %v", err)
	}
	return id
}

// TestWorkflowOwnershipAndDefaultPin covers D1/D2: a workflow belongs to one
// squad, a squad task defaults to that squad's latest published workflow, an
// explicit pin must match the owner, and "不使用工作流" skips the default.
func TestWorkflowOwnershipAndDefaultPin(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	dom := seedDomain(t, st)
	leaderA := seedAgent(t, st, "leader-a-"+newID()[:6])
	leaderB := seedAgent(t, st, "leader-b-"+newID()[:6])
	sqA := insertTestSquad(t, st, leaderA, "")
	sqB := insertTestSquad(t, st, leaderB, "")

	content := strings.ReplaceAll(bpJSON("owned", "second"), "agent:WORKER", "agent:"+leaderA)
	bp, err := bs.CreateForSquad(ctx, sqA, "owned-flow-"+newID()[:6], "", content, "human", "")
	if err != nil {
		t.Fatalf("create owned workflow: %v", err)
	}
	_, versions, err := bs.Get(ctx, bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions: %v", err)
	}
	v, err := bs.Publish(ctx, versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	bpB, err := bs.CreateForSquad(ctx, sqB, "owned-flow-b-"+newID()[:6], "", content, "human", "")
	if err != nil {
		t.Fatalf("create B workflow: %v", err)
	}
	_, versionsB, err := bs.Get(ctx, bpB.ID)
	if err != nil || len(versionsB) == 0 {
		t.Fatalf("B versions: %v", err)
	}
	vB, err := bs.Publish(ctx, versionsB[0].ID, "init")
	if err != nil {
		t.Fatalf("publish B: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE squad SET default_workflow_id=? WHERE id=?`, bpB.ID, sqB); err != nil {
		t.Fatal(err)
	}

	// 0) a workflow owned by A cannot name squad B in its spec (D1).
	crossContent := strings.ReplaceAll(bpJSON("owned", "second"), "agent:WORKER", "squad:"+sqB)
	if _, err := bs.CreateForSquad(ctx, sqA, "cross-squad-"+newID()[:6], "", crossContent, "human", ""); err == nil || !strings.Contains(err.Error(), "another squad") {
		t.Fatalf("cross-squad spec must be refused, got %v", err)
	}

	// 1) squad B task with no pin resolves B's default latest published.
	g1, err := gs.Create(ctx, Goal{
		Title: "default pin", AssigneeType: "squad", AssigneeID: sqB,
		Status: "backlog", DomainID: dom, Category: "hootl",
	})
	if err != nil {
		t.Fatalf("create default goal: %v", err)
	}
	if g1.BlueprintVersionID != vB.ID {
		t.Fatalf("default pin = %q, want %q", g1.BlueprintVersionID, vB.ID)
	}
	if g1.ExecutionMode != "train" {
		t.Fatalf("default pin without explicit mode must enable review, got %q", g1.ExecutionMode)
	}

	// 2) explicit pin by another squad is refused before any run starts.
	if _, err := gs.Create(ctx, Goal{
		Title: "wrong owner", AssigneeType: "squad", AssigneeID: sqB,
		Status: "backlog", DomainID: dom, Category: "hootl", BlueprintVersionID: v.ID,
	}); err == nil || !strings.Contains(err.Error(), "different squad") {
		t.Fatalf("cross-squad pin must be refused, got %v", err)
	}

	// 3) opt-out skips the default and stays auto-decompose.
	g3, err := gs.Create(ctx, Goal{
		Title: "auto", AssigneeType: "squad", AssigneeID: sqB,
		Status: "backlog", DomainID: dom, Category: "hootl", SkipDefaultWorkflow: true,
	})
	if err != nil {
		t.Fatalf("create opt-out goal: %v", err)
	}
	if g3.BlueprintVersionID != "" {
		t.Fatalf("opt-out pinned %q", g3.BlueprintVersionID)
	}
	if g3.ExecutionMode != "infer" {
		t.Fatalf("opt-out must stay auto-decompose, got %q", g3.ExecutionMode)
	}

	// 4) a single agent cannot pin a squad-owned workflow.
	if _, err := gs.Create(ctx, Goal{
		Title: "agent pin", AssigneeType: "agent", AssigneeID: leaderA,
		Status: "backlog", DomainID: dom, Category: "hootl", BlueprintVersionID: vB.ID,
	}); err == nil || !strings.Contains(err.Error(), "different squad") {
		t.Fatalf("agent pin must be refused, got %v", err)
	}
}
