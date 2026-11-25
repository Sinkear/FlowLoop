package main

import (
	"testing"

	"github.com/Sinkear/flowloop/internal/acp"
	"github.com/Sinkear/flowloop/internal/link"
	"github.com/Sinkear/flowloop/internal/proto"
)

// execSessionSpec is the single place a dispatched run's model reaches the
// ACP layer: the spec carries p.Model into OpenSession, where acpbackend
// applyModel applies it (config option first, session/set_model fallback).
func TestExecSessionSpecCarriesModel(t *testing.T) {
	workdir := t.TempDir()
	spec := execSessionSpec(link.RunDispatchParams{
		Model:      "opencode/opus",
		McpServers: []acp.McpServer{{Type: "http", Name: "browser", URL: "http://127.0.0.1:8080/sse"}},
	}, proto.Conn{}, workdir)
	if spec.Model != "opencode/opus" {
		t.Fatalf("spec.Model = %q, want opencode/opus", spec.Model)
	}
	if spec.Cwd != workdir {
		t.Fatalf("spec.Cwd = %q, want %q", spec.Cwd, workdir)
	}
	if len(spec.McpServers) != 1 || spec.McpServers[0].Name != "browser" {
		t.Fatalf("extra MCP servers dropped: %+v", spec.McpServers)
	}
	if spec.ClientHandler == nil {
		t.Fatal("autopermit handler must be wired")
	}
}

// An empty agent.model must reach the session as "": applyModel then leaves
// the CLI's default model untouched.
func TestExecSessionSpecEmptyModel(t *testing.T) {
	spec := execSessionSpec(link.RunDispatchParams{RunID: "r1"}, proto.Conn{}, t.TempDir())
	if spec.Model != "" {
		t.Fatalf("spec.Model = %q, want empty (CLI default)", spec.Model)
	}
}
