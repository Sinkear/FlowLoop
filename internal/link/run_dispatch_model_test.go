package link

import (
	"encoding/json"
	"testing"
)

// Verifies agent.model survives the daemon → machine dispatch wire: the
// daemon marshals RunDispatchParams.Model → JSON → the machine unmarshals it
// → exec.go applies it at ACP session/new. A regression that drops the
// field would silently run every goal on the CLI's default model.
func TestRunDispatchParamsModelRoundTrip(t *testing.T) {
	in := RunDispatchParams{RunID: "r1", AgentID: "a1", Model: "claude-opus-4"}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(raw); got == "" || !json.Valid(raw) {
		t.Fatalf("invalid dispatch JSON: %q", got)
	}
	var out RunDispatchParams
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Model != in.Model {
		t.Fatalf("Model round-trip = %q, want %q", out.Model, in.Model)
	}
}

// An unset agent.model stays unset on the wire (omitempty): the executor must
// see an empty string and leave the CLI's default model alone.
func TestRunDispatchParamsEmptyModelOmitted(t *testing.T) {
	raw, err := json.Marshal(RunDispatchParams{RunID: "r1", AgentID: "a1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var asMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if _, ok := asMap["model"]; ok {
		t.Fatalf("empty model must be omitted from the wire, got %s", raw)
	}
}
