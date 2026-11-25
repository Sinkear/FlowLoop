package handler

import (
	"encoding/json"
	"net/http"

	"github.com/Sinkear/flowloop/internal/service"
)

func (h *Handlers) listProjectSquads(w http.ResponseWriter, r *http.Request) {
	if h.ProjectSquad == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("project squad service not configured"))
		return
	}
	out, err := h.ProjectSquad.List(r.Context(), r.PathValue("id"))
	writeJSON(w, out, err)
}

func (h *Handlers) addProjectSquad(w http.ResponseWriter, r *http.Request) {
	if h.ProjectSquad == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("project squad service not configured"))
		return
	}
	var body struct {
		SquadID string `json:"squad_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.SquadID == "" {
		writeErr(w, http.StatusBadRequest, service.NewFieldRequiredError("squad_id"))
		return
	}
	out, err := h.ProjectSquad.Add(r.Context(), r.PathValue("id"), body.SquadID)
	writeJSON(w, out, err)
}

func (h *Handlers) removeProjectSquad(w http.ResponseWriter, r *http.Request) {
	if h.ProjectSquad == nil {
		writeErr(w, http.StatusServiceUnavailable, service.NewValidationError("project squad service not configured"))
		return
	}
	if err := h.ProjectSquad.Remove(r.Context(), r.PathValue("id"), r.PathValue("squadID")); err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
