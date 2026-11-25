package acpbackend

import (
	"context"
	"log"
	"strings"

	"github.com/Sinkear/flowloop/internal/acp"
)

// modelConfigID is the config-option id agents use for the model selector.
// Verified against opencode 2.2.5: session/new returns
// {id:"model", category:"model", type:"select",
//
//	options:[{value:"provider/model", name:"..."}]}. The ACP spec also names
//
// the category "model" for this option, so either match is accepted.
const modelConfigID = "model"

// applyModel selects the requested model on a freshly opened ACP session.
//
// Preference order (best effort — a wrong/unsupported model must never fail
// a run, only log and keep the agent's default):
//  1. If session/new advertised a "model" config option (opencode, hermes
//     and other config-option agents), set it via session/set_config_option.
//  2. Otherwise — or if that call fails — try the spec's session/set_model.
//
// A Model of "" is a no-op: the agent's default stays.
func applyModel(ctx context.Context, sess *acp.Session, sessionID string, model string, configOptions []acp.SessionConfigOption) {
	model = strings.TrimSpace(model)
	if model == "" {
		return
	}
	if hasModelConfigOption(configOptions) {
		_, err := sess.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
			SessionID: acp.SessionId(sessionID),
			ConfigID:  modelConfigID,
			Type:      "select",
			Value:     model,
		})
		if err == nil {
			return
		}
		log.Printf("acpbackend: select model %q via session/set_config_option: %v; trying session/set_model", model, err)
	}
	if _, err := sess.SetSessionModel(ctx, acp.SetSessionModelRequest{
		SessionID: acp.SessionId(sessionID),
		ModelID:   model,
	}); err != nil {
		log.Printf("acpbackend: select model %q: %v (continuing with the agent default)", model, err)
	}
}

// hasModelConfigOption reports whether session/new advertised a model
// selector — an option whose id or category is "model".
func hasModelConfigOption(opts []acp.SessionConfigOption) bool {
	for _, o := range opts {
		if o.ID == modelConfigID || o.Category == modelConfigID {
			return true
		}
	}
	return false
}
