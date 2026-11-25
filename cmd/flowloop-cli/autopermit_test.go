package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Sinkear/flowloop/internal/acp"
)

// TestConsultAutopermitDeniesWrites pins the consult tool-layer policy: a
// consult run is read-only, so delegated file writes and terminals fail
// instead of silently producing work.
func TestConsultAutopermitDeniesWrites(t *testing.T) {
	ctx := context.Background()
	a := newAutopermit(t.TempDir(), true)
	path := filepath.Join(t.TempDir(), "nope.txt")
	if _, err := a.HandleWriteTextFile(ctx, acp.WriteTextFileRequest{Path: path, Content: "x"}); err == nil {
		t.Fatalf("consult run must not write files")
	}
	if _, err := a.HandleCreateTerminal(ctx, acp.CreateTerminalRequest{Command: "sh", Args: []string{"-c", "true"}}); err == nil {
		t.Fatalf("consult run must not spawn terminals")
	}
}

// TestConsultAutopermitDeniesWritePermissions: edit/execute permission
// requests are answered with a reject option when the CLI offers one.
func TestConsultAutopermitDeniesWritePermissions(t *testing.T) {
	ctx := context.Background()
	a := newAutopermit(t.TempDir(), true)
	req := acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{Title: "apply patch", Kind: "edit"},
		Options: []acp.PermissionOption{
			{OptionID: "once", Kind: acp.PermissionAllowOnce},
			{OptionID: "reject", Kind: acp.PermissionRejectOnce},
		},
	}
	resp, err := a.HandleRequestPermission(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome.Outcome != acp.PermissionOutcomeSelected || resp.Outcome.OptionID == nil || *resp.Outcome.OptionID != "reject" {
		t.Fatalf("must select the reject option, got %+v", resp.Outcome)
	}
	// No reject option offered → cancel.
	req.Options = []acp.PermissionOption{{OptionID: "once", Kind: acp.PermissionAllowOnce}}
	resp, err = a.HandleRequestPermission(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome.Outcome != acp.PermissionOutcomeCancelled {
		t.Fatalf("must cancel when only allow is offered, got %+v", resp.Outcome)
	}
	// Read-only tool kinds are still allowed.
	req.ToolCall.Kind = "read"
	req.Options = []acp.PermissionOption{{OptionID: "once", Kind: acp.PermissionAllowOnce}}
	resp, err = a.HandleRequestPermission(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome.Outcome != acp.PermissionOutcomeSelected || *resp.Outcome.OptionID != "once" {
		t.Fatalf("read permission must be approved, got %+v", resp.Outcome)
	}
}

// TestWorkerAutopermitStillWrites: the policy must not weaken normal runs.
func TestWorkerAutopermitStillWrites(t *testing.T) {
	ctx := context.Background()
	a := newAutopermit(t.TempDir(), false)
	path := filepath.Join(t.TempDir(), "ok.txt")
	if _, err := a.HandleWriteTextFile(ctx, acp.WriteTextFileRequest{Path: path, Content: "x"}); err != nil {
		t.Fatalf("worker write must succeed: %v", err)
	}
	resp, err := a.HandleRequestPermission(ctx, acp.RequestPermissionRequest{
		ToolCall: acp.ToolCallUpdate{Title: "apply patch", Kind: "edit"},
		Options:  []acp.PermissionOption{{OptionID: "once", Kind: acp.PermissionAllowOnce}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Outcome.Outcome != acp.PermissionOutcomeSelected || *resp.Outcome.OptionID != "once" {
		t.Fatalf("worker edit permission must be approved, got %+v", resp.Outcome)
	}
}
