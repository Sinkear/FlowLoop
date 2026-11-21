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

// ── Phase C (proposal §12): patch proposals + diff + A/B compare ──

// listBlueprintProposals: GET /blueprints/{id}/proposals
func (h *Handlers) listBlueprintProposals(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	out, err := h.Blueprint.ListProposals(r.Context(), r.PathValue("id"))
	if out == nil && err == nil {
		out = []service.BlueprintProposal{}
	}
	writeJSON(w, map[string]any{"proposals": out}, err)
}

// createBlueprintProposal: POST /blueprints/{id}/proposals
func (h *Handlers) createBlueprintProposal(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	var body struct {
		Title             string                   `json:"title"`
		Hypothesis        string                   `json:"hypothesis"`
		ExpectedMetric    string                   `json:"expected_metric"`
		ExpectedDirection string                   `json:"expected_direction"`
		Patches           []service.BlueprintPatch `json:"patches"`
		FindingID         string                   `json:"finding_id"`
		BaseVersionID     string                   `json:"base_version_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("invalid JSON body: "+err.Error()))
		return
	}
	out, err := h.Blueprint.CreateProposal(r.Context(), r.PathValue("id"), body.BaseVersionID,
		body.Title, body.Hypothesis, body.ExpectedMetric, body.ExpectedDirection,
		body.Patches, body.FindingID, "human", "")
	writeJSON(w, out, err)
}

// getBlueprintProposal: GET /blueprint-proposals/{proposalId}
func (h *Handlers) getBlueprintProposal(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	out, err := h.Blueprint.GetProposal(r.Context(), r.PathValue("proposalId"))
	writeJSON(w, out, err)
}

// blueprintProposalDiff: GET /blueprint-proposals/{proposalId}/diff —
// three-color structured diff, base vs patched (§12.2).
func (h *Handlers) blueprintProposalDiff(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	out, err := h.Blueprint.ProposalDiff(r.Context(), r.PathValue("proposalId"))
	writeJSON(w, out, err)
}

// approveBlueprintProposal: POST /blueprint-proposals/{proposalId}/approve —
// apply + re-validate + open a NEW DRAFT (changelog = hypothesis). Never
// auto-publishes.
func (h *Handlers) approveBlueprintProposal(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	p, v, err := h.Blueprint.ApproveProposal(r.Context(), r.PathValue("proposalId"), "human", "")
	writeJSON(w, map[string]any{"proposal": p, "draft": v}, err)
}

// deleteBlueprintVersion: DELETE /blueprint-versions/{versionId} — removes a
// junk DRAFT version. Published versions are the blueprint's authority and
// history (refused, 409); a draft still referenced by a goal pin or a proposal
// is refused too, with the counts in the error message.
func (h *Handlers) deleteBlueprintVersion(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	if err := h.Blueprint.DeleteVersion(r.Context(), r.PathValue("versionId")); err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteBlueprint: DELETE /blueprints/{id} — removes the blueprint with its
// versions and proposals; refused (409) while a non-terminal goal still pins
// one of its versions. Terminal goals keep their historical pin.
func (h *Handlers) deleteBlueprint(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	if err := h.Blueprint.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// nextRoundFromProposal: POST /blueprint-proposals/{proposalId}/next-round —
// the explicit RSI loop closure. Publishes the approved proposal's applied
// version (if still a draft), clones the origin training goal, pins the new
// version, and (by default) starts the round immediately. Body:
//
//	{"start": false}  → create the next-round goal in backlog instead of active
//
// Idempotent: the created goal's source_ref is "rsi-proposal:<id>", so a
// second call returns the existing goal with created=false.
func (h *Handlers) nextRoundFromProposal(w http.ResponseWriter, r *http.Request) {
	if h.Goal == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("goal service not configured"))
		return
	}
	start := true
	if r.Body != nil && r.ContentLength != 0 {
		var body struct {
			Start *bool `json:"start"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, service.NewValidationError("invalid body: "+err.Error()))
			return
		}
		if body.Start != nil {
			start = *body.Start
		}
	}
	g, created, err := h.Goal.StartNextRoundFromProposal(r.Context(), r.PathValue("proposalId"), "human", "", start)
	writeJSON(w, map[string]any{"goal": g, "created": created}, err)
}

// rejectBlueprintProposal: POST /blueprint-proposals/{proposalId}/reject
func (h *Handlers) rejectBlueprintProposal(w http.ResponseWriter, r *http.Request) {
	if h.Blueprint == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("blueprint service not configured"))
		return
	}
	out, err := h.Blueprint.RejectProposal(r.Context(), r.PathValue("proposalId"), "human", "")
	writeJSON(w, out, err)
}

// blueprintABCompare: GET /workflows/blueprints/{id}/ab?goal_a=..&goal_b=.. —
// same-issue A/B face (§12.3): two goals' execution metrics side by side with
// deltas. The goals' pinned blueprint versions are echoed so the caller can
// verify they are actually comparing v1 vs v2 (or any chosen pair).
func (h *Handlers) blueprintABCompare(w http.ResponseWriter, r *http.Request) {
	if h.RSI == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("rsi service not configured"))
		return
	}
	goalA, goalB := r.URL.Query().Get("goal_a"), r.URL.Query().Get("goal_b")
	if goalA == "" || goalB == "" {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("query params goal_a and goal_b are both required"))
		return
	}
	ab, err := h.RSI.GoalABCompare(r.Context(), goalA, goalB)
	writeJSON(w, ab, err)
}
