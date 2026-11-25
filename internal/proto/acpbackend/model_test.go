package acpbackend

import (
	"context"
	"errors"
	"testing"

	"github.com/Sinkear/flowloop/internal/acp"
	"github.com/Sinkear/flowloop/internal/proto"
)

// opencodeConfigOptions is the session/new payload opencode 2.2.5 returns:
// the model selector is a select config option, there is no result.models.
var opencodeConfigOptions = []acp.SessionConfigOption{{
	ID:           "model",
	Name:         "Model",
	Category:     "model",
	Type:         "select",
	CurrentValue: "opencode/sonnet",
	Options: []acp.SessionConfigOptValue{
		{Value: "opencode/sonnet", Name: "Sonnet"},
		{Value: "opencode/opus", Name: "Opus"},
	},
}}

// TestApplyModelViaConfigOption: a config-option agent (opencode/hermes) gets
// session/set_config_option{configId:"model",type:"select",value:model} —
// never session/set_model, which opencode rejects.
func TestApplyModelViaConfigOption(t *testing.T) {
	f := &fakeAgent{configOptions: opencodeConfigOptions}
	s := openTestSessionSpec(t, f, proto.SessionSpec{Model: "opencode/opus"})
	if s.SessionID() != "sess-1" {
		t.Fatalf("session id: %q", s.SessionID())
	}
	if len(f.configCalls) != 1 {
		t.Fatalf("expected one set_config_option, got %+v", f.configCalls)
	}
	got := f.configCalls[0]
	if got.SessionID != "sess-1" || got.ConfigID != "model" || got.Type != "select" || got.Value != "opencode/opus" {
		t.Fatalf("set_config_option request mismatch: %+v", got)
	}
	if len(f.modelCalls) != 0 {
		t.Fatalf("config-option agents must not get session/set_model, got %+v", f.modelCalls)
	}
}

// TestApplyModelViaSetModel: an agent that advertises no model config option
// (result.models only) gets session/set_model{sessionId,modelId}.
func TestApplyModelViaSetModel(t *testing.T) {
	f := &fakeAgent{}
	openTestSessionSpec(t, f, proto.SessionSpec{Model: "hermes-4"})
	if len(f.configCalls) != 0 {
		t.Fatalf("no config option advertised: set_config_option must not be called, got %+v", f.configCalls)
	}
	if len(f.modelCalls) != 1 {
		t.Fatalf("expected one set_model, got %+v", f.modelCalls)
	}
	got := f.modelCalls[0]
	if got.SessionID != "sess-1" || got.ModelID != "hermes-4" {
		t.Fatalf("set_model request mismatch: %+v", got)
	}
}

// TestApplyModelConfigOptionFailureFallsBack: the config option is advertised
// but the agent rejects set_config_option — fall back to session/set_model
// instead of giving up.
func TestApplyModelConfigOptionFailureFallsBack(t *testing.T) {
	f := &fakeAgent{
		configOptions: opencodeConfigOptions,
		configErr:     errors.New("method session/set_config_option not found"),
	}
	openTestSessionSpec(t, f, proto.SessionSpec{Model: "opencode/opus"})
	if len(f.configCalls) != 1 || len(f.modelCalls) != 1 {
		t.Fatalf("expected config attempt + set_model fallback, got config=%+v model=%+v", f.configCalls, f.modelCalls)
	}
	if f.modelCalls[0].ModelID != "opencode/opus" {
		t.Fatalf("fallback model mismatch: %+v", f.modelCalls[0])
	}
}

// TestApplyModelUnsupportedIsBestEffort: no selection surface works (opencode
// rejects set_model with -32601; an agent may advertise neither) — OpenSession
// must still succeed and the session must serve prompts on the default model.
func TestApplyModelUnsupportedIsBestEffort(t *testing.T) {
	f := &fakeAgent{modelErr: errors.New("method session/set_model not found")}
	s := openTestSessionSpec(t, f, proto.SessionSpec{Model: "unknown-model"})
	if len(f.modelCalls) != 1 {
		t.Fatalf("set_model must be attempted, got %+v", f.modelCalls)
	}
	run, err := s.Prompt(context.Background(), "still works")
	if err != nil {
		t.Fatalf("prompt after failed model selection: %v", err)
	}
	res := <-run.Result
	if res.Status != proto.StatusCompleted {
		t.Fatalf("a failed model selection must not fail the run, got %v (err %v)", res.Status, res.Err)
	}
}

// TestApplyModelEmptyIsNoop: Model "" keeps the agent default — no selection
// RPC at all.
func TestApplyModelEmptyIsNoop(t *testing.T) {
	f := &fakeAgent{configOptions: opencodeConfigOptions}
	openTestSessionSpec(t, f, proto.SessionSpec{})
	if len(f.configCalls) != 0 || len(f.modelCalls) != 0 {
		t.Fatalf("empty Model must not select anything, got config=%+v model=%+v", f.configCalls, f.modelCalls)
	}
}

// TestHasModelConfigOption: id or category "model" matches; unrelated
// options (mode, thought_level) do not.
func TestHasModelConfigOption(t *testing.T) {
	if !hasModelConfigOption(opencodeConfigOptions) {
		t.Fatal("opencode config option must match by id/category")
	}
	if !hasModelConfigOption([]acp.SessionConfigOption{{ID: "primary", Category: "model"}}) {
		t.Fatal("category-only model option must match")
	}
	if hasModelConfigOption([]acp.SessionConfigOption{{ID: "mode", Category: "mode", Type: "select"}}) {
		t.Fatal("a mode option is not a model selector")
	}
	if hasModelConfigOption(nil) {
		t.Fatal("nil options must not match")
	}
}
