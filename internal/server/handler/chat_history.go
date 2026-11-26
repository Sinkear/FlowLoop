package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Sinkear/flowloop/internal/service"
)

// Chat workbench history (bug 2): the conversation list + transcript live on
// the daemon so a different browser, a different origin (localhost vs
// 127.0.0.1), or a cleared site-data state still shows the same history.

func (h *Handlers) listChatConversations(w http.ResponseWriter, r *http.Request) {
	out, err := h.ChatHistory.List(r.Context(), 60)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"conversations": out}, nil)
}

func (h *Handlers) getChatConversation(w http.ResponseWriter, r *http.Request) {
	out, err := h.ChatHistory.Get(r.Context(), r.PathValue("id"))
	writeJSON(w, out, err)
}

func (h *Handlers) saveChatConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, service.NewFieldRequiredError("id"))
		return
	}
	var in service.ChatConversation
	// Reject oversized transcripts before decoding them into memory. The
	// service still enforces the same cap, but only after the whole body has
	// already been materialized here.
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeErr(w, http.StatusRequestEntityTooLarge, tooBig)
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	in.ID = id
	out, err := h.ChatHistory.Save(r.Context(), in)
	writeJSON(w, out, err)
}

func (h *Handlers) deleteChatConversation(w http.ResponseWriter, r *http.Request) {
	if err := h.ChatHistory.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
