package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/store"
)

// Phase C (proposal §12) acceptance tests:
//   C1  invalid patch → CreateProposal rejected (V1-V6 fail fast)
//   C2  ApproveProposal opens a NEW DRAFT (changelog = hypothesis), never
//       publishes; re-approve → conflict; base stays immutable
//   C3  ProposalDiff lists exactly the touched nodes/edges
//   C4  3 consecutive isomorphic infer shapes → auto-draft v1;
//       different shapes don't trigger; not double-drafted
//   C5  GoalABCompare returns both faces + numeric deltas
//   C6  RejectProposal flips status and forbids later approve

// propCluster wires RSI + Blueprint together (RecordInferShape needs both;
// Phase D auto-proposals need the goal service to create training sessions).
func propCluster(t *testing.T) (*GoalService, *RunService, *BlueprintService, *RSIService, *store.Store) {
	t.Helper()
	gs, rs, _, st := newTestCluster(t)
	bs := NewBlueprintService(st, events.NewBus())
	rsi := NewRSIService(st, events.NewBus())
	rsi.SetBlueprint(bs)
	gs.SetRSI(rsi)
	return gs, rs, bs, rsi, st
}

// publishedChain seeds a published blueprint `name` with a 2-task chain and
// returns (blueprintID, versionID, workerID).
func publishedChain(t *testing.T, bs *BlueprintService, st *store.Store, name string) (string, string, string) {
	t.Helper()
	worker := seedAgent(t, st, "w-"+strings.ToLower(name))
	content := strings.ReplaceAll(bpJSON(name, "second step"), "agent:WORKER", "agent:"+worker)
	bp, err := bs.Create(context.Background(), name, "desc", content, "human", "")
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	_, versions, err := bs.Get(context.Background(), bp.ID)
	if err != nil || len(versions) == 0 {
		t.Fatalf("versions for %s: %v", name, err)
	}
	v, err := bs.Publish(context.Background(), versions[0].ID, "init")
	if err != nil {
		t.Fatalf("publish %s: %v", name, err)
	}
	return bp.ID, v.ID, worker
}

func patchAdd(title, id, workerID string) BlueprintPatch {
	return BlueprintPatch{Op: "add_node", ID: id, Node: &SpecNode{
		ID: id, Type: "task", Title: title,
		Task: &SpecTask{Assignee: "agent:" + workerID, Brief: "added by proposal"},
	}}
}

// TestBlueprintProposalInvalidPatchRejected (C1): a patch that would make the
// graph invalid must fail AT CREATE time — never reach the approve gate.
func TestBlueprintProposalInvalidPatchRejected(t *testing.T) {
	_, _, bs, _, st := propCluster(t)
	bpID, baseID, _ := publishedChain(t, bs, st, "prop-invalid")

	// delete_node n9 (the end) leaves n2 unable to reach any end → V2 rejects.
	p, err := bs.CreateProposal(context.Background(), bpID, baseID,
		"kill the end node", "fewer nodes faster", "", "",
		[]BlueprintPatch{{Op: "delete_node", ID: "n9"}}, "", "human", "")
	if err == nil {
		t.Fatalf("expected V2 rejection at create, got proposal %v", p)
	}
	if !strings.Contains(err.Error(), "cannot reach any end node") {
		t.Fatalf("expected end-reachability validation error, got: %v", err)
	}
}

