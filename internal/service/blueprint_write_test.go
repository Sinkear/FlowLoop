package service

// BUG-4 attribution tests: a delivered commit is attributed to a spec node
// only when its message carries a "Blueprint-Node: <node_key>" trailer naming
// a node in the goal's compiled ledger. Everything else is an unattributed
// owner write recorded at deliver time and surfaced by PlanAlignment.

import (
	"context"
	"strings"
	"testing"
)

// bwSHA builds a full 40-char hex object id from one repeated hex digit — the
// parser only checks shape, never the object's existence.
func bwSHA(c byte) string { return strings.Repeat(string(c), 40) }

func TestParseBlueprintNodeTrailer(t *testing.T) {
	cases := []struct {
		name  string
		msg   string
		key   string
		found bool
	}{
		{"present", "feat: add thing\n\nbody line\nBlueprint-Node: n1\n", "n1", true},
		{"label case-insensitive", "FEAT\nblueprint-node: task_two", "task_two", true},
		{"trailing crlf", "feat: crlf\r\n\r\nBlueprint-Node: n2\r\n", "n2", true},
		{"absent", "feat: no trailer\n\njust a body", "", false},
		{"last occurrence wins", "Blueprint-Node: n1\nBlueprint-Node: n9\n", "n9", true},
		{"bad key: leading digit", "Blueprint-Node: 1bad", "", false},
		{"bad key: uppercase", "Blueprint-Node: N1", "", false},
		{"bad key: too long", "Blueprint-Node: " + strings.Repeat("a", 65), "", false},
		{"bad key: inner space", "Blueprint-Node: n 1", "", false},
		{"empty key", "Blueprint-Node:", "", false},
		{"mid-line mention is not a trailer", "note: Blueprint-Node: n1 in prose", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, ok := ParseBlueprintNodeTrailer(tc.msg)
			if ok != tc.found || key != tc.key {
				t.Fatalf("ParseBlueprintNodeTrailer(%q) = (%q,%v), want (%q,%v)", tc.msg, key, ok, tc.key, tc.found)
			}
		})
	}
}

func TestParseBlueprintWrites(t *testing.T) {
	known := map[string]bool{"n1": true}
	fixLog := strings.Join([]string{
		bwSHA('a') + " step one\n\nbody\nBlueprint-Node: n1\n",
		bwSHA('b') + " stray label\n\nBlueprint-Node: ghost\n",
		bwSHA('c') + " plain work\nno trailer here\n",
		"junk not-a-sha subject\nbody",
	}, "\x00")

	writes := ParseBlueprintWrites("goal-bw", fixLog, known)
	if len(writes) != 3 {
		t.Fatalf("junk entry must be skipped; got %d writes: %+v", len(writes), writes)
	}
	if writes[0].CommitSHA != bwSHA('a') || writes[0].Subject != "step one" || !writes[0].Attributed || writes[0].NodeKey != "n1" {
		t.Fatalf("attributed entry wrong: %+v", writes[0])
	}
	if writes[1].CommitSHA != bwSHA('b') || writes[1].Subject != "stray label" || writes[1].Attributed || writes[1].NodeKey != "" {
		t.Fatalf("unknown trailer must be a stray label, not an attribution: %+v", writes[1])
	}
	if writes[2].Subject != "plain work" || writes[2].Attributed || writes[2].NodeKey != "" {
		t.Fatalf("plain commit must be unattributed: %+v", writes[2])
	}
	unattributed := 0
	for _, w := range writes {
		if !w.Attributed {
			unattributed++
		}
	}
	if unattributed != 2 {
		t.Fatalf("want 2 unattributed writes, got %d", unattributed)
	}
	// Empty/garbage logs produce no rows.
	if got := ParseBlueprintWrites("goal-bw", "", nil); len(got) != 0 {
		t.Fatalf("empty log must yield no writes, got %+v", got)
	}
}

func TestRecordBlueprintWritesSkipsUnpinnedGoal(t *testing.T) {
	ctx := context.Background()
	gs, _, _, st := bpCluster(t)
	agent := seedAgent(t, st, "bw-owner")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{
		Title: "bw unpinned", DomainID: domID, AssigneeType: "agent", AssigneeID: agent,
		Status: "backlog", Category: "hitl",
	})
	if err != nil {
		t.Fatal(err)
	}
	writes := []BlueprintWrite{
		{NodeKey: "", CommitSHA: bwSHA('a'), Subject: "loose work", Attributed: false},
	}
	if err := gs.RecordBlueprintWrites(ctx, g.ID, writes); err != nil {
		t.Fatalf("unpinned goal must be a silent no-op, got %v", err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM blueprint_write WHERE goal_id=?`, g.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("unpinned goal must record 0 rows, got %d", n)
	}
}

func TestPlanAlignmentFlagsUnattributedWrites(t *testing.T) {
	ctx := context.Background()
	gs, _, bs, st := bpCluster(t)
	worker := seedAgent(t, st, "bw-worker")
	owner := seedAgent(t, st, "bw-plan-owner")
	domID := seedDomain(t, st)
	v := seedPublishedContent(t, bs, "bw-unattr-bp",
		strings.ReplaceAll(bpJSON("bw-unattr", "second"), "agent:WORKER", "agent:"+worker))
	g, err := gs.Create(ctx, Goal{
		Title: "bw unattr", DomainID: domID, AssigneeType: "agent", AssigneeID: owner,
		Status: "backlog", Category: "hitl", BlueprintVersionID: v.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gs.Activate(ctx, g.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	writes := []BlueprintWrite{
		{NodeKey: "n1", CommitSHA: bwSHA('a'), Subject: "step one", Attributed: true},
		{NodeKey: "", CommitSHA: bwSHA('b'), Subject: "loose work", Attributed: false},
	}
	if err := gs.RecordBlueprintWrites(ctx, g.ID, writes); err != nil {
		t.Fatal(err)
	}
	al, err := gs.PlanAlignment(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if al.Writes != 2 || al.AttributedWrites != 1 || al.UnattributedWrites != 1 {
		t.Fatalf("write counts wrong: writes=%d attributed=%d unattributed=%d",
			al.Writes, al.AttributedWrites, al.UnattributedWrites)
	}
	if len(al.UnattributedCommits) != 1 {
		t.Fatalf("want 1 unattributed commit entry, got %+v", al.UnattributedCommits)
	}
	wantCommit := bwSHA('b') + " loose work"
	if al.UnattributedCommits[0] != wantCommit {
		t.Fatalf("unattributed commit = %q, want %q", al.UnattributedCommits[0], wantCommit)
	}
	if joined := strings.Join(al.Deviations, " | "); !strings.Contains(joined, "计划外写入") {
		t.Fatalf("unattributed writes must be a deviation, got %v", al.Deviations)
	}
}
