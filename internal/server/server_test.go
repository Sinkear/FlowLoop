package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sinkear/flowloop/internal/service"
	"github.com/Sinkear/flowloop/internal/store"
)

func newTestSettings(t *testing.T, token string) *service.SettingsService {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc := service.NewSettingsService(st)
	if token != "" {
		_ = svc.Set(context.Background(), "platform.worker_token", token)
	}
	return svc
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:7373", true},
		{"localhost:7373", true},
		{"[::1]:7373", true},
		{"0.0.0.0:7373", false},
		{":7373", false},
		{"0.0.0.0:8080", false},
		{"192.168.1.100:7373", false},
		{"10.0.0.1:7373", false},
	}
	for _, c := range cases {
		if got := isLoopbackAddr(c.addr); got != c.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", c.addr, got, c.want)
		}
	}
}

func TestCSRFMiddleware(t *testing.T) {
	cases := []struct {
		name    string
		method  string
		origin  string
		host    string
		want    int
		reached bool
	}{
		{"same-origin POST", http.MethodPost, "http://127.0.0.1:7373", "127.0.0.1:7373", http.StatusOK, true},
		{"dev-server origin POST", http.MethodPost, "http://localhost:3000", "127.0.0.1:7373", http.StatusOK, true},
		{"no origin (curl/CLI)", http.MethodPost, "", "127.0.0.1:7373", http.StatusOK, true},
		{"cross-site POST", http.MethodPost, "https://evil.example", "127.0.0.1:7373", http.StatusForbidden, false},
		{"proxied workspace domain without allowlist", http.MethodPost, "https://17246-d08a.workspace.developer.huaweicloud.com", "127.0.0.1:7373", http.StatusForbidden, false},
		{"null origin POST", http.MethodPost, "null", "127.0.0.1:7373", http.StatusForbidden, false},
		{"cross-site GET stays readable", http.MethodGet, "https://evil.example", "127.0.0.1:7373", http.StatusOK, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(tc.method, "http://"+tc.host+"/api/thing", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			rec := httptest.NewRecorder()
			csrfMiddleware(nil, next).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if reached != tc.reached {
				t.Fatalf("next reached = %v, want %v", reached, tc.reached)
			}
		})
	}
}

func TestSplitOrigin(t *testing.T) {
	cases := []struct {
		raw          string
		scheme, host string
		port         string
		ok           bool
	}{
		{"https://a.example", "https", "a.example", "443", true},
		{"http://a.example:8080", "http", "a.example", "8080", true},
		{"HTTPS://A.Example", "https", "a.example", "443", true},
		{"https://*.a.example", "https", "*.a.example", "443", true},
		{"http://[::1]:7373", "http", "[::1]", "7373", true},
		{"https://a.example/path", "https", "a.example", "443", true},
		{"null", "", "", "", false},
		{"ftp://a.example", "", "", "", false},
		{"https://", "", "", "", false},
		{"", "", "", "", false},
	}
	for _, tc := range cases {
		scheme, host, port, ok := splitOrigin(tc.raw)
		if scheme != tc.scheme || host != tc.host || port != tc.port || ok != tc.ok {
			t.Errorf("splitOrigin(%q) = (%q,%q,%q,%v), want (%q,%q,%q,%v)",
				tc.raw, scheme, host, port, ok, tc.scheme, tc.host, tc.port, tc.ok)
		}
	}
}

func TestCSRFMiddlewareAllowlist(t *testing.T) {
	t.Setenv("FLOWLOOP_ALLOWED_ORIGINS", "https://*.workspace.developer.huaweicloud.com, https://api.example:8443")
	cases := []struct {
		name   string
		origin string
		want   int
	}{
		{"wildcard subdomain", "https://17246-d08a67.workspace.developer.huaweicloud.com", http.StatusOK},
		{"wildcard bare apex", "https://workspace.developer.huaweicloud.com", http.StatusForbidden},
		{"wildcard requires scheme match", "http://17246-d08a67.workspace.developer.huaweicloud.com", http.StatusForbidden},
		{"wildcard suffix smuggling", "https://17246-d08a67.workspace.developer.huaweicloud.com.evil.example", http.StatusForbidden},
		{"exact entry with port", "https://api.example:8443", http.StatusOK},
		{"exact entry wrong port", "https://api.example:8444", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7373/api/thing", nil)
			req.Host = "127.0.0.1:7373"
			req.Header.Set("Origin", tc.origin)
			rec := httptest.NewRecorder()
			csrfMiddleware(nil, next).ServeHTTP(rec, req)
			if rec.Code != tc.want || reached != (tc.want == http.StatusOK) {
				t.Fatalf("origin %q: status=%d reached=%v, want %d", tc.origin, rec.Code, reached, tc.want)
			}
		})
	}
}

func TestCSRFMiddlewareLegacyEnvAllowlist(t *testing.T) {
	t.Setenv("OUROFLOW_ALLOWED_ORIGINS", "https://legacy.example")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7373/api/thing", nil)
	req.Host = "127.0.0.1:7373"
	req.Header.Set("Origin", "https://legacy.example")
	rec := httptest.NewRecorder()
	csrfMiddleware(nil, next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("legacy env allowlist: status=%d, want 200", rec.Code)
	}
}

