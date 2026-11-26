package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/service"
	"github.com/Sinkear/flowloop/internal/store"
)

// An oversized transcript must be refused before the whole body is decoded
// into memory; the service cap alone would only run afterwards.
func TestSaveChatConversationRejectsOversizedBody(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	h := &Handlers{ChatHistory: service.NewChatHistoryService(st)}
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /chat/conversations/{id}", h.saveChatConversation)

	body := `{"title":"big","messages":["` + strings.Repeat("a", (8<<20)+1024) + `"]}`
	req := httptest.NewRequest(http.MethodPut, "/chat/conversations/c1", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413 for oversized transcript, got %d: %s", rec.Code, rec.Body.String())
	}
}
