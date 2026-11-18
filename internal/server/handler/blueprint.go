package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/eushing/agentwork/internal/service"
)

// Blueprint endpoints (Phase A, docs/BLUEPRINT-PROPOSAL.md §5/§4.12): the
// spec graph is a first-class entity — create, draft, publish, list, and a
// spec-graph rendering endpoint that mirrors WorkflowGraph's shape so the
// frontend renders it with the existing workflow-graph component.

// listBlueprints returns all blueprints.
func (h *Handlers) listBlueprints(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeJSON(w, []service.Blueprint{}, nil)
		return
	}
	out, err := h.Blueprint.List(r.Context())
	if out == nil {
		out = []service.Blueprint{}
	}
	writeJSON(w, out, err)
}

// createBlueprint registers a blueprint with its draft v1 content.
func (h *Handlers) createBlueprint(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("invalid JSON body"))
		return
	}
	out, err := h.Blueprint.Create(r.Context(), body.Name, body.Description, body.Content, "human", "")
	writeJSON(w, out, err)
}

// getBlueprint returns one blueprint with its versions.
func (h *Handlers) getBlueprint(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	bp, versions, err := h.Blueprint.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if versions == nil {
		versions = []service.BlueprintVersion{}
	}
	writeJSON(w, map[string]any{"blueprint": bp, "versions": versions}, nil)
}

// listBlueprintVersions returns one blueprint's versions newest first.
func (h *Handlers) listBlueprintVersions(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeJSON(w, []service.BlueprintVersion{}, nil)
		return
	}
	out, err := h.Blueprint.ListVersions(r.Context(), r.PathValue("id"))
	if out == nil {
		out = []service.BlueprintVersion{}
	}
	writeJSON(w, out, err)
}

// newBlueprintDraft opens version N+1 from the latest version.
func (h *Handlers) newBlueprintDraft(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	var body struct {
		Changelog string `json:"changelog"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // body optional
	out, err := h.Blueprint.NewDraft(r.Context(), r.PathValue("id"), body.Changelog)
	writeJSON(w, out, err)
}

// updateBlueprintDraft replaces a draft version's content (validated).
func (h *Handlers) updateBlueprintDraft(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("invalid JSON body"))
		return
	}
	out, err := h.Blueprint.UpdateDraft(r.Context(), r.PathValue("versionId"), body.Content)
	writeJSON(w, out, err)
}

// publishBlueprintVersion flips draft → published through the full gate.
func (h *Handlers) publishBlueprintVersion(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	var body struct {
		Changelog string `json:"changelog"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // body optional
	out, err := h.Blueprint.Publish(r.Context(), r.PathValue("versionId"), body.Changelog)
	writeJSON(w, out, err)
}

// blueprintVersionGraph renders a version's spec graph in the SAME shape as
// the execution workflow graph — the frontend's workflow-graph component
// renders it with zero layout changes (proposal §4.12).
func (h *Handlers) blueprintVersionGraph(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	out, err := h.Blueprint.SpecWorkflowGraph(r.Context(), r.PathValue("versionId"))
	writeJSON(w, out, err)
}

// statusFor maps service errors to HTTP statuses the same way writeErr does,
// for handlers that need to branch on the error before composing a payload.
func statusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, service.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, service.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, service.ErrConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}
