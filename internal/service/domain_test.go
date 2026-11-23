package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
)

// TestDomainCRUD covers the domain lifecycle: create with defaults, read
// back, list, and validation gates.
func TestDomainCRUD(t *testing.T) {
	st := newTestStore(t)
	svc := NewDomainService(st, events.NewBus())
	ctx := context.Background()

	d, err := svc.Create(ctx, Domain{
		Name:   "ouroflow",
		GitURL: "https://github.com/Sinkear/ouroflow.git",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if d.Type != "repo" {
		t.Fatalf("expected default type repo, got %q", d.Type)
	}
	if d.MaxRunDuration != 7200 {
		t.Fatalf("expected default max_run_duration 7200, got %d", d.MaxRunDuration)
	}

	got, err := svc.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "ouroflow" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 || all[0].ID != d.ID {
		t.Fatalf("list mismatch: %+v", all)
	}

	// Validation gates.
	if _, err := svc.Create(ctx, Domain{Name: "x"}); err == nil {
		t.Fatalf("expected validation error for missing git_url")
	}
	if _, err := svc.Get(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestUpdateDomainTypeGuard covers the edit-path type guard: type is fixed
// at creation, and Update must validate against the DB's real type, not the
// (often-empty) value in the request body. The frontend edit form omits type
// entirely — without the guard, a scratch domain could silently absorb issue
// tracking config (type ↔ capability contradiction) because the scratch
// branch in validateIssueTracking never fires on d.Type == "".
func TestUpdateDomainTypeGuard(t *testing.T) {
	st := newTestStore(t)
	svc := NewDomainService(st, events.NewBus())
	ctx := context.Background()

	// A scratch domain — no repo, so issue tracking must be rejected on edit.
	scratch, err := svc.Create(ctx, Domain{Name: "notebook", Type: "scratch"})
	if err != nil {
		t.Fatalf("create scratch: %v", err)
	}
	if scratch.Type != "scratch" {
		t.Fatalf("expected scratch, got %q", scratch.Type)
	}
	// validateIssueTracking now sees the real type (scratch) and must refuse
	// issue config even though the request body carries no type field.
	if _, err := svc.Update(ctx, scratch.ID, Domain{
		IssueRepo:     "ouroflow/notebook",
		IssueAssignee: "someone",
	}); err == nil {
		t.Fatalf("expected scratch domain to reject issue tracking on edit")
	}

	// A repo domain — editing without type (the normal frontend path) keeps
	// working and persists the git config.
	repo, err := svc.Create(ctx, Domain{
		Name:   "ouroflow",
		Type:   "repo",
		GitURL: "https://github.com/Sinkear/ouroflow.git",
	})
	if err != nil {
		t.Fatalf("create repo: %v", err)
	}
	updated, err := svc.Update(ctx, repo.ID, Domain{
		GitURL:         "https://github.com/Sinkear/ouroflow.git",
		GitCredentials: "token-xyz",
	})
	if err != nil {
		t.Fatalf("update repo without type: %v", err)
	}
	if updated.Type != "repo" || updated.GitCredentials != "token-xyz" {
		t.Fatalf("repo edit round-trip mismatch: %+v", updated)
	}

	// A request that tries to flip the type must be refused loudly — the edit
	// path must never silently swallow a type change into a contradiction.
	if _, err := svc.Update(ctx, repo.ID, Domain{
		Type:   "scratch",
		GitURL: "https://github.com/Sinkear/ouroflow.git",
	}); err == nil {
		t.Fatalf("expected error when editing tries to change type repo→scratch")
	}
	if _, err := svc.Update(ctx, scratch.ID, Domain{
		Type: "repo",
	}); err == nil {
		t.Fatalf("expected error when editing tries to change type scratch→repo")
	}
}

// TestDomainAcceptanceLevel covers the project-level acceptance mode
// (验收级别): create validation + persistence, rename/acceptance on Update
// (empty incoming = keep), the coded duplicate-name error, List/Get exposure,
// and the explicit SetAcceptanceLevel clear path (” = 待定/未设置).
func TestDomainAcceptanceLevel(t *testing.T) {
	st := newTestStore(t)
	svc := NewDomainService(st, events.NewBus())
	ctx := context.Background()

	// Create rejects an unknown level loudly.
	if _, err := svc.Create(ctx, Domain{
		Name: "bad", GitURL: "https://github.com/ouroflow/bad.git", AcceptanceLevel: "auto",
	}); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for acceptance_level=auto, got %v", err)
	}

	// Create persists hitl/hootl; unset stays "" and round-trips as such.
	for _, level := range []string{"hitl", "hootl", ""} {
		name := "d-" + level
		d, err := svc.Create(ctx, Domain{
			Name:            name,
			GitURL:          "https://github.com/ouroflow/" + name + ".git",
			AcceptanceLevel: level,
		})
		if err != nil {
			t.Fatalf("create %q: %v", level, err)
		}
		if d.AcceptanceLevel != level {
			t.Fatalf("create %q: got %q", level, d.AcceptanceLevel)
		}
		got, err := svc.Get(ctx, d.ID)
		if err != nil {
			t.Fatalf("get %q: %v", level, err)
		}
		if got.AcceptanceLevel != level {
			t.Fatalf("get %q: got %q", level, got.AcceptanceLevel)
		}
	}

	// List exposes acceptance_level for every row.
	all, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	seen := map[string]string{}
	for _, d := range all {
		seen[d.Name] = d.AcceptanceLevel
	}
	if seen["d-hitl"] != "hitl" || seen["d-hootl"] != "hootl" || seen["d-"] != "" {
		t.Fatalf("list acceptance levels mismatch: %+v", seen)
	}

	// Update: empty incoming name/acceptance mean "keep the old values".
	target, err := svc.Create(ctx, Domain{
		Name: "alpha", GitURL: "https://github.com/ouroflow/alpha.git", AcceptanceLevel: "hitl",
	})
	if err != nil {
		t.Fatalf("create alpha: %v", err)
	}
	if _, err := svc.Create(ctx, Domain{
		Name: "beta", GitURL: "https://github.com/ouroflow/beta.git",
	}); err != nil {
		t.Fatalf("create beta: %v", err)
	}
	updated, err := svc.Update(ctx, target.ID, Domain{
		GitURL: "https://github.com/ouroflow/alpha.git", // partial edit carries no name/acceptance
	})
	if err != nil {
		t.Fatalf("update keep: %v", err)
	}
	if updated.Name != "alpha" || updated.AcceptanceLevel != "hitl" {
		t.Fatalf("empty incoming fields must keep old values: %+v", updated)
	}

	// Rename + change the level in one edit.
	updated, err = svc.Update(ctx, target.ID, Domain{
		Name:            "alpha-2",
		GitURL:          "https://github.com/ouroflow/alpha.git",
		AcceptanceLevel: "hootl",
	})
	if err != nil {
		t.Fatalf("update rename: %v", err)
	}
	if updated.Name != "alpha-2" || updated.AcceptanceLevel != "hootl" {
		t.Fatalf("rename/level round-trip mismatch: %+v", updated)
	}

	// A non-empty name that collides with another domain is a coded dup error.
	_, err = svc.Update(ctx, target.ID, Domain{
		Name:   "beta",
		GitURL: "https://github.com/ouroflow/alpha.git",
	})
	var ce CodedError
	if err == nil || !errors.As(err, &ce) || ce.Code() != CodeDomainNameExists {
		t.Fatalf("expected coded dup-name error, got %v", err)
	}

	// Invalid level on update is rejected (non-empty incoming).
	if _, err := svc.Update(ctx, target.ID, Domain{
		Name:            "alpha-2",
		GitURL:          "https://github.com/ouroflow/alpha.git",
		AcceptanceLevel: "auto",
	}); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error on bad update level, got %v", err)
	}

	// SetAcceptanceLevel is the explicit path that can CLEAR back to 待定.
	cleared, err := svc.SetAcceptanceLevel(ctx, target.ID, "")
	if err != nil {
		t.Fatalf("clear acceptance level: %v", err)
	}
	if cleared.AcceptanceLevel != "" {
		t.Fatalf("expected cleared level, got %q", cleared.AcceptanceLevel)
	}
	set, err := svc.SetAcceptanceLevel(ctx, target.ID, "hitl")
	if err != nil || set.AcceptanceLevel != "hitl" {
		t.Fatalf("set hitl: %+v %v", set, err)
	}
	if _, err := svc.SetAcceptanceLevel(ctx, target.ID, "nope"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("expected validation error for bad level, got %v", err)
	}
	if _, err := svc.SetAcceptanceLevel(ctx, "missing", "hitl"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for missing domain, got %v", err)
	}
}
