package handler

// Hook handler wiring: the REST endpoints for the hooks registry, bindings
// and run history (spec: tmp/hooks-spec.md §5.1). Kept in hook.go to keep the
// feature in one place. The endpoints follow the same conventions as skills:
// JSON bodies (multipart where a file upload is natural), writeJSON for the
// response envelope.

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/eushing/agentwork/internal/service"
)

// hookBindingInput is the binding request shape for both scopes.
type hookBindingInput struct {
	HookID string            `json:"hook_id"`
	Event  string            `json:"event"`
	Seq    int               `json:"seq"`
	Params map[string]string `json:"params"` // per-binding overrides (flat string map)
}

// createHookRequest is the JSON create/update-meta shape. Content is the
// script text itself (web editor); multipart upload is also accepted.
type createHookRequest struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Content     string            `json:"content"`
	Interpreter string            `json:"interpreter"`
	TimeoutSec  int               `json:"timeout_sec"`
	Params      map[string]string `json:"params"`
}

func (h *Handlers) listHooks(w http.ResponseWriter, r *http.Request) {
	out, err := h.Hooks.List(r.Context())
	writeJSON(w, out, err)
}

// listHookEvents exposes the event registry for UI dropdowns.
func (h *Handlers) listHookEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"events": service.HookEvents()}, nil)
}

func (h *Handlers) createHook(w http.ResponseWriter, r *http.Request) {
	req := createHookRequest{}
	// Multipart: file field carries the script, form fields carry the meta.
	// Same dual mode as skills (createSkill).
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		req.Name = r.FormValue("name")
		req.Description = r.FormValue("description")
		req.Interpreter = r.FormValue("interpreter")
		if v := r.FormValue("timeout_sec"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			req.TimeoutSec = n
		}
		file, _, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			b, err := io.ReadAll(io.LimitReader(file, service.HookMaxContent+1))
			if err != nil {
				writeErr(w, http.StatusBadRequest, err)
				return
			}
			req.Content = string(b)
		}
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.Hooks.Create(r.Context(), req.Name, req.Description, req.Content, req.Interpreter, req.TimeoutSec, req.Params)
	writeJSON(w, out, err)
}

func (h *Handlers) getHook(w http.ResponseWriter, r *http.Request) {
	out, err := h.Hooks.Get(r.Context(), r.PathValue("id"))
	writeJSON(w, out, err)
}

func (h *Handlers) updateHook(w http.ResponseWriter, r *http.Request) {
	// Meta updates use pointer fields so absent keys keep current values.
	var req struct {
		Name        *string            `json:"name"`
		Description *string            `json:"description"`
		Interpreter *string            `json:"interpreter"`
		TimeoutSec  *int               `json:"timeout_sec"`
		Params      *map[string]string `json:"params"` // present = replace-all; absent = keep
		Enabled     *bool              `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.Hooks.UpdateMeta(r.Context(), r.PathValue("id"), req.Name, req.Description, req.Interpreter, req.TimeoutSec, req.Enabled, req.Params)
	writeJSON(w, out, err)
}

func (h *Handlers) updateHookContent(w http.ResponseWriter, r *http.Request) {
	content := ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		defer file.Close()
		b, err := io.ReadAll(io.LimitReader(file, service.HookMaxContent+1))
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		content = string(b)
	} else {
		var req struct {
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		content = req.Content
	}
	out, err := h.Hooks.UpdateContent(r.Context(), r.PathValue("id"), content)
	writeJSON(w, out, err)
}

func (h *Handlers) testHook(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Event string `json:"event"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req) // empty body = default event
	out, err := h.Hooks.TestHook(r.Context(), r.PathValue("id"), req.Event)
	writeJSON(w, out, err)
}

func (h *Handlers) deleteHook(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"deleted": r.PathValue("id")}, h.Hooks.Delete(r.Context(), r.PathValue("id")))
}

func (h *Handlers) listHookRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out, err := h.Hooks.Runs(r.Context(), r.PathValue("id"), limit)
	writeJSON(w, out, err)
}

// ── bindings ──

func (h *Handlers) bindGoalHook(w http.ResponseWriter, r *http.Request) {
	var req hookBindingInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.Hooks.BindGoal(r.Context(), r.PathValue("goalID"), req.HookID, req.Event, req.Seq, req.Params)
	writeJSON(w, out, err)
}

func (h *Handlers) listGoalHooks(w http.ResponseWriter, r *http.Request) {
	out, err := h.Hooks.GoalEffectiveHooks(r.Context(), r.PathValue("goalID"))
	writeJSON(w, out, err)
}

func (h *Handlers) unbindGoalHook(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"deleted": r.PathValue("bindingID")},
		h.Hooks.UnbindGoal(r.Context(), r.PathValue("goalID"), r.PathValue("bindingID")))
}

func (h *Handlers) bindDomainHook(w http.ResponseWriter, r *http.Request) {
	var req hookBindingInput
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.Hooks.BindDomain(r.Context(), r.PathValue("domainID"), req.HookID, req.Event, req.Seq, req.Params)
	writeJSON(w, out, err)
}

func (h *Handlers) listDomainHooks(w http.ResponseWriter, r *http.Request) {
	out, err := h.Hooks.DomainHooks(r.Context(), r.PathValue("domainID"))
	writeJSON(w, out, err)
}

func (h *Handlers) unbindDomainHook(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"deleted": r.PathValue("bindingID")},
		h.Hooks.UnbindDomain(r.Context(), r.PathValue("domainID"), r.PathValue("bindingID")))
}