// TestBlueprintProposalApproveOpensDraftNotPublish (C2 + C6): approval opens
// a new draft with changelog=hypothesis; the draft is NOT published; the
// proposal cannot be decided twice; a rejected proposal cannot be approved.
func TestBlueprintProposalApproveOpensDraftNotPublish(t *testing.T) {
	_, _, bs, _, st := propCluster(t)
	bpID, baseID, worker := publishedChain(t, bs, st, "prop-approve")

	patches := []BlueprintPatch{
		patchAdd("review the work", "n_r", worker),
		// re-wire: n1 → n_r → n2 (replace the direct n1→n2 edge)
		{Op: "delete_edge", Edge: &SpecEdge{Source: "n1", SourceHandle: "done", Target: "n2"}},
		{Op: "add_edge", Edge: &SpecEdge{Source: "n1", SourceHandle: "done", Target: "n_r"}},
		{Op: "add_edge", Edge: &SpecEdge{Source: "n_r", SourceHandle: "done", Target: "n2"}},
	}
	p, err := bs.CreateProposal(context.Background(), bpID, baseID,
		"add a review step", "adding a reviewer step catches bugs earlier", "", "",
		patches, "", "human", "")
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}

	app, draft, err := bs.ApproveProposal(context.Background(), p.ID, "human", "")
	if err != nil {
		t.Fatalf("approve proposal: %v", err)
	}
	if app.Status != "approved" || app.AppliedVersionID != draft.ID {
		t.Fatalf("proposal bookkeeping wrong: status=%s applied=%s", app.Status, app.AppliedVersionID)
	}
	if draft.Status != "draft" {
		t.Fatalf("approval must open a DRAFT, got status=%s (auto-publish is forbidden)", draft.Status)
	}
	if draft.Changelog != p.Hypothesis {
		t.Fatalf("draft changelog must equal hypothesis, got %q", draft.Changelog)
	}
	// base version stays untouched.
	base, err := bs.GetVersion(context.Background(), baseID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(base.Content, "n1") {
		t.Fatal("base content mutated — published versions are immutable")
	}

	// re-approve → conflict.
	if _, _, err := bs.ApproveProposal(context.Background(), p.ID, "human", ""); err == nil {
		t.Fatal("re-approve must be rejected")
	}

	// a second VALID proposal (chain n2 → n_x → n9): reject it, then approve
	// must be refused (terminal decision, one shot).
	p2, err := bs.CreateProposal(context.Background(), bpID, baseID,
		"another", "h2", "", "",
		[]BlueprintPatch{
			patchAdd("extra", "n_x", worker),
			{Op: "delete_edge", Edge: &SpecEdge{Source: "n2", SourceHandle: "done", Target: "n9"}},
			{Op: "add_edge", Edge: &SpecEdge{Source: "n2", SourceHandle: "done", Target: "n_x"}},
			{Op: "add_edge", Edge: &SpecEdge{Source: "n_x", SourceHandle: "done", Target: "n9"}},
		}, "", "human", "")
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}
	if _, err := bs.RejectProposal(context.Background(), p2.ID, "human", ""); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if _, _, err := bs.ApproveProposal(context.Background(), p2.ID, "human", ""); err == nil {
		t.Fatal("approve after reject must be rejected")
	}
}

