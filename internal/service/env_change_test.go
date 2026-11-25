package service

import (
	"context"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/store"
)

// envChangeSeedGoal creates the goal fixture the ledger tests record against.
// Unique helper names (envChange…) keep it from colliding with other tests in
// package service.
func envChangeSeedGoal(t *testing.T, gs *GoalService, st *store.Store) string {
	t.Helper()
	owner := seedAgent(t, st, "env-change-owner")
	domID := seedDomain(t, st)
	g, err := gs.Create(context.Background(), Goal{
		Title: "环境变更", DomainID: domID, AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", Category: "hitl",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g.ID
}

// envChangeValidInput is a succeeded mutation with every optional field set —
// per-case tests override the one field under test.
func envChangeValidInput(goalID string) EnvChangeInput {
	return EnvChangeInput{
		GoalID:         goalID,
		RunID:          "run-1",
		NodeKey:        "n1",
		EnvID:          "opencode",
		BeforeVersion:  "1.0.0",
		AfterVersion:   "1.1.0",
		SnapshotRef:    "snap-20261121",
		ManifestSHA256: strings.Repeat("a", 64),
		Status:         "succeeded",
		Operator:       "agent-1",
		StartedAt:      "2026-11-21T10:00:00Z",
		FinishedAt:     "2026-11-21T10:05:00Z",
	}
}

func TestEnvChangeRecordRoundTripsAndListOrdering(t *testing.T) {
	gs, _, _, st := bpCluster(t)
	goalID := envChangeSeedGoal(t, gs, st)
	ctx := context.Background()

	first := envChangeValidInput(goalID)
	first.StartedAt = "2026-11-21T09:00:00Z"
	first.FinishedAt = "2026-11-21T09:01:00Z"
	row1, err := gs.RecordEnvChange(ctx, first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	if row1.ID == "" || row1.CreatedAt == "" {
		t.Fatalf("record must mint id/created_at: %+v", row1)
	}
	if row1.GoalID != goalID || row1.RunID != "run-1" || row1.NodeKey != "n1" ||
		row1.EnvID != "opencode" || row1.BeforeVersion != "1.0.0" ||
		row1.AfterVersion != "1.1.0" || row1.SnapshotRef != "snap-20261121" ||
		row1.ManifestSHA256 != first.ManifestSHA256 || row1.Status != "succeeded" ||
		row1.Operator != "agent-1" {
		t.Fatalf("record returned wrong row: %+v", row1)
	}

	second := envChangeValidInput(goalID)
	second.EnvID = "claude"
	second.StartedAt = "2026-11-21T11:00:00Z"
	second.FinishedAt = "2026-11-21T11:02:00Z"
	row2, err := gs.RecordEnvChange(ctx, second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}

	// A started row (no finish yet) sorts between the two terminal rows by
	// started_at — order comes from the timeline, not insertion order.
	started := EnvChangeInput{
		GoalID: goalID, EnvID: "node", Status: "started",
		StartedAt: "2026-11-21T10:00:00Z",
	}
	row3, err := gs.RecordEnvChange(ctx, started)
	if err != nil {
		t.Fatalf("record started: %v", err)
	}

	list, err := gs.ListEnvChanges(ctx, goalID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 rows, got %d: %+v", len(list), list)
	}
	gotOrder := []string{list[0].ID, list[1].ID, list[2].ID}
	wantOrder := []string{row2.ID, row3.ID, row1.ID}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("order must be started_at DESC: got %v want %v", gotOrder, wantOrder)
		}
	}

	// The row survives a fresh read with every column intact.
	got := list[2]
	if got.ID != row1.ID || got.GoalID != goalID || got.RunID != "run-1" ||
		got.NodeKey != "n1" || got.EnvID != "opencode" ||
		got.BeforeVersion != "1.0.0" || got.AfterVersion != "1.1.0" ||
		got.SnapshotRef != "snap-20261121" || got.ManifestSHA256 != first.ManifestSHA256 ||
		got.Status != "succeeded" || got.Operator != "agent-1" ||
		got.StartedAt != "2026-11-21T09:00:00Z" || got.FinishedAt != "2026-11-21T09:01:00Z" ||
		got.Error != "" || got.CreatedAt == "" {
		t.Fatalf("row did not round-trip: %+v", got)
	}
}

func TestEnvChangeValidation(t *testing.T) {
	gs, _, _, st := bpCluster(t)
	goalID := envChangeSeedGoal(t, gs, st)
	ctx := context.Background()

	cases := []struct {
		name    string
		mutate  func(*EnvChangeInput)
		wantSub string
	}{
		{"bad status", func(in *EnvChangeInput) { in.Status = "bogus" }, "status"},
		{"succeeded missing after_version", func(in *EnvChangeInput) {
			in.AfterVersion = ""
		}, "after_version"},
		{"succeeded missing before_version", func(in *EnvChangeInput) {
			in.BeforeVersion = ""
		}, "before_version"},
		{"failed missing error", func(in *EnvChangeInput) {
			in.Status = "failed"
			in.Error = ""
		}, "error"},
		{"bad sha", func(in *EnvChangeInput) {
			in.ManifestSHA256 = strings.Repeat("A", 64)
		}, "manifest_sha256"},
		{"short sha", func(in *EnvChangeInput) {
			in.ManifestSHA256 = "abc123"
		}, "manifest_sha256"},
		{"finished before started", func(in *EnvChangeInput) {
			in.StartedAt = "2026-11-21T10:00:00Z"
			in.FinishedAt = "2026-11-21T09:59:59Z"
		}, "finished_at"},
		{"started with finished_at", func(in *EnvChangeInput) {
			in.Status = "started"
			in.FinishedAt = "2026-11-21T10:05:00Z"
		}, "finished_at"},
		{"empty env_id", func(in *EnvChangeInput) { in.EnvID = "   " }, "env_id"},
		{"unknown goal", func(in *EnvChangeInput) { in.GoalID = "no-such-goal" }, "not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := envChangeValidInput(goalID)
			tc.mutate(&in)
			row, err := gs.RecordEnvChange(ctx, in)
			if err == nil {
				t.Fatalf("want validation error, got row %+v", row)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q must mention %q", err.Error(), tc.wantSub)
			}
		})
	}

	list, err := gs.ListEnvChanges(ctx, goalID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("rejected records must not be persisted, got %+v", list)
	}
}

