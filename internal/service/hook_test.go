package service

// Hook service tests — acceptance items from tmp/hooks-spec.md §7:
// validation 400s, skip semantics, execution + stdin contract, seq order,
// domain inheritance + goal override, timeout, tripwire hash provenance,
// terminal-goal 409, orphan sweep, tail truncation.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// newHookFixture opens a store and creates one domain + one active goal to
// bind against (goal:finished needs a real goal row for title/status lookups).
func newHookFixture(t *testing.T) (*HookService, *store.Store, string, string) {
	t.Helper()
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO domain (id,name,created_at) VALUES (?,?,?)`, "d-1", "demo", now()); err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,domain_id,status,assignee_type,created_at,created_by_type) VALUES (?,?,?,?,?,?,?)`,
		"g-1", "Goal One", "d-1", "active", "agent", now(), "human"); err != nil {
		t.Fatalf("seed goal: %v", err)
	}
	return NewHookService(st, nil), st, "d-1", "g-1"
}

func TestHookCreateValidation(t *testing.T) {
	svc, _, _, _ := newHookFixture(t)
	ctx := context.Background()

	cases := []struct {
		name        string
		hookName    string
		content     string
		interpreter string
		timeout     int
		wantErr     string
	}{
		{"no shebang no interpreter", "bad-1", "echo hi", "", 0, "shebang"},
		{"shebang ok", "ok-1", "#!/bin/sh\necho hi", "", 0, ""},
		{"interpreter overrides shebang", "ok-2", "echo hi", "bash", 0, ""},
		{"bad name", "Bad_Name", "#!/bin/sh\necho", "", 0, "name"},
		{"bad interpreter", "ok-3", "#!/bin/sh\necho", "ruby", 0, "interpreter"},
		{"timeout too big", "ok-4", "#!/bin/sh\necho", "", 601, "timeout"},
		{"timeout zero means default", "ok-5", "#!/bin/sh\necho", "", 0, ""},
	}
	for _, tc := range cases {
		h, err := svc.Create(ctx, tc.hookName, "", tc.content, tc.interpreter, tc.timeout, nil)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: unexpected error: %v", tc.name, err)
			}
			if h.TimeoutSec == 0 {
				h2, _ := svc.Get(ctx, h.ID)
				if h2.TimeoutSec != 60 {
					t.Errorf("%s: default timeout not applied: %d", tc.name, h2.TimeoutSec)
				}
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
		}
		if !errors.Is(err, ErrValidation) {
			t.Errorf("%s: error must be ErrValidation, got %T", tc.name, err)
		}
	}

	// Duplicate name → coded validation error.
	if _, err := svc.Create(ctx, "ok-1", "", "#!/bin/sh\necho", "", 0, nil); err == nil || !errors.Is(err, ErrValidation) {
		t.Errorf("duplicate name: want ErrValidation, got %v", err)
	}
	// Oversize content.
	if _, err := svc.Create(ctx, "big", "", "#!/bin/sh\n"+strings.Repeat("x", HookMaxContent+1), "", 0, nil); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("oversize content: want exceeds error, got %v", err)
	}
}

