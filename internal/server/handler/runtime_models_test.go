package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sinkear/ouroflow/internal/daemon"
	"github.com/Sinkear/ouroflow/internal/service"
	"github.com/Sinkear/ouroflow/internal/store"
)

// modelsResponse mirrors the GET /runtimes/{id}/models contract exactly:
// all six keys are always present (models [] instead of null, detected_at ”
// when never detected).
type modelsResponse struct {
	RuntimeID  string         `json:"runtime_id"`
	Name       string         `json:"name"`
	CLI        string         `json:"cli"`
	MachineID  string         `json:"machine_id"`
	Models     []serviceModel `json:"models"`
	DetectedAt string         `json:"detected_at"`
}

type serviceModel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

// TestRefreshRuntimeModelsHandler covers the failure paths of
// POST /runtimes/{id}/models/refresh without a live machine: a handler with
// no daemon (or a daemon with no peers) answers 409, not 500/502, and the
// runtime's persisted list stays untouched. Unknown runtimes 404 like GET.
func TestRefreshRuntimeModelsHandler(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	rtSvc := service.NewRuntimeService(st)
	rt, err := rtSvc.Create(ctx, service.Runtime{Name: "hermes@dev", MachineID: "m1"})
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE runtime SET models='[{"id":"stale"}]', models_at='2026-01-01T00:00:00Z' WHERE id=?`, rt.ID); err != nil {
		t.Fatalf("seed models: %v", err)
	}
	refresh := func(h *Handlers, id string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		h.Mount(mux)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/runtimes/"+id+"/models/refresh", nil))
		return rec
	}

	// Daemon nil = no machine link component at all → 409.
	if rec := refresh(&Handlers{Runtime: rtSvc}, rt.ID); rec.Code != http.StatusConflict {
		t.Fatalf("daemon-less refresh: expected 409, got %d (body %s)", rec.Code, rec.Body.String())
	}
	// Daemon present but this machine has no live peer → 409 (offline), and
	// the stale list must NOT be cleared by a failed refresh.
	if rec := refresh(&Handlers{Runtime: rtSvc, Daemon: &daemon.Daemon{}}, rt.ID); rec.Code != http.StatusConflict {
		t.Fatalf("offline refresh: expected 409, got %d (body %s)", rec.Code, rec.Body.String())
	}
	models, err := rtSvc.Models(ctx, rt.ID)
	if err != nil {
		t.Fatalf("models after failed refresh: %v", err)
	}
	if len(models.Models) != 1 || models.Models[0].ID != "stale" || models.DetectedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("failed refresh must leave persisted models untouched, got %+v", models)
	}
	// Unknown runtime → 404 with the standard error body.
	rec := refresh(&Handlers{Runtime: rtSvc, Daemon: &daemon.Daemon{}}, "nope")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown runtime refresh: expected 404, got %d (body %s)", rec.Code, rec.Body.String())
	}
}
func TestGetRuntimeModelsHandler(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()

	rtSvc := service.NewRuntimeService(st)
	rt, err := rtSvc.Create(ctx, service.Runtime{Name: "claude@dev", MachineID: "m1"})
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE runtime SET models=?, models_at=? WHERE id=?`,
		`[{"id":"claude-sonnet-4","name":"Sonnet 4","description":"balanced"},{"id":"claude-opus-4"}]`,
		"2026-11-21T10:00:00Z", rt.ID); err != nil {
		t.Fatalf("seed models: %v", err)
	}

	h := &Handlers{Runtime: rtSvc}
	mux := http.NewServeMux()
	h.Mount(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/"+rt.ID+"/models", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET models: status %d, body %s", rec.Code, rec.Body.String())
	}
	var got modelsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v (body %s)", err, rec.Body.String())
	}
	if got.RuntimeID != rt.ID || got.Name != "claude@dev" || got.CLI != "claude" ||
		got.MachineID != "m1" || got.DetectedAt != "2026-11-21T10:00:00Z" {
		t.Fatalf("identity mismatch: %+v", got)
	}
	if len(got.Models) != 2 || got.Models[0].ID != "claude-sonnet-4" ||
		got.Models[0].Name != "Sonnet 4" || got.Models[0].Description != "balanced" ||
		got.Models[1].ID != "claude-opus-4" {
		t.Fatalf("models mismatch: %+v", got.Models)
	}

	// Every key must be present in the raw JSON (models not omitted when
	// empty; detected_at explicitly '' rather than absent).
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw: %v", err)
	}
	for _, key := range []string{"runtime_id", "name", "cli", "machine_id", "models", "detected_at"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("response must always carry %q, body %s", key, rec.Body.String())
		}
	}

	// Unknown runtime → 404 with the standard error body/code.
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/runtimes/nope/models", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown runtime: expected 404, got %d (body %s)", rec.Code, rec.Body.String())
	}
	var errBody struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if errBody.Error == "" || errBody.Code != service.CodeNotFound {
		t.Fatalf("expected not_found error body, got %+v", errBody)
	}
}
