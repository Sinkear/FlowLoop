package main

import (
	"fmt"
	"sync"
	"testing"

	"github.com/Sinkear/flowloop/internal/link"
	"github.com/Sinkear/flowloop/internal/paths"
)

// TestStashPendingReportSurvivesConcurrentWriters pins the stash's
// read-modify-write serialization. Two writers share the file in production:
// every run's exec goroutine (a run finishing while the link is down) and the
// link goroutine flushing the stash after a register. Without pendingReportsMu
// the loser's append was overwritten by a stale write-back — the terminal
// report vanished, the run stayed 'running' on the platform, and nothing was
// logged (the exact incident symptom: "no terminal report, no error").
func TestStashPendingReportSurvivesConcurrentWriters(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())

	const writers = 16
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			stashPendingReport(link.RunFinishedParams{
				RunID:   fmt.Sprintf("run-%02d", i),
				Status:  "completed",
				Summary: "concurrent finish while the link was down",
			})
		}(i)
	}
	wg.Wait()

	got, err := loadPendingReports()
	if err != nil {
		t.Fatalf("stash unreadable after %d concurrent writers: %v", writers, err)
	}
	seen := make(map[string]bool, len(got))
	for _, r := range got {
		seen[r.RunID] = true
	}
	for i := 0; i < writers; i++ {
		id := fmt.Sprintf("run-%02d", i)
		if !seen[id] {
			t.Errorf("report for %s was lost by a concurrent stash (stash now holds %d report(s), want %d)", id, len(got), writers)
		}
	}
}

// TestSavePendingReportsKeepsDurability checks the write-then-rename contract
// directly: a saved stash must coexist with a pre-existing file (unique temp
// path, no shared ".tmp" clobber), and clearing it must be observable.
func TestSavePendingReportsKeepsDurability(t *testing.T) {
	t.Setenv(paths.EnvHome, t.TempDir())

	first := []link.RunFinishedParams{{RunID: "run-a", Status: "failed"}, {RunID: "run-b", Status: "completed"}}
	if err := savePendingReports(first); err != nil {
		t.Fatalf("save initial stash: %v", err)
	}
	got, err := loadPendingReports()
	if err != nil {
		t.Fatalf("load initial stash: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("initial stash holds %d report(s), want 2", len(got))
	}
	if err := savePendingReports(got[1:]); err != nil {
		t.Fatalf("rewrite stash: %v", err)
	}
	got, err = loadPendingReports()
	if err != nil {
		t.Fatalf("load rewritten stash: %v", err)
	}
	if len(got) != 1 || got[0].RunID != "run-b" {
		t.Fatalf("rewritten stash = %+v, want only run-b", got)
	}
	if err := savePendingReports(nil); err != nil {
		t.Fatalf("clear stash: %v", err)
	}
	if _, err := loadPendingReports(); err == nil {
		t.Fatalf("stash file must be gone after clearing it")
	}
}
