package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/service"
	"github.com/Sinkear/ouroflow/internal/store"
)

// Phase A HTTP smoke: blueprint CRUD + spec-graph endpoints over the wire —
// the acceptance criterion is that the API is exposed over HTTP, not just
// as in-process service calls.
func TestBlueprintHTTPRoutes(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	bus := events.NewBus()
	bs := service.NewBlueprintService(st, bus)

	// Seed an agent so V4 (assignee resolution) passes. The spec's assignee
	// ref is the agent ID (service-level tests do the same), not the name.
	seedAgent := func(t *testing.T, name string) string {
		t.Helper()
		if err := service.NewMachineService(st).Register(ctx, service.Machine{ID: "m1", Name: "m1", Hostname: "host"}, "[]"); err != nil {
			t.Fatalf("seed machine: %v", err)
		}
		rt, err := service.NewRuntimeService(st).Create(ctx, service.Runtime{Name: "rt-" + name, MachineID: "m1"})
		if err != nil {
			t.Fatalf("seed runtime: %v", err)
		}
		a, err := service.NewAgentService(st, events.NewBus()).Create(ctx, service.Agent{Name: name, RuntimeID: rt.ID, MaxConcurrent: 2})
		if err != nil {
			t.Fatalf("seed agent: %v", err)
		}
		return a.ID
	}
	agentID := seedAgent(t, "worker")

	h := &Handlers{Blueprint: bs}
	mux := http.NewServeMux()
	h.Mount(mux)

	post := func(t *testing.T, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	get := func(t *testing.T, path string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	content := `{
	  "name": "bp",
	  "description": "http smoke",
	  "nodes": [
	    {"id": "n1", "type": "task", "title": "first", "task": {"assignee": "agent:` + agentID + `", "brief": "do first"}},
	    {"id": "n9", "type": "end", "title": "done", "end": {"outcome": "success"}}
	  ],
	  "edges": [
	    {"source": "n1", "source_handle": "done", "target": "n9"}
	  ]
	}`

	// Create over HTTP.
	rec := post(t, "/blueprints", `{"name": "bp", "description": "d", "content": `+jsonRaw(t, content)+`}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("create blueprint: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatalf("create returned no id: %s", rec.Body.String())
	}

	// List over HTTP.
	rec = get(t, "/blueprints")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), created.ID) {
		t.Fatalf("list does not contain created blueprint: %s", rec.Body.String())
	}

	// Publish the draft, then fetch the spec graph (both route aliases).
	rec = post(t, "/blueprints/"+created.ID+"/versions", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("new draft version: %d %s", rec.Code, rec.Body.String())
	}
	var v2 struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &v2)

	rec = post(t, "/blueprint-versions/"+v2.ID+"/publish", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
	}

	// After publishing, list MUST expose latest_version_id — the web UI keys
	// off it for the published badge AND the new-goal "bind blueprint"
	// dropdown (a missing field would make every blueprint unbindable from
	// the UI).
	rec = get(t, "/blueprints")
	if rec.Code != http.StatusOK {
		t.Fatalf("list after publish: %d %s", rec.Code, rec.Body.String())
	}
	var listed []service.Blueprint
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	if len(listed) != 1 || listed[0].LatestVersionID != v2.ID {
		t.Fatalf("list must carry latest_version_id=%s, got %+v", v2.ID, listed)
	}

	rec = get(t, "/workflows/blueprints/"+v2.ID)
	if rec.Code != http.StatusOK {
		t.Fatalf("spec graph: %d %s", rec.Code, rec.Body.String())
	}
	var graph service.WorkflowGraph
	if err := json.Unmarshal(rec.Body.Bytes(), &graph); err != nil {
		t.Fatalf("spec graph decode: %v (%s)", err, rec.Body.String())
	}
	if len(graph.Nodes) != 2 || len(graph.Edges) != 1 {
		t.Fatalf("spec graph shape: %d nodes / %d edges", len(graph.Nodes), len(graph.Edges))
	}
	if graph.Nodes[0].Kind != "task" || graph.Edges[0].Kind != "spec" {
		t.Fatalf("spec graph kinds: %+v %+v", graph.Nodes[0], graph.Edges[0])
	}
}

func jsonRaw(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
