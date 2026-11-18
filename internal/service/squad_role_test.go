package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// squadRoleTestEnv seeds a squad with leader + two agents and returns the
// service, squad id, member agent ids, and the store.
func squadRoleTestEnv(t *testing.T) (*SquadService, string, [2]string, *store.Store) {
	t.Helper()
	ctx := context.Background()
	st := newTestStore(t)
	leader := seedAgent(t, st, "role-leader")
	a1 := seedAgent(t, st, "worker-a")
	a2 := seedAgent(t, st, "worker-b")
	sqSvc := NewSquadService(st, events.NewBus())
	sq, err := sqSvc.Create(ctx, Squad{Name: "role-squad", LeaderID: leader})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	return sqSvc, sq.ID, [2]string{a1, a2}, st
}

// TestSquadRoleRegistryLifecycle: register → list → re-register updates the
// description (upsert, not an error) → reserved words and bad slugs refused
// → remove works → removing a missing word is ErrNotFound.
func TestSquadRoleRegistryLifecycle(t *testing.T) {
	sqSvc, squadID, _, _ := squadRoleTestEnv(t)
	ctx := context.Background()

	r, err := sqSvc.RegisterRole(ctx, squadID, "design-implementer", "设计岗位：负责 UI/视觉实现")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if r.Name != "design-implementer" {
		t.Fatalf("registered name = %q", r.Name)
	}
	// Re-registering an existing word updates the job spec (upsert).
	if _, err := sqSvc.RegisterRole(ctx, squadID, "design-implementer", "设计岗位 v2"); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	roles, err := sqSvc.ListRoles(ctx, squadID)
	if err != nil || len(roles) != 1 {
		t.Fatalf("list roles: %v len=%d", err, len(roles))
	}
	if roles[0].Description != "设计岗位 v2" {
		t.Fatalf("re-register did not update description: %q", roles[0].Description)
	}

	// Reserved words need no registration.
	if _, err := sqSvc.RegisterRole(ctx, squadID, "reviewer", "x"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("registering reserved 'reviewer' should be refused, got %v", err)
	}
	if _, err := sqSvc.RegisterRole(ctx, squadID, "member", "x"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("registering reserved 'member' should be refused, got %v", err)
	}
	// Bad slug refused (uppercase, spaces).
	if _, err := sqSvc.RegisterRole(ctx, squadID, "Bad Slug", "x"); err == nil || !errors.Is(err, ErrValidation) {
		t.Fatalf("bad slug should be refused, got %v", err)
	}

	// Cross-squad listing includes the word with its squad name.
	all, err := sqSvc.ListAllRoles(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list all roles: %v len=%d", err, len(all))
	}
	if all[0].SquadName != "role-squad" || all[0].Name != "design-implementer" {
		t.Fatalf("all-roles view wrong: %+v", all[0])
	}

	// Remove works; removing the same word again is ErrNotFound.
	if err := sqSvc.RemoveRole(ctx, squadID, "design-implementer"); err != nil {
		t.Fatalf("remove role: %v", err)
	}
	if err := sqSvc.RemoveRole(ctx, squadID, "design-implementer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove twice should be ErrNotFound, got %v", err)
	}
}

// TestSquadRoleVocabularyGate: zero registered rows = free text kept; once
// ANY word is registered, member roles must come from the registry or the
// reserved words (fail-closed); registered words are normalized on write.
func TestSquadRoleVocabularyGate(t *testing.T) {
	sqSvc, squadID, ids, st := squadRoleTestEnv(t)
	ctx := context.Background()

	// Zero rows: free text roles pass (legacy / template apply / import).
	if _, err := sqSvc.AddMember(ctx, squadID, "agent", ids[0], "whatever-word"); err != nil {
		t.Fatalf("free-text role should pass on unregistered squad: %v", err)
	}

	// Registering ONE word flips the squad into strict mode.
	if _, err := sqSvc.RegisterRole(ctx, squadID, "implementer", "通用执行岗"); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Unregistered word now refused.
	_, err := sqSvc.AddMember(ctx, squadID, "agent", ids[1], "whatever-word")
	if err == nil || !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "registered vocabulary") {
		t.Fatalf("unregistered role should be refused, got %v", err)
	}
	// Registered word passes — and is normalized (spaces/case folded).
	m, err := sqSvc.AddMember(ctx, squadID, "agent", ids[1], "  Implementer ")
	if err != nil {
		t.Fatalf("registered role should pass: %v", err)
	}
	if m.Role != "implementer" {
		t.Fatalf("role should be normalized to %q, got %q", "implementer", m.Role)
	}
	// Reserved words always pass the gate. ids[1] already carries
	// 'implementer' and the roster is UNIQUE per member — a third agent
	// exercises the reviewer case.
	third := seedAgent(t, st, "worker-c")
	if _, err := sqSvc.AddMember(ctx, squadID, "agent", third, "reviewer"); err != nil {
		t.Fatalf("reserved 'reviewer' should pass: %v", err)
	}
}

// TestSquadRoleRemoveRefusedWhileCarried: the registry and the roster must
// not drift apart — a word still carried by a member cannot be dropped.
func TestSquadRoleRemoveRefusedWhileCarried(t *testing.T) {
	sqSvc, squadID, ids, _ := squadRoleTestEnv(t)
	ctx := context.Background()
	if _, err := sqSvc.RegisterRole(ctx, squadID, "implementer", "通用执行岗"); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := sqSvc.AddMember(ctx, squadID, "agent", ids[0], "implementer"); err != nil {
		t.Fatalf("add member: %v", err)
	}
	err := sqSvc.RemoveRole(ctx, squadID, "implementer")
	if err == nil || !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "still carried") {
		t.Fatalf("remove while carried should be refused, got %v", err)
	}
	if err := sqSvc.RemoveMember(ctx, squadID, ids[0]); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	if err := sqSvc.RemoveRole(ctx, squadID, "implementer"); err != nil {
		t.Fatalf("remove after member gone: %v", err)
	}
}