func TestHookGoalTerminalConflict(t *testing.T) {
	svc, st, domainID, goalID := newHookFixture(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, "notify-hook", "", "#!/bin/sh\necho ok", "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Active goal binds fine.
	if _, err := svc.BindGoal(ctx, goalID, h.ID, "goal_finished", 0, nil); err != nil {
		t.Fatalf("bind on active goal: %v", err)
	}
	// Terminal goal is read-only → ErrConflict (409).
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='done' WHERE id=?`, goalID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BindGoal(ctx, goalID, h.ID, "goal_reviewing", 0, nil); !errors.Is(err, ErrConflict) {
		t.Errorf("bind on terminal goal: want ErrConflict, got %v", err)
	}
	// Unknown event / unknown hook / unknown goal.
	if _, err := svc.BindGoal(ctx, goalID, h.ID, "nope", 0, nil); !errors.Is(err, ErrValidation) {
		t.Errorf("unknown event: want ErrValidation, got %v", err)
	}
	if _, err := svc.BindGoal(ctx, goalID, "missing", "goal_finished", 0, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown hook: want ErrNotFound, got %v", err)
	}
	if _, err := svc.BindGoal(ctx, "missing", h.ID, "goal_finished", 0, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown goal: want ErrNotFound, got %v", err)
	}
	// Domain binding + duplicate binding rejection.
	if _, err := svc.BindDomain(ctx, domainID, h.ID, "goal_finished", 0, nil); err != nil {
		t.Fatalf("domain bind: %v", err)
	}
	if _, err := svc.BindDomain(ctx, domainID, h.ID, "goal_finished", 1, nil); !errors.Is(err, ErrValidation) {
		t.Errorf("duplicate domain bind: want ErrValidation, got %v", err)
	}
}

// effectiveBindingsFor ordering: domain first (seq asc), then goal (seq asc),
// goal overriding domain for the same hook.
func TestHookEffectiveBindingsOrderAndOverride(t *testing.T) {
	svc, _, domainID, goalID := newHookFixture(t)
	ctx := context.Background()
	mk := func(name string) string {
		h, err := svc.Create(ctx, name, "", "#!/bin/sh\ntrue", "", 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		return h.ID
	}
	a, b, c := mk("hook-a"), mk("hook-b"), mk("hook-c")
	// Domain defaults: a(seq 5), c(seq 1). Goal-level: b(seq 2), a(seq 0 —
	// overrides the domain binding of a).
	for _, bd := range []struct {
		hook, scope, ev string
		seq             int
	}{
		{a, "domain", "goal_finished", 5},
		{c, "domain", "goal_finished", 1},
		{b, "goal", "goal_finished", 2},
		{a, "goal", "goal_finished", 0},
	} {
		var err error
		if bd.scope == "domain" {
			_, err = svc.BindDomain(ctx, domainID, bd.hook, bd.ev, bd.seq, nil)
		} else {
			_, err = svc.BindGoal(ctx, goalID, bd.hook, bd.ev, bd.seq, nil)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	got, err := svc.effectiveBindingsFor(ctx, domainID, goalID, "goal_finished")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range got {
		names = append(names, b.HookID)
	}
	// Domain a is overridden by goal a; remaining: c(domain,seq1), b(goal,seq2), a(goal,seq0→ after domain? goal seq asc)
	// Expected: domain-level first (c seq 1), then goal-level seq asc (a seq 0, b seq 2).
	want := []string{c, a, b}
	if len(names) != len(want) {
		t.Fatalf("effective bindings: got %v want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("effective bindings order: got %v want %v", names, want)
			break
		}
	}
	// GoalEffectiveHooks returns the same set across all events.
	all, err := svc.GoalEffectiveHooks(ctx, goalID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Errorf("GoalEffectiveHooks: got %d, want 3", len(all))
	}
}

// TestHookExecutorFiresOnFinished covers the acceptance core: bus event →
// script runs with the stdin JSON contract → hook_run completed with
// content_hash → exit≠0 recorded as failed without touching the goal.
func TestHookExecutorFiresOnFinished(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)

	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO domain (id,name,created_at) VALUES ('d-1','demo',?)`, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,domain_id,status,assignee_type,created_at,created_by_type) VALUES ('g-1','Hook Goal','d-1','active','agent',?,'human')`, now()); err != nil {
		t.Fatal(err)
	}

	bus := events.NewBus()
	svc := NewHookService(st, bus)
	svc.Subscribe()
	defer svc.Unsubscribe()

	// The script asserts the stdin contract and writes a marker into the
	// run dir so the test can verify env + cwd.
	script := `#!/bin/sh
cat > "$AGENTWORK_HOOK_RUN_ID.stdin.json"
echo "cwd=$(pwd)" > out.txt
`
	h, err := svc.Create(ctx, "contract-hook", "", script, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BindGoal(ctx, "g-1", h.ID, "goal_finished", 0, nil); err != nil {
		t.Fatal(err)
	}

	bus.Publish(context.Background(), events.Event{Topic: "goal:finished",
		Payload: map[string]any{"goal_id": "g-1", "status": "done", "summary": "all good"}})

	waitFor(t, 5*time.Second, func() bool {
		runs, err := svc.Runs(ctx, h.ID, 10)
		return err == nil && len(runs) == 1 && runs[0].Status == "completed"
	})
	runs, _ := svc.Runs(ctx, h.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(runs))
	}
	r := runs[0]
	if r.ContentHash != h.ContentHash {
		t.Errorf("content_hash provenance: run %s != hook %s", r.ContentHash, h.ContentHash)
	}
	if r.ExitCode == nil || *r.ExitCode != 0 {
		t.Errorf("exit code: got %v", r.ExitCode)
	}
	// stdin contract check (run dir lives under AGENTWORK_HOME, i.e. dir).
	raw, err := os.ReadFile(filepath.Join(dir, "runs", "hooks", r.ID, r.ID+".stdin.json"))
	if err != nil {
		t.Fatalf("read stdin capture: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, raw)
	}
	for _, k := range []string{"platform", "hook_schema", "hook_event_name", "goal_id", "title", "domain_id", "status", "summary", "timestamp", "created_by_type"} {
		if _, ok := p[k]; !ok {
			t.Errorf("payload missing %q: %s", k, raw)
		}
	}
	if p["hook_event_name"] != "goal_finished" || p["goal_id"] != "g-1" || p["status"] != "done" {
		t.Errorf("payload fields wrong: %s", raw)
	}
	// cwd is the run dir (out.txt marker written there).
	if _, err := os.Stat(filepath.Join(dir, "runs", "hooks", r.ID, "out.txt")); err != nil {
		t.Errorf("script did not run with run dir as cwd: %v", err)
	}
	// The staged script file is removed after execution.
	if _, err := os.Stat(filepath.Join(dir, "runs", "hooks", r.ID, "script")); !os.IsNotExist(err) {
		t.Errorf("staged script should be removed after run")
	}
}

// TestHookExecutorDisabledAndFailing: enabled=0 → skipped (recorded); exit≠0
// → failed, goal status untouched.
func TestHookExecutorDisabledAndFailing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,status,assignee_type,created_at,created_by_type) VALUES ('g-2','G','active','agent',?,'human')`, now()); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc := NewHookService(st, bus)
	svc.Subscribe()
	defer svc.Unsubscribe()

	disabled, _ := svc.Create(ctx, "disabled-hook", "", "#!/bin/sh\ntrue", "", 0, nil)
	if _, err := svc.UpdateMeta(ctx, disabled.ID, nil, nil, nil, nil, boolPtr(false), nil); err != nil {
		t.Fatal(err)
	}
	failing, _ := svc.Create(ctx, "failing-hook", "", "#!/bin/sh\necho boom >&2; exit 3", "", 0, nil)
	for _, id := range []string{disabled.ID, failing.ID} {
		if _, err := svc.BindGoal(ctx, "g-2", id, "goal_finished", 0, nil); err != nil {
			t.Fatal(err)
		}
	}
	bus.Publish(context.Background(), events.Event{Topic: "goal:finished",
		Payload: map[string]any{"goal_id": "g-2", "status": "done", "summary": ""}})

	waitFor(t, 5*time.Second, func() bool {
		d, _ := svc.Runs(ctx, disabled.ID, 10)
		f, _ := svc.Runs(ctx, failing.ID, 10)
		return len(d) == 1 && d[0].Status == "skipped" && len(f) == 1 && f[0].Status == "failed"
	})
	d, _ := svc.Runs(ctx, disabled.ID, 10)
	f, _ := svc.Runs(ctx, failing.ID, 10)
	if len(d) != 1 || d[0].Status != "skipped" || !strings.Contains(d[0].Output, "hook disabled") {
		t.Errorf("disabled hook: %+v", d)
	}
	if len(f) != 1 || f[0].Status != "failed" || f[0].ExitCode == nil || *f[0].ExitCode != 3 {
		t.Errorf("failing hook: %+v", f)
	}
	if !strings.Contains(f[0].Output, "boom") {
		t.Errorf("failing hook output should carry stderr: %q", f[0].Output)
	}
	var status string
	_ = st.DB().QueryRowContext(ctx, `SELECT status FROM goal WHERE id='g-2'`).Scan(&status)
	if status != "active" {
		t.Errorf("hook failure must not touch goal status: %s", status)
	}
}