// TestBlueprintProposalDiffListsTouched (C3): diff shows exactly the added
// node + edges, base graph unchanged in the base face.
func TestBlueprintProposalDiffListsTouched(t *testing.T) {
	_, _, bs, _, st := propCluster(t)
	bpID, baseID, worker := publishedChain(t, bs, st, "prop-diff")

	patches := []BlueprintPatch{
		patchAdd("review the work", "n_r", worker),
		{Op: "delete_edge", Edge: &SpecEdge{Source: "n1", SourceHandle: "done", Target: "n2"}},
		{Op: "add_edge", Edge: &SpecEdge{Source: "n1", SourceHandle: "done", Target: "n_r"}},
		{Op: "add_edge", Edge: &SpecEdge{Source: "n_r", SourceHandle: "done", Target: "n2"}},
	}
	p, err := bs.CreateProposal(context.Background(), bpID, baseID,
		"add a review step", "h", "", "",
		patches, "", "human", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	d, err := bs.ProposalDiff(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if len(d.NodesAdded) != 1 || d.NodesAdded[0] != "n_r" {
		t.Fatalf("nodes_added = %v, want [n_r]", d.NodesAdded)
	}
	if len(d.NodesRemoved) != 0 || len(d.NodesChanged) != 0 {
		t.Fatalf("unexpected removed/changed: %v / %v", d.NodesRemoved, d.NodesChanged)
	}
	if len(d.Base.Nodes) != 3 || len(d.Patched.Nodes) != 4 {
		t.Fatalf("base=%d patched=%d nodes", len(d.Base.Nodes), len(d.Patched.Nodes))
	}
	// the two new edges appear in patched, not in base.
	if len(d.EdgesAdded) != 2 {
		t.Fatalf("edges_added = %v", d.EdgesAdded)
	}
	if len(d.EdgesRemoved) != 1 {
		t.Fatalf("edges_removed = %v (n1→n2 re-wire)", d.EdgesRemoved)
	}
}

// runInferGoalToEnd creates an infer goal with one worker sub-goal, runs it to
// done, and returns the goal id. Shape: goal + 1 agent + 1 sub_goal (verified
// machine) — always the same signature.
func runInferGoalToEnd(t *testing.T, gs *GoalService, rs *RunService, st *store.Store, domainID string) string {
	t.Helper()
	worker := seedAgent(t, st, "w-"+newID()[:8])
	g, err := gs.Create(context.Background(), Goal{
		Title: "t", AssigneeType: "agent", AssigneeID: worker,
		Status: "active", DomainID: domainID, Category: "hootl",
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(context.Background(), g.ID, "s", "d", worker, "", nil, "human", "")
	if err != nil {
		t.Fatalf("subgoal: %v", err)
	}
	var runID string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY created_at DESC LIMIT 1`, sg.ID).
		Scan(&runID); err != nil {
		t.Fatalf("no run for sub_goal %s: %v", sg.ID, err)
	}
	if err := rs.Finish(context.Background(), runID, "completed", "ok"); err != nil {
		t.Fatalf("finish: %v", err)
	}
	// owner run to let the guard finalize the goal.
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	goalObj, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(context.Background(), *goalObj); err != nil {
		t.Fatalf("owner enqueue: %v", err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatalf("no owner run: %v", err)
	}
	if err := rs.Finish(context.Background(), ownerRun, "completed", "done"); err != nil {
		t.Fatalf("owner finish: %v", err)
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatalf("reconcile2: %v", err)
	}
	got, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("goal status=%s, want done", got.Status)
	}
	return g.ID
}

// TestInferShapeThreeIsomorphismsDraftV1 (C4): three structurally identical
// infer goals in one domain → a draft blueprint appears; a fourth identical
// goal does NOT double-draft (name-collision idempotence); a different-shape
// goal in between breaks the streak and does not draft.
func TestInferShapeThreeIsomorphismsDraftV1(t *testing.T) {
	gs, rs, bs, rsi, st := propCluster(t)
	dom := seedDomain(t, st)

	// three identical shapes → draft appears.
	runInferGoalToEnd(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ := bs.List(context.Background())
	if len(bps) != 0 {
		t.Fatalf("after 1 shape there must be no draft, got %d", len(bps))
	}
	runInferGoalToEnd(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 0 {
		t.Fatalf("after 2 shapes there must be no draft, got %d", len(bps))
	}
	runInferGoalToEnd(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("after 3 isomorphic shapes exactly one draft expected, got %d", len(bps))
	}
	if bps[0].LatestVersionID != "" {
		t.Fatalf("emergent draft must NOT be published (latest_version_id=%q)", bps[0].LatestVersionID)
	}
	if !strings.HasPrefix(bps[0].Name, "固化：") {
		t.Fatalf("emergent draft name = %q", bps[0].Name)
	}

	// a fourth identical goal must not double-draft (UNIQUE name absorbs it).
	runInferGoalToEnd(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), lastGoalID(t, st)); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("double-draft detected: %d blueprints", len(bps))
	}

	// a DIFFERENT-shape goal (two sub_goals → different signature) breaks the
	// streak and drafts nothing on its own.
	g := differentShapeGoal(t, gs, rs, st, dom)
	if err := rsi.RecordInferShape(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	bps, _ = bs.List(context.Background())
	if len(bps) != 1 {
		t.Fatalf("different shape must not draft: %d", len(bps))
	}
}

// differentShapeGoal builds a done infer goal with TWO sub_goals — a
// signature distinct from the single-sub_goal shape.
func differentShapeGoal(t *testing.T, gs *GoalService, rs *RunService, st *store.Store, domainID string) string {
	t.Helper()
	worker := seedAgent(t, st, "w2-"+newID()[:8])
	g, err := gs.Create(context.Background(), Goal{
		Title: "t2", AssigneeType: "agent", AssigneeID: worker,
		Status: "active", DomainID: domainID, Category: "hootl",
	})
	if err != nil {
		t.Fatal(err)
	}
	sg1, err := gs.CreateSubGoal(context.Background(), g.ID, "a", "d", worker, "", nil, "human", "")
	if err != nil {
		t.Fatal(err)
	}
	sg2, err := gs.CreateSubGoal(context.Background(), g.ID, "b", "d", worker, "", nil, "human", "")
	if err != nil {
		t.Fatal(err)
	}
	finishSubGoal := func(sgID string) {
		var runID string
		if err := st.DB().QueryRowContext(context.Background(),
			`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY created_at DESC LIMIT 1`, sgID).
			Scan(&runID); err != nil {
			t.Fatalf("no run for %s: %v", sgID, err)
		}
		if err := rs.Finish(context.Background(), runID, "completed", "ok"); err != nil {
			t.Fatal(err)
		}
	}
	finishSubGoal(sg1.ID)
	finishSubGoal(sg2.ID)
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	goalObj, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rs.EnqueueForGoal(context.Background(), *goalObj); err != nil {
		t.Fatal(err)
	}
	var ownerRun string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE goal_id=? AND role='owner' AND status IN ('queued','running') ORDER BY created_at DESC LIMIT 1`, g.ID).
		Scan(&ownerRun); err != nil {
		t.Fatalf("no owner run: %v", err)
	}
	if err := rs.Finish(context.Background(), ownerRun, "completed", "done"); err != nil {
		t.Fatal(err)
	}
	if err := gs.ReconcileGoal(context.Background(), g.ID); err != nil {
		t.Fatal(err)
	}
	got, err := gs.Get(context.Background(), g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "done" {
		t.Fatalf("status=%s", got.Status)
	}
	return g.ID
}

// lastGoalID returns the most recently created goal (runInferGoalToEnd is the
// only creator in these tests).
func lastGoalID(t *testing.T, st *store.Store) string {
	t.Helper()
	row := st.DB().QueryRow(`SELECT id FROM goal ORDER BY created_at DESC, rowid DESC LIMIT 1`)
	var id string
	if err := row.Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestGoalABCompareFacesAndDeltas (C5): compare the two earlier goals —
// metrics faces + numeric deltas present, notes mention the infer-mode caveat.
func TestGoalABCompareFacesAndDeltas(t *testing.T) {
	gs, rs, _, rsi, st := propCluster(t)
	dom := seedDomain(t, st)
	a := runInferGoalToEnd(t, gs, rs, st, dom)
	b := runInferGoalToEnd(t, gs, rs, st, dom)

	ab, err := rsi.GoalABCompare(context.Background(), a, b)
	if err != nil {
		t.Fatalf("ab compare: %v", err)
	}
	if ab.GoalA.GoalID != a || ab.GoalB.GoalID != b {
		t.Fatalf("face ids %s / %s", ab.GoalA.GoalID, ab.GoalB.GoalID)
	}
	if _, ok := ab.Deltas["critical_path_ms"]; !ok {
		t.Fatalf("critical_path_ms delta missing: %v", ab.Deltas)
	}
	if len(ab.Notes) == 0 {
		t.Fatal("infer-mode caveat note expected")
	}
	_ = json.Marshal // keep import if assertions above change
}