// A pinned goal's node_key must name a compiled blueprint node; an unpinned
// goal keeps it free-form (handled by the round-trip test). The bridge only
// means something when a plan ledger exists.
func TestEnvChangeNodeKeyMustBeACompiledNode(t *testing.T) {
	gs, _, bs, st := bpCluster(t)
	ctx := context.Background()
	owner := seedAgent(t, st, "env-node-owner")
	worker := seedAgent(t, st, "env-node-worker")
	dom := seedDomain(t, st)
	content := strings.ReplaceAll(bpJSON("环境变更", "do the work"), "agent:WORKER", "agent:"+worker)
	ver := seedPublishedContent(t, bs, "env-node", content)
	g, err := gs.Create(ctx, Goal{
		Title: "环境变更节点", DomainID: dom, AssigneeType: "agent", AssigneeID: owner,
		Status: "active", Category: "hitl", BlueprintVersionID: ver.ID,
	})
	if err != nil {
		t.Fatalf("create pinned goal: %v", err)
	}
	var realKey string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT node_key FROM blueprint_node WHERE goal_id=? ORDER BY node_key LIMIT 1`, g.ID).Scan(&realKey); err != nil {
		t.Fatalf("read compiled node: %v", err)
	}

	in := envChangeValidInput(g.ID)
	in.NodeKey = "ghost-node"
	if _, err := gs.RecordEnvChange(ctx, in); err == nil || !strings.Contains(err.Error(), "not a node") {
		t.Fatalf("ghost node_key must be rejected, got %v", err)
	}
	in.NodeKey = realKey
	if _, err := gs.RecordEnvChange(ctx, in); err != nil {
		t.Fatalf("compiled node_key must be accepted: %v", err)
	}
}
