package daemon

import (
	"encoding/json"
	"strings"

	"github.com/Sinkear/flowloop/internal/link"
	"github.com/Sinkear/flowloop/internal/logging"
)

// resolveModel maps the agent's configured model id (agent.model, often a
// BARE name like "glm-5.3-flash") onto the QUALIFIED id its CLI actually
// advertises at ACP session time ("OpenGW/glm-5.3-flash",
// "custom:opengw:glm-5.3", ...). The executor passes the value verbatim to
// session/set_config_option / session/set_model, and agent CLIs that key
// their model tables by provider-qualified ids reject bare names ("model
// not found") — the run then silently continues on the agent DEFAULT,
// which is only correct when the default happens to equal the configured
// model.
//
// Resolution against the runtime's probe-reported model list:
//   - "" → "" (no selection; the agent default applies)
//   - already qualified (contains "/" or ":") → passed through untouched;
//     the agent owner asked for a specific provider entry
//   - exact advertised id → passed through (nothing to do)
//   - bare name with a UNIQUE qualified sibling (id ends "/<name>" or
//     ":<name>") → that qualified id. This is the fix for opencode-style
//     catalogs where every model id carries the provider prefix.
//   - bare name with MULTIPLE qualified siblings → left bare + warn. The
//     candidates point at DIFFERENT providers (distinct gateways/keys);
//     guessing one would silently reroute the run to another endpoint. The
//     CLI's own default resolution keeps today's behavior.
//   - bare name with no sibling → left bare + warn (the probe may be stale;
//     the CLI may still resolve the name natively).
func resolveModel(agentModel string, models []link.ModelInfo) string {
	agentModel = strings.TrimSpace(agentModel)
	if agentModel == "" {
		return ""
	}
	if strings.ContainsAny(agentModel, "/:") {
		return agentModel
	}
	match := ""
	for _, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		if id == agentModel {
			return agentModel
		}
		if strings.HasSuffix(id, "/"+agentModel) || strings.HasSuffix(id, ":"+agentModel) {
			if match != "" && match != id {
				logging.Warnf("dispatch: agent model %q is ambiguous among the runtime's reported models (%s, %s, …) — keeping the bare id; the agent CLI's default resolution applies",
					agentModel, match, id)
				return agentModel
			}
			match = id
		}
	}
	if match != "" {
		return match
	}
	logging.Warnf("dispatch: agent model %q has no match in the runtime's reported models — keeping the bare id; the agent CLI's default resolution applies", agentModel)
	return agentModel
}

// parseRuntimeModels decodes a runtime row's persisted models JSON (probe
// time). Nil on any malformed/empty input — resolveModel treats nil as "no
// catalog" and leaves the configured id untouched.
func parseRuntimeModels(rtModelsJSON string) []link.ModelInfo {
	var models []link.ModelInfo
	if err := json.Unmarshal([]byte(rtModelsJSON), &models); err != nil {
		return nil
	}
	return models
}
