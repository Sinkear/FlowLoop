package service

// Blueprint deletion: the list page and the blueprint management page both
// expose a delete button, so this pins the two guarantees the UI relies on —
// a blue print still pinned by a non-terminal goal is refused (409), and an
// otherwise-unused blueprint goes away together with its versions/proposals.

import (
	"context"
	"errors"
	"testing"
)

// TestBlueprintDeleteRefusesWhileNonTerminalGoalPins: backlog/active/review
// goals are live authority holders; deleting their blueprint would leave a
// pinned goal pointing at a vanished spec.
func TestBlueprintDeleteRefusesWhileNonTerminalGoalPins(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	v := seedPublishedBlueprint(t, bs, worker, "delete-pinned", "second step")

	g, err := gs.Create(ctx, Goal{
		Title: "pinned backlog", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: worker, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create pinned goal: %v", err)
	}
	if err := bs.Delete(ctx, v.BlueprintID); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict while goal %s is non-terminal, got %v", g.ID, err)
	}
}

// TestBlueprintDeleteCascadesVersionsAndProposals: once no live goal pins the
// blueprint the row, its versions and its proposals all go away in one tx.
func TestBlueprintDeleteCascadesVersionsAndProposals(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	domID := seedDomain(t, st)
	v := seedPublishedBlueprint(t, bs, worker, "delete-unused", "second step")

	g, err := gs.Create(ctx, Goal{
		Title: "terminal history", Description: "d", DomainID: domID,
		AssigneeType: "agent", AssigneeID: worker, Status: "backlog",
		Category: "hootl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatalf("create pinned goal: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='done' WHERE id=?`, g.ID); err != nil {
		t.Fatalf("finish goal: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO blueprint_proposal (id, blueprint_id, base_version_id, title, status, created_at)
		 VALUES ('pp-del', ?, ?, 'proposal', 'pending', ?)`, v.BlueprintID, v.ID, now()); err != nil {
		t.Fatalf("seed proposal: %v", err)
	}

	if err := bs.Delete(ctx, v.BlueprintID); err != nil {
		t.Fatalf("delete unused blueprint: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM blueprint WHERE id=?`,
		`SELECT COUNT(*) FROM blueprint_version WHERE blueprint_id=?`,
		`SELECT COUNT(*) FROM blueprint_proposal WHERE blueprint_id=?`,
	} {
		var n int
		if err := st.DB().QueryRowContext(ctx, q, v.BlueprintID).Scan(&n); err != nil {
			t.Fatalf("count after delete: %v", err)
		}
		if n != 0 {
			t.Fatalf("row still present after delete (%s): %d", q, n)
		}
	}
	// The terminal goal keeps its historical pin row untouched.
	var pin string
	if err := st.DB().QueryRowContext(ctx, `SELECT blueprint_version_id FROM goal WHERE id=?`, g.ID).Scan(&pin); err != nil {
		t.Fatalf("read terminal goal pin: %v", err)
	}
	if pin != v.ID {
		t.Fatalf("terminal goal pin changed: %q", pin)
	}
}

func TestBlueprintDeleteUnknownIsNotFound(t *testing.T) {
	_, _, bs, _ := bpCluster(t)
	if err := bs.Delete(context.Background(), "no-such-blueprint"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// ── 版本删除：草稿可以删，已发布/被引用不可以 ──

func TestBlueprintDeleteVersionRemovesJunkDraft(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	pub := seedPublishedBlueprint(t, bs, worker, "junk-draft", "second step")
	draft, err := bs.NewDraft(ctx, pub.BlueprintID, "乱填的草稿")
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	if err := bs.DeleteVersion(ctx, draft.ID); err != nil {
		t.Fatalf("delete junk draft: %v", err)
	}
	if _, err := bs.GetVersion(ctx, draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("draft must be gone, got %v", err)
	}
	var versions int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blueprint_version WHERE blueprint_id=?`, pub.BlueprintID).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("published version must survive, got %d versions", versions)
	}
}

func TestBlueprintDeleteVersionRefusesPublished(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "worker")
	pub := seedPublishedBlueprint(t, bs, worker, "no-delete-published", "second step")
	if err := bs.DeleteVersion(context.Background(), pub.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("published version must be refused, got %v", err)
	}
}

func TestBlueprintDeleteVersionRefusesReferencedProposal(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	pub := seedPublishedBlueprint(t, bs, worker, "no-delete-referenced", "second step")
	draft, err := bs.NewDraft(ctx, pub.BlueprintID, "proposal draft")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO blueprint_proposal (id, blueprint_id, base_version_id, title, status, applied_version_id, created_at)
		 VALUES ('pp-ref', ?, ?, 'p', 'approved', ?, ?)`, pub.BlueprintID, pub.ID, draft.ID, now()); err != nil {
		t.Fatal(err)
	}
	if err := bs.DeleteVersion(ctx, draft.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft referenced by a proposal must be refused, got %v", err)
	}
}

func TestBlueprintDeleteVersionRefusesPinnedGoal(t *testing.T) {
	_, _, bs, st := bpCluster(t)
	ctx := context.Background()
	worker := seedAgent(t, st, "worker")
	pub := seedPublishedBlueprint(t, bs, worker, "no-delete-pinned-draft", "second step")
	draft, err := bs.NewDraft(ctx, pub.BlueprintID, "pinned draft")
	if err != nil {
		t.Fatal(err)
	}
	// Create() only pins published versions, so a direct row models the guard
	// against historical/orphaned data that pins a draft.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO goal (id,title,description,assignee_type,assignee_id,status,category,created_by_type,created_at,blueprint_version_id)
		 VALUES ('g-pin-draft','orphan','d','agent',?,'backlog','hootl','human',?,?)`,
		worker, now(), draft.ID); err != nil {
		t.Fatal(err)
	}
	if err := bs.DeleteVersion(ctx, draft.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("draft pinned by a goal must be refused, got %v", err)
	}
}

func TestBlueprintDeleteVersionUnknownIsNotFound(t *testing.T) {
	_, _, bs, _ := bpCluster(t)
	if err := bs.DeleteVersion(context.Background(), "no-such-version"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
