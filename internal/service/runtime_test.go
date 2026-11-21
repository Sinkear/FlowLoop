package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/eushing/agentwork/internal/link"
)

// TestRuntimeModelsEndpoint covers RuntimeService.Models: identity fields
// (cli = the name segment before '@'), the persisted model list and the
// detection timestamp, plus ErrNotFound for an unknown runtime.
func TestRuntimeModelsEndpoint(t *testing.T) {
	st := newTestStore(t)
	svc := NewRuntimeService(st)
	ctx := context.Background()

	rt, err := svc.Create(ctx, Runtime{Name: "claude@dev", MachineID: "m1"})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// Fresh runtime: no detection yet → empty models, empty detected_at.
	models, err := svc.Models(ctx, rt.ID)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if models.RuntimeID != rt.ID || models.Name != "claude@dev" || models.CLI != "claude" || models.MachineID != "m1" {
		t.Fatalf("identity mismatch: %+v", models)
	}
	if models.Models == nil || len(models.Models) != 0 || models.DetectedAt != "" {
		t.Fatalf("undetected runtime must report [] and '', got %+v", models)
	}

	// Simulate a probe that persisted a model list.
	listJSON, _ := json.Marshal([]link.ModelInfo{
		{ID: "claude-sonnet-4", Name: "Sonnet 4", Description: "balanced"},
		{ID: "claude-opus-4"},
	})
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE runtime SET models=?, models_at=? WHERE id=?`,
		string(listJSON), "2026-11-21T10:00:00Z", rt.ID); err != nil {
		t.Fatalf("seed models: %v", err)
	}
	models, err = svc.Models(ctx, rt.ID)
	if err != nil {
		t.Fatalf("models after seed: %v", err)
	}
	if len(models.Models) != 2 || models.Models[0].ID != "claude-sonnet-4" ||
		models.Models[0].Description != "balanced" || models.DetectedAt != "2026-11-21T10:00:00Z" {
		t.Fatalf("detected models mismatch: %+v", models)
	}

	// List/Get also expose the models + models_at JSON fields.
	all, err := svc.List(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list: %d runtimes (err %v)", len(all), err)
	}
	if len(all[0].Models) != 2 || all[0].ModelsAt != "2026-11-21T10:00:00Z" {
		t.Fatalf("List must scan models/models_at, got %+v", all[0])
	}
	got, err := svc.Get(ctx, rt.ID)
	if err != nil || len(got.Models) != 2 {
		t.Fatalf("Get must scan models, got %+v (err %v)", got, err)
	}

	// Unknown runtime → ErrNotFound (the handler maps it to 404).
	if _, err := svc.Models(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// TestRuntimeModelsBadJSON: a corrupt models column degrades to [] instead
// of failing the whole list/endpoint.
func TestRuntimeModelsBadJSON(t *testing.T) {
	st := newTestStore(t)
	svc := NewRuntimeService(st)
	ctx := context.Background()
	rt, err := svc.Create(ctx, Runtime{Name: "opencode@dev", MachineID: "m1"})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE runtime SET models='not-json' WHERE id=?`, rt.ID); err != nil {
		t.Fatalf("corrupt models: %v", err)
	}
	models, err := svc.Models(ctx, rt.ID)
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	if models.Models == nil || len(models.Models) != 0 {
		t.Fatalf("corrupt models must degrade to [], got %+v", models.Models)
	}
}