func TestCSRFMiddlewareSettingsAllowlist(t *testing.T) {
	settings := newTestSettings(t, "")
	if err := settings.Set(context.Background(), allowedOriginsSettingKey, "https://app.example"); err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	h := csrfMiddleware(settings, next)

	post := func(origin string) int {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:7373/api/thing", nil)
		req.Host = "127.0.0.1:7373"
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post("https://app.example"); got != http.StatusOK {
		t.Fatalf("settings allowlisted origin: status=%d, want 200", got)
	}
	if got := post("https://other.example"); got != http.StatusForbidden {
		t.Fatalf("unlisted origin: status=%d, want 403", got)
	}
	// A hot rotation of the setting must be visible to the next request.
	if err := settings.Set(context.Background(), allowedOriginsSettingKey, ""); err != nil {
		t.Fatal(err)
	}
	if got := post("https://app.example"); got != http.StatusForbidden {
		t.Fatalf("cleared allowlist: status=%d, want 403", got)
	}
}

func TestSameOriginRequestThroughProxy(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		host       string
		fproto     string
		fhost      string
		origin     string
		want       bool
	}{
		{
			"loopback peer behind TLS proxy",
			"127.0.0.1:5555", "127.0.0.1:17450", "https", "17246-d08a67.workspace.developer.huaweicloud.com",
			"https://17246-d08a67.workspace.developer.huaweicloud.com", true,
		},
		{
			"proxy preserves host, forwarded proto only",
			"127.0.0.1:5555", "17246-d08a67.workspace.developer.huaweicloud.com", "https", "",
			"https://17246-d08a67.workspace.developer.huaweicloud.com", true,
		},
		{
			"forwarded host carries the default port",
			"127.0.0.1:5555", "127.0.0.1:17450", "https", "17246-d08a67.workspace.developer.huaweicloud.com:443",
			"https://17246-d08a67.workspace.developer.huaweicloud.com", true,
		},
		{
			"forwarded host non-default port mismatch",
			"127.0.0.1:5555", "127.0.0.1:17450", "https", "17246-d08a67.workspace.developer.huaweicloud.com:8443",
			"https://17246-d08a67.workspace.developer.huaweicloud.com", false,
		},
		{
			"remote peer cannot spoof forwarded headers",
			"192.0.2.9:4444", "127.0.0.1:17450", "https", "17246-d08a67.workspace.developer.huaweicloud.com",
			"https://17246-d08a67.workspace.developer.huaweicloud.com", false,
		},
		{
			"forwarded http stays http",
			"127.0.0.1:5555", "127.0.0.1:17450", "http", "",
			"http://127.0.0.1:17450", true,
		},
		{
			"no forwarded proto keeps plain http",
			"127.0.0.1:5555", "127.0.0.1:17450", "", "",
			"https://127.0.0.1:17450", false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/api/thing", nil)
			req.Host = tc.host
			req.RemoteAddr = tc.remoteAddr
			if tc.fproto != "" {
				req.Header.Set("X-Forwarded-Proto", tc.fproto)
			}
			if tc.fhost != "" {
				req.Header.Set("X-Forwarded-Host", tc.fhost)
			}
			if got := sameOriginRequest(tc.origin, req); got != tc.want {
				t.Errorf("sameOriginRequest(%q) = %v, want %v", tc.origin, got, tc.want)
			}
		})
	}
}

func TestAuthMiddleware(t *testing.T) {
	settings := newTestSettings(t, "secret123")
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := authMiddleware(settings, next)

	// Correct token in query
	called = false
	req := httptest.NewRequest("GET", "/?token=secret123", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("query token: expected 200 + called, got %d called=%v", rec.Code, called)
	}

	// Correct token in Authorization header
	called = false
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("bearer token: expected 200 + called, got %d called=%v", rec.Code, called)
	}

	// Wrong token
	called = false
	req = httptest.NewRequest("GET", "/?token=wrong", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("wrong token: expected 401 + not called, got %d called=%v", rec.Code, called)
	}

	// Missing token
	called = false
	req = httptest.NewRequest("GET", "/", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("missing token: expected 401 + not called, got %d called=%v", rec.Code, called)
	}

	// /rpc is EXEMPT (agent/link channel with app-layer auth)
	called = false
	req = httptest.NewRequest("GET", "/rpc", nil) // no token at all
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("/rpc: exempt path should pass without token, got %d called=%v", rec.Code, called)
	}

	// /connect is NOT exempt (machine connect CLI passes ?token=)
	called = false
	req = httptest.NewRequest("GET", "/connect", nil) // no token
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("/connect: without token should be 401, got %d called=%v", rec.Code, called)
	}
	called = false
	req = httptest.NewRequest("GET", "/connect?token=secret123", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("/connect: with token should pass, got %d called=%v", rec.Code, called)
	}

	// OPTIONS (CORS preflight) is EXEMPT — browsers send it without token
	called = false
	req = httptest.NewRequest(http.MethodOptions, "/api/goals", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("OPTIONS preflight: expected pass without token, got %d called=%v", rec.Code, called)
	}

	// Token rotation: changing the settings takes effect immediately
	_ = settings.Set(context.Background(), "platform.worker_token", "rotated456")
	called = false
	req = httptest.NewRequest("GET", "/?token=secret123", nil) // old token
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("old token after rotation: expected 401, got %d", rec.Code)
	}
	called = false
	req = httptest.NewRequest("GET", "/?token=rotated456", nil) // new token
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("new token after rotation: expected 200, got %d", rec.Code)
	}
}
