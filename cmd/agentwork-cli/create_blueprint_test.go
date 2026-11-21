package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIntentForKindBlueprint: `agentwork create blueprint` rides the same
// dispatch path as the other create kinds.
func TestIntentForKindBlueprint(t *testing.T) {
	intent, ok := intentForKind("blueprint")
	if !ok || intent != "create_blueprint" {
		t.Fatalf("intentForKind(blueprint) = (%q, %v), want (create_blueprint, true)", intent, ok)
	}
}

// TestBuildCreateActionBlueprint: flags map onto the intake blueprint payload;
// content-file is read into content; agent_id rides along for __AGENT_ID__.
func TestBuildCreateActionBlueprint(t *testing.T) {
	const graph = `{"name":"g","nodes":[],"edges":[]}`

	action := buildCreateAction("blueprint", []string{
		"--name", "调研-落地", "--description", "两步", "--content", graph, "--agent", "a1",
	})
	if action["intent"] != "create_blueprint" {
		t.Fatalf("intent = %v", action["intent"])
	}
	bp, ok := action["blueprint"].(map[string]any)
	if !ok {
		t.Fatalf("blueprint payload missing: %#v", action)
	}
	if bp["name"] != "调研-落地" || bp["description"] != "两步" || bp["content"] != graph || bp["agent_id"] != "a1" {
		t.Fatalf("blueprint payload = %#v", bp)
	}

	// content-file: the CLI reads the file and sends its text as content, so a
	// long SpecGraph never has to fit on the command line.
	dir := t.TempDir()
	path := filepath.Join(dir, "bp.json")
	if err := os.WriteFile(path, []byte(graph), 0o644); err != nil {
		t.Fatal(err)
	}
	action = buildCreateAction("blueprint", []string{"--name", "文件图", "--content-file", path})
	bp = action["blueprint"].(map[string]any)
	if bp["content"] != graph {
		t.Fatalf("content-file must be read into content, got %q", bp["content"])
	}

	// Neither --content nor --content-file is a legitimate request: the
	// platform answers need_fields instead of the CLI inventing a graph.
	action = buildCreateAction("blueprint", []string{"--name", "只有名字"})
	bp = action["blueprint"].(map[string]any)
	if bp["content"] != "" {
		t.Fatalf("absent content must stay empty, got %q", bp["content"])
	}
}
