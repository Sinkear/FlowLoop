package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// patchAcceptance issues PATCH /domains/{id}/acceptance and returns the
// response recorder plus the decoded body (domain on 200, error map otherwise).
func patchAcceptance(t *testing.T, mux *http.ServeMux, id, body string) (*httptest.ResponseRecorder, service.Domain, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPatch, "/domains/"+id+"/acceptance", strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var dom service.Domain
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &dom); err != nil {
			t.Fatalf("decode domain: %v (body %s)", err, rec.Body.String())
		}
	}
	var errBody map[string]any
	if rec.Code != http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("decode error: %v (body %s)", err, rec.Body.String())
		}
	}
	return rec, dom, errBody
}

// TestPatchDomainAcceptance covers the explicit acceptance-mode endpoint:
// it sets hitl/hootl, clears back to 待定 with "", rejects an unknown level
// (400 + validation code), and 404s an unknown domain — and the response is
// the full updated domain so the caller can re-render.
func TestPatchDomainAcceptance(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	ds := service.NewDomainService(st, events.NewBus())
	d, err := ds.Create(ctx, service.Domain{Name: "acc", GitURL: "https://example.com/acc.git"})
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}

	h := &Handlers{Domain: ds}
	mux := http.NewServeMux()
	h.Mount(mux)

	for _, level := range []string{"hitl", "hootl"} {
		rec, dom, _ := patchAcceptance(t, mux, d.ID, `{"level":"`+level+`"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH %q: status %d body %s", level, rec.Code, rec.Body.String())
		}
		if dom.AcceptanceLevel != level || dom.ID != d.ID {
			t.Fatalf("PATCH %q: unexpected body %+v", level, dom)
		}
	}

	// Clearing back to 待定 is this endpoint's job (PUT cannot).
	rec, dom, _ := patchAcceptance(t, mux, d.ID, `{"level":""}`)
	if rec.Code != http.StatusOK || dom.AcceptanceLevel != "" {
		t.Fatalf("clear: status %d body %+v (%s)", rec.Code, dom, rec.Body.String())
	}

	// Persisted: Get agrees with the response.
	got, err := ds.Get(ctx, d.ID)
	if err != nil || got.AcceptanceLevel != "" {
		t.Fatalf("get after clear: %+v %v", got, err)
	}

	// Unknown level → 400 with the validation code.
	rec, _, errBody := patchAcceptance(t, mux, d.ID, `{"level":"auto"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid level: expected 400, got %d (%s)", rec.Code, rec.Body.String())
	}
	if errBody["code"] != service.CodeValidation {
		t.Fatalf("invalid level: expected code %s, got %+v", service.CodeValidation, errBody)
	}

	// Unknown domain → 404, consistent with the other domain routes.
	rec, _, errBody = patchAcceptance(t, mux, "missing", `{"level":"hitl"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing domain: expected 404, got %d (%s)", rec.Code, rec.Body.String())
	}
	if errBody["code"] != service.CodeNotFound {
		t.Fatalf("missing domain: expected code %s, got %+v", service.CodeNotFound, errBody)
	}
}