// TestHookTimeout: timeout_sec enforced, status=timeout.
func TestHookTimeout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	svc := NewHookService(st, nil)
	h, err := svc.Create(ctx, "sleepy-hook", "", "#!/bin/sh\nsleep 30", "", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	run, err := svc.TestHook(ctx, h.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "timeout" {
		t.Errorf("status: got %s want timeout (output=%q)", run.Status, run.Output)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("timeout took too long: %s", elapsed)
	}
}

// TestHookListTripwireBadge: the panel inputs — content_changed_since_last_run
// appears after an edit, clears after the next run.
func TestHookListTripwireBadge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	svc, st, _, _ := newHookFixture(t)
	ctx := context.Background()
	h, _ := svc.Create(ctx, "tripwire-hook", "", "#!/bin/sh\necho v1", "", 0, nil)
	if _, err := svc.TestHook(ctx, h.ID, ""); err != nil {
		t.Fatal(err)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if list[0]["content_changed_since_last_run"] != nil {
		t.Errorf("fresh run must not set the badge")
	}
	// Edit content — badge appears.
	if _, err := svc.UpdateContent(ctx, h.ID, "#!/bin/sh\necho v2"); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.List(ctx)
	if list[0]["content_changed_since_last_run"] != true {
		t.Errorf("after edit the badge must be set: %v", list[0])
	}
	// Run again — badge clears (run recorded the new hash).
	if _, err := svc.TestHook(ctx, h.ID, ""); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.List(ctx)
	if list[0]["content_changed_since_last_run"] != nil {
		t.Errorf("after re-run the badge must clear: %v", list[0])
	}
	_ = st
}

// TestHookOrphanSweep: running rows from a previous process are marked
// failed with the daemon-restart note.
func TestHookOrphanSweep(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	svc := NewHookService(st, nil)
	h, _ := svc.Create(ctx, "orphan-hook", "", "#!/bin/sh\ntrue", "", 0, nil)
	// Simulate a crashed execution: insert a running row directly.
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO hook_run (id,hook_id,binding_id,goal_id,event,content_hash,status,exit_code,started_at,finished_at,output)
		 VALUES ('r-orph', ?, '', '', 'goal_finished', ?, 'running', NULL, ?, '', '')`,
		h.ID, h.ContentHash, now()); err != nil {
		t.Fatal(err)
	}
	n, err := svc.SweepOrphanRuns(ctx)
	if err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	var status, output string
	_ = st.DB().QueryRowContext(ctx, `SELECT status, output FROM hook_run WHERE id='r-orph'`).Scan(&status, &output)
	if status != "failed" || !strings.Contains(output, "daemon restart") {
		t.Errorf("orphan run: status=%s output=%q", status, output)
	}
	// Second sweep is a no-op.
	if n, _ := svc.SweepOrphanRuns(ctx); n != 0 {
		t.Errorf("second sweep should be 0, got %d", n)
	}
}

// TestHookTailWriter: output tail truncation keeps the LAST bytes and marks
// the prefix.
func TestHookTailWriter(t *testing.T) {
	w := newTailWriter(100)
	// Write 4 chunks of 60 bytes: total 240, tail keeps last 100.
	for i := 0; i < 4; i++ {
		if _, err := w.Write([]byte(strings.Repeat(string(rune('a'+i)), 60))); err != nil {
			t.Fatal(err)
		}
	}
	s := w.String()
	if !strings.HasPrefix(s, "…[truncated]\n") {
		t.Errorf("missing truncation marker: %q", s[:30])
	}
	if len(s) > 100+len("…[truncated]\n") {
		t.Errorf("tail too long: %d", len(s))
	}
	if !strings.HasSuffix(s, strings.Repeat("d", 60)) {
		t.Errorf("tail must keep the LAST bytes, got suffix %q", s[len(s)-10:])
	}
}

// TestHookDeleteCascadesAndKeepsHistory: deleting a hook removes bindings
// (FK cascade) but hook_run history survives (no FK by design).
func TestHookDeleteCascadesAndKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	svc, st, domainID, goalID := newHookFixture(t)
	ctx := context.Background()
	h, _ := svc.Create(ctx, "doomed-hook", "", "#!/bin/sh\ntrue", "", 0, nil)
	if _, err := svc.BindDomain(ctx, domainID, h.ID, "goal_finished", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.BindGoal(ctx, goalID, h.ID, "goal_finished", 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TestHook(ctx, h.ID, ""); err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, h.ID); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hook_binding WHERE hook_id=?`, h.ID).Scan(&n)
	if n != 0 {
		t.Errorf("bindings must cascade: %d left", n)
	}
	// Run history survives deletion IN THE DB (the /hooks/{id}/runs API
	// 404s for a deleted hook by design — no registry row to attach to).
	_ = st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hook_run WHERE hook_id=?`, h.ID).Scan(&n)
	if n != 1 {
		t.Errorf("run history rows must survive deletion: %d", n)
	}
	if err := svc.Delete(ctx, h.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: want ErrNotFound, got %v", err)
	}
}

// TestHookEventBusTopics: registry maps exactly the five spec events onto the
// existing bus topics.
func TestHookEventBusTopics(t *testing.T) {
	want := map[string]string{
		"goal_finished":        "goal:finished",
		"goal_delivered":       "goal:delivered",
		"goal_deliver_failed":  "goal:deliver_failed",
		"goal_reviewing":       "goal:reviewing",
		"goal_review_resolved": "goal:review_resolved",
	}
	if len(hookEventTopics) != len(want) {
		t.Fatalf("registry drift: %v", hookEventTopics)
	}
	for ev, topic := range want {
		if hookEventTopics[ev] != topic {
			t.Errorf("%s → %s, want %s", ev, hookEventTopics[ev], topic)
		}
	}
}

// TestHookReviewingSkipOnTerminal: a reviewing binding created while the
// goal was active, followed by the goal completing — a stale goal:reviewing
// then records a "goal terminal" skip, not an execution.
func TestHookReviewingSkipOnTerminal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,status,assignee_type,created_at,created_by_type) VALUES ('g-3','G','active','agent',?,'human')`, now()); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc := NewHookService(st, bus)
	svc.Subscribe()
	defer svc.Unsubscribe()
	h, _ := svc.Create(ctx, "review-hook", "", "#!/bin/sh\ntrue", "", 0, nil)
	if _, err := svc.BindGoal(ctx, "g-3", h.ID, "goal_reviewing", 0, nil); err != nil {
		t.Fatalf("bind on active goal: %v", err)
	}
	// Goal completes; the reviewing binding stays but must not fire.
	if _, err := st.DB().ExecContext(ctx, `UPDATE goal SET status='done' WHERE id='g-3'`); err != nil {
		t.Fatal(err)
	}
	bus.Publish(context.Background(), events.Event{Topic: "goal:reviewing",
		Payload: map[string]any{"goal_id": "g-3", "reason": "gate", "auto_approve": false}})
	waitFor(t, 5*time.Second, func() bool {
		runs, _ := svc.Runs(ctx, h.ID, 10)
		return len(runs) == 1 && runs[0].Status == "skipped"
	})
	runs, _ := svc.Runs(ctx, h.ID, 10)
	if len(runs) != 1 || runs[0].Status != "skipped" || !strings.Contains(runs[0].Output, "goal terminal") {
		t.Errorf("reviewing on done goal: %+v", runs)
	}
}

