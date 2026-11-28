package main

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestParseFlagsReordered: the documented usage puts flags AFTER the
// positional skill name (`skill pr <name> --title t`); Go's flag package
// stops at the first non-flag argument, so the reorder helper must recover
// flags from the tail without losing the positional.
func TestParseFlagsReordered(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantPos   []string
		wantTitle string
		wantBody  string
	}{
		{
			name:      "flags after name (documented usage)",
			args:      []string{"demo", "--title", "T", "--body", "B"},
			wantPos:   []string{"demo"},
			wantTitle: "T",
			wantBody:  "B",
		},
		{
			name:      "flags before name",
			args:      []string{"--title", "T", "--body", "B", "demo"},
			wantPos:   []string{"demo"},
			wantTitle: "T",
			wantBody:  "B",
		},
		{
			name:    "name only",
			args:    []string{"demo"},
			wantPos: []string{"demo"},
		},
		{
			name:    "no args",
			args:    nil,
			wantPos: nil,
		},
		{
			name:    "two positionals keep order",
			args:    []string{"a", "b"},
			wantPos: []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("skill pr", flag.ContinueOnError)
			title := fs.String("title", "", "pull request title")
			body := fs.String("body", "", "pull request body")
			pos := parseFlagsReordered(fs, tc.args)
			if len(pos) != len(tc.wantPos) {
				t.Fatalf("positionals = %v, want %v", pos, tc.wantPos)
			}
			for i := range tc.wantPos {
				if pos[i] != tc.wantPos[i] {
					t.Fatalf("positionals = %v, want %v", pos, tc.wantPos)
				}
			}
			if *title != tc.wantTitle {
				t.Errorf("title = %q, want %q", *title, tc.wantTitle)
			}
			if *body != tc.wantBody {
				t.Errorf("body = %q, want %q", *body, tc.wantBody)
			}
		})
	}
}

// TestGetJSONMapSingleRequest: GET /skills returns a BARE ARRAY; the map
// wrapper must decode it as {"skills": [...]} and — critically — fetch the
// body ONCE. The old implementation decoded-then-refetched, doubling every
// skill command's latency.
func TestGetJSONMapSingleRequest(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"s1","name":"demo"}]`))
	}))
	defer srv.Close()

	m, err := getJSONMap(srv.URL + "/skills")
	if err != nil {
		t.Fatalf("getJSONMap: %v", err)
	}
	if requests != 1 {
		t.Fatalf("array response: got %d requests, want 1", requests)
	}
	list, ok := m["skills"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("skills wrapper = %#v, want one element", m)
	}
	first, ok := list[0].(map[string]any)
	if !ok || first["name"] != "demo" {
		t.Fatalf("skills[0] = %#v, want name demo", list[0])
	}
}

// TestGetJSONMapObjectPassthrough: object responses come back as-is, also
// in a single request.
func TestGetJSONMapObjectPassthrough(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"skill_id":"s1","state":"in_sync"}`))
	}))
	defer srv.Close()

	m, err := getJSONMap(srv.URL + "/check")
	if err != nil {
		t.Fatalf("getJSONMap: %v", err)
	}
	if requests != 1 {
		t.Fatalf("object response: got %d requests, want 1", requests)
	}
	if m["state"] != "in_sync" {
		t.Fatalf("state = %v, want in_sync", m["state"])
	}
}
