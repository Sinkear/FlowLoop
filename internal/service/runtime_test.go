package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Sinkear/flowloop/internal/link"
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

// TestRuntimeSetModels covers the authoritative write behind the models
// refresh endpoint: the machine's fresh detection replaces the row's list,
// an empty detection CLEARS stale ids (unlike probe reports), and an unknown
// runtime is ErrNotFound.
func TestRuntimeSetModels(t *testing.T) {
	st := newTestStore(t)
	svc := NewRuntimeService(st)
	ctx := context.Background()
	rt, err := svc.Create(ctx, Runtime{Name: "hermes@m1", MachineID: "m1"})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// Seed a stale, inflated list (the 52-model symptom).
	stale, _ := json.Marshal([]link.ModelInfo{{ID: "a"}, {ID: "b"}, {ID: "c"}})
	if _, err := st.DB().ExecContext(ctx, `UPDATE runtime SET models=?, models_at='2026-01-01T00:00:00Z' WHERE id=?`, string(stale), rt.ID); err != nil {
		t.Fatalf("seed stale models: %v", err)
	}
	fresh, err := svc.SetModels(ctx, rt.ID, []link.ModelInfo{{ID: "only-usable", Name: "Usable"}}, "2026-11-23T10:00:00Z")
	if err != nil {
		t.Fatalf("set models: %v", err)
	}
	if len(fresh.Models) != 1 || fresh.Models[0].ID != "only-usable" || fresh.DetectedAt != "2026-11-23T10:00:00Z" {
		t.Fatalf("fresh models mismatch: %+v", fresh)
	}
	// An empty re-detection is authoritative: stale ids must be cleared.
	empty, err := svc.SetModels(ctx, rt.ID, nil, "2026-11-23T10:01:00Z")
	if err != nil {
		t.Fatalf("set empty models: %v", err)
	}
	if empty.Models == nil || len(empty.Models) != 0 {
		t.Fatalf("empty refresh must clear models to [], got %+v", empty.Models)
	}
	if empty.DetectedAt != "2026-11-23T10:01:00Z" {
		t.Fatalf("empty refresh must still stamp detected_at, got %q", empty.DetectedAt)
	}
	if _, err := svc.SetModels(ctx, "nope", nil, ""); err != ErrNotFound {
		t.Fatalf("unknown runtime must be ErrNotFound, got %v", err)
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

// TestRuntimeSetProbe: the connectivity outcome of the last models refresh
// is persisted and exposed by Models/List — a failed probe keeps the model
// catalog but flags the endpoint, an ok probe clears the previous error.
func TestRuntimeSetProbe(t *testing.T) {
	st := newTestStore(t)
	svc := NewRuntimeService(st)
	ctx := context.Background()
	rt, err := svc.Create(ctx, Runtime{Name: "opencode@m1", MachineID: "m1"})
	if err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	if _, err := svc.SetModels(ctx, rt.ID, []link.ModelInfo{{ID: "glm-5.3-flash"}}, "2026-11-26T09:00:00Z"); err != nil {
		t.Fatalf("set models: %v", err)
	}
	failed, err := svc.SetProbe(ctx, rt.ID, false, "Internal error: ECONNREFUSED: Unable to connect.", "2026-11-26T09:00:01Z")
	if err != nil {
		t.Fatalf("set probe: %v", err)
	}
	if failed.ProbeStatus != "failed" || failed.ProbeError == "" || failed.ProbeAt != "2026-11-26T09:00:01Z" {
		t.Fatalf("failed probe not recorded: %+v", failed)
	}
	// The catalog survives a failed probe: the CLI still advertises models.
	if len(failed.Models) != 1 {
		t.Fatalf("failed probe must keep the catalog, got %+v", failed.Models)
	}
	listed, err := svc.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ProbeStatus != "failed" {
		t.Fatalf("List must scan probe columns, got %+v (err %v)", listed, err)
	}
	ok, err := svc.SetProbe(ctx, rt.ID, true, "ignored", "2026-11-26T09:05:00Z")
	if err != nil {
		t.Fatalf("set ok probe: %v", err)
	}
	if ok.ProbeStatus != "ok" || ok.ProbeError != "" {
		t.Fatalf("ok probe must clear the error, got %+v", ok)
	}
	if _, err := svc.SetProbe(ctx, "nope", true, "", ""); err != ErrNotFound {
		t.Fatalf("unknown runtime must be ErrNotFound, got %v", err)
	}
}