// TestHookSeqOrderWithinEvent: same event, multiple goal-level bindings fire
// in seq order. Each binding runs in its own run dir, so the observable is
// the hook_run started_at order (bindings fire sequentially within one
// event — the executor loop blocks on each script).
func TestHookSeqOrderWithinEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,status,assignee_type,created_at,created_by_type) VALUES ('g-4','G','active','agent',?,'human')`, now()); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc := NewHookService(st, bus)
	svc.Subscribe()
	defer svc.Unsubscribe()

	mk := func(name string, seq int) {
		h, err := svc.Create(ctx, name, "", "#!/bin/sh\ntrue", "", 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.BindGoal(ctx, "g-4", h.ID, "goal_finished", seq, nil); err != nil {
			t.Fatal(err)
		}
	}
	mk("alpha", 2)
	mk("beta", 1)
	mk("gamma", 0)

	bus.Publish(context.Background(), events.Event{Topic: "goal:finished",
		Payload: map[string]any{"goal_id": "g-4", "status": "done", "summary": ""}})
	waitFor(t, 5*time.Second, func() bool {
		done := 0
		for _, name := range []string{"alpha", "beta", "gamma"} {
			hh, err := svc.Get(ctx, hookIDByName(t, svc, name))
			if err != nil {
				return false
			}
			rs, _ := svc.Runs(ctx, hh.ID, 10)
			if len(rs) == 1 && rs[0].Status == "completed" {
				done++
			}
		}
		return done == 3
	})

	// Order by parsed started_at — RFC3339Nano strings must not be compared
	// lexically (trailing-zero trimming in fractions).
	type entry struct {
		name string
		at   time.Time
	}
	var entries []entry
	for _, name := range []string{"alpha", "beta", "gamma"} {
		hh, err := svc.Get(ctx, hookIDByName(t, svc, name))
		if err != nil {
			t.Fatal(err)
		}
		rs, _ := svc.Runs(ctx, hh.ID, 1)
		if len(rs) != 1 {
			t.Fatalf("no run recorded for %s", name)
		}
		at, err := time.Parse(time.RFC3339Nano, rs[0].StartedAt)
		if err != nil {
			t.Fatalf("parse started_at %q: %v", rs[0].StartedAt, err)
		}
		entries = append(entries, entry{name, at})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].at.Before(entries[j].at) })
	var got []string
	for _, e := range entries {
		got = append(got, e.name)
	}
	if strings.Join(got, ",") != "gamma,beta,alpha" {
		t.Errorf("seq order: got %v, want gamma,beta,alpha", got)
	}
}

func hookIDByName(t *testing.T, svc *HookService, name string) string {
	t.Helper()
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range list {
		if h, ok := m["hook"].(*Hook); ok && h.Name == name {
			return h.ID
		}
	}
	t.Fatalf("hook %s not found", name)
	return ""
}

// ── helpers ──

// boolPtr already exists in this package (template_apply.go).

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	// One last check outside the loop to fail with the final state.
	if !cond() {
		t.Fatalf("condition not met within %s", timeout)
	}
}

var _ = sync.Mutex{} // keep sync import stable if helpers change

// TestHookParamsValidation: bad keys are rejected on both create and bind.
func TestHookParamsValidation(t *testing.T) {
	svc, _, domainID, goalID := newHookFixture(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, "p-bad", "", "#!/bin/sh\ntrue", "", 0, map[string]string{"9bad": "x"}); err == nil || !errors.Is(err, ErrValidation) {
		t.Errorf("bad key on create: want ErrValidation, got %v", err)
	}
	if _, err := svc.Create(ctx, "p-bad2", "", "#!/bin/sh\ntrue", "", 0, map[string]string{"with space": "x"}); err == nil || !errors.Is(err, ErrValidation) {
		t.Errorf("key with space: want ErrValidation, got %v", err)
	}
	h, err := svc.Create(ctx, "p-ok", "", "#!/bin/sh\ntrue", "", 0, map[string]string{"webhook_url": "https://x", "_x": "1", "A-b_9": "y"})
	if err != nil {
		t.Fatal(err)
	}
	if h.Params["webhook_url"] != "https://x" || len(h.Params) != 3 {
		t.Errorf("params roundtrip: %+v", h.Params)
	}
	if _, err := svc.BindGoal(ctx, goalID, h.ID, "goal_finished", 0, map[string]string{"bad!key": "x"}); err == nil || !errors.Is(err, ErrValidation) {
		t.Errorf("bad key on bind: want ErrValidation, got %v", err)
	}
	if _, err := svc.BindDomain(ctx, domainID, h.ID, "goal_finished", 0, map[string]string{"bad!key": "x"}); err == nil || !errors.Is(err, ErrValidation) {
		t.Errorf("bad key on domain bind: want ErrValidation, got %v", err)
	}
}

// TestHookParamsInjection: params reach the script via all three channels —
// stdin payload "params" key, AGENTWORK_HOOK_PARAMS (whole JSON), and
// AGENTWORK_PARAM_<KEY> flattened env. Binding params override hook
// defaults on the same key while other hook defaults survive (shallow merge).
func TestHookParamsInjection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO goal (id,title,status,assignee_type,created_at,created_by_type) VALUES ('g-5','P','active','agent',?,'human')`, now()); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	svc := NewHookService(st, bus)
	svc.Subscribe()
	defer svc.Unsubscribe()

	script := `#!/bin/sh
cat > "$AGENTWORK_HOOK_RUN_ID.stdin.json"
env | grep '^AGENTWORK_' | sort > env.txt
`
	h, err := svc.Create(ctx, "params-hook", "", script, "", 0, map[string]string{
		"channel": "default-chan", "webhook-url": "https://hook.example/default",
	})
	if err != nil {
		t.Fatal(err)
	}
	// Binding overrides "channel" but keeps "webhook-url"; adds a new key.
	if _, err := svc.BindGoal(ctx, "g-5", h.ID, "goal_finished", 0, map[string]string{
		"channel": "proj-alerts", "extra": "42",
	}); err != nil {
		t.Fatal(err)
	}
	bus.Publish(context.Background(), events.Event{Topic: "goal:finished",
		Payload: map[string]any{"goal_id": "g-5", "status": "done", "summary": ""}})

	waitFor(t, 5*time.Second, func() bool {
		runs, err := svc.Runs(ctx, h.ID, 10)
		return err == nil && len(runs) == 1 && runs[0].Status == "completed"
	})
	runs, _ := svc.Runs(ctx, h.ID, 10)
	if len(runs) != 1 || runs[0].Status != "completed" {
		t.Fatalf("run did not complete: %+v", runs)
	}
	runDir := filepath.Join(dir, "runs", "hooks", runs[0].ID)

	// Channel 1: stdin payload params (merged values).
	raw, err := os.ReadFile(filepath.Join(runDir, runs[0].ID+".stdin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("payload: %v", err)
	}
	params, ok := p["params"].(map[string]any)
	if !ok {
		t.Fatalf("payload has no params object: %s", raw)
	}
	if params["channel"] != "proj-alerts" || params["webhook-url"] != "https://hook.example/default" || params["extra"] != "42" {
		t.Errorf("merged params wrong: %v", params)
	}

	// Channels 2+3: env capture.
	envRaw, err := os.ReadFile(filepath.Join(runDir, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	env := string(envRaw)
	for _, want := range []string{
		"AGENTWORK_HOOK_PARAMS=",
		`AGENTWORK_PARAM_CHANNEL=proj-alerts`,
		"AGENTWORK_PARAM_WEBHOOK_URL=https://hook.example/default",
		"AGENTWORK_PARAM_EXTRA=42",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env missing %q in:\n%s", want, env)
		}
	}
	// AGENTWORK_HOOK_PARAMS carries the same merged map as JSON.
	line := ""
	for _, l := range strings.Split(env, "\n") {
		if strings.HasPrefix(l, "AGENTWORK_HOOK_PARAMS=") {
			line = strings.TrimPrefix(l, "AGENTWORK_HOOK_PARAMS=")
		}
	}
	var envParams map[string]string
	if err := json.Unmarshal([]byte(line), &envParams); err != nil {
		t.Fatalf("AGENTWORK_HOOK_PARAMS not JSON: %q", line)
	}
	if envParams["channel"] != "proj-alerts" || envParams["extra"] != "42" || envParams["webhook-url"] != "https://hook.example/default" {
		t.Errorf("HOOK_PARAMS env wrong: %v", envParams)
	}
}

// TestHookParamsUpdateMetaReplaceAll: params updates are replace-all —
// present pointer replaces the whole map (empty map clears), absent keeps.
func TestHookParamsUpdateMetaReplaceAll(t *testing.T) {
	svc, _, _, _ := newHookFixture(t)
	ctx := context.Background()
	h, err := svc.Create(ctx, "p-meta", "", "#!/bin/sh\ntrue", "", 0, map[string]string{"a": "1", "b": "2"})
	if err != nil {
		t.Fatal(err)
	}
	// Absent pointer → keep.
	if _, err := svc.UpdateMeta(ctx, h.ID, nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.Get(ctx, h.ID)
	if len(got.Params) != 2 || got.Params["a"] != "1" {
		t.Errorf("absent params should keep: %+v", got.Params)
	}
	// Present pointer → replace-all (b dropped, c added).
	if _, err := svc.UpdateMeta(ctx, h.ID, nil, nil, nil, nil, nil, &map[string]string{"a": "x", "c": "3"}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, h.ID)
	if len(got.Params) != 2 || got.Params["a"] != "x" || got.Params["c"] != "3" || got.Params["b"] != "" {
		t.Errorf("replace-all wrong: %+v", got.Params)
	}
	// Empty map → clear.
	if _, err := svc.UpdateMeta(ctx, h.ID, nil, nil, nil, nil, nil, &map[string]string{}); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.Get(ctx, h.ID)
	if len(got.Params) != 0 {
		t.Errorf("empty map should clear: %+v", got.Params)
	}
}

// TestHookTestRunSurvivesCanceledRequestContext pins the OCR-M2 fix: the
// executor must not inherit the HTTP request lifetime. An already-canceled
// client ctx (browser tab closed mid test run) must neither fail the run nor
// drop the finishRun write — that combination leaves a ghost `running` row
// until the next orphan sweep.
func TestHookTestRunSurvivesCanceledRequestContext(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTWORK_HOME", dir)
	st := newTestStore(t)
	svc := NewHookService(st, nil)
	bg := context.Background()
	h, err := svc.Create(bg, "detach-hook", "", "#!/bin/sh\necho detached-ok", "", 60, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	run, err := svc.TestHook(canceled, h.ID, "")
	if err != nil {
		t.Fatalf("canceled request ctx must not fail the test run: %v", err)
	}
	if run.Status != "completed" {
		t.Errorf("status: got %s want completed (output=%q)", run.Status, run.Output)
	}
	if !strings.Contains(run.Output, "detached-ok") {
		t.Errorf("output missing script stdout: %q", run.Output)
	}
}
