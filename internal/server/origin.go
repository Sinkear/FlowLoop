package server

import (
	"context"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/Sinkear/flowloop/internal/logging"
	"github.com/Sinkear/flowloop/internal/service"
)

// allowedOriginsSettingKey is the app_settings key for the runtime-editable
// CSRF allowlist. The middleware reads it on every state-changing request
// (same pattern as platform.worker_token), so a reverse proxy that terminates
// TLS in front of the daemon can be unblocked by writing one key — no restart,
// no rebuild.
const allowedOriginsSettingKey = "platform.allowed_origins"

// allowedOriginsEnvKeys are the process-environment spellings of the same
// allowlist. FLOWLOOP_ALLOWED_ORIGINS is the current name; OUROFLOW_* is the
// pre-rename alias, kept so start scripts written before the rename keep
// working.
var allowedOriginsEnvKeys = []string{"FLOWLOOP_ALLOWED_ORIGINS", "OUROFLOW_ALLOWED_ORIGINS"}

// csrfAllowlist answers "may this browser Origin perform writes against the
// daemon?" for origins that are not loopback and not same-origin. Two sources
// are consulted: process environment (restart required, deployment-time
// setting) and app_settings (hot, for a misconfigured proxy today).
type csrfAllowlist struct {
	settings *service.SettingsService
	env      []string
}

func newCSRFAllowlist(settings *service.SettingsService) *csrfAllowlist {
	a := &csrfAllowlist{settings: settings}
	for _, key := range allowedOriginsEnvKeys {
		raw := strings.TrimSpace(os.Getenv(key))
		if raw == "" {
			continue
		}
		rules := splitOriginList(raw)
		a.env = append(a.env, rules...)
		logging.Infof("server: CSRF allowlist from %s: %s", key, strings.Join(rules, ", "))
	}
	return a
}

// allows reports whether origin is matched by the env or settings allowlist.
// An empty/unparseable origin never matches: the "null" origin (sandboxed
// iframe, opaque document) must stay rejected.
func (a *csrfAllowlist) allows(ctx context.Context, origin string) bool {
	if originMatches(a.env, origin) {
		return true
	}
	if a.settings == nil {
		return false
	}
	raw, err := a.settings.Get(ctx, allowedOriginsSettingKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return false
	}
	return originMatches(splitOriginList(raw), origin)
}

// splitOriginList splits a comma/semicolon/newline separated allowlist. Entries
// that are empty after trimming are dropped.
func splitOriginList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// originMatches reports whether origin is matched by one of rules. Comparison
// is strict on scheme, port, and (case-insensitively) host — a rule
// "https://app.example.com" does NOT allow "http://app.example.com". A rule may
// start with "*." to match any subdomain: "https://*.developer.huaweicloud.com"
// allows "https://a-b.developer.huaweicloud.com" but not the bare apex.
func originMatches(rules []string, origin string) bool {
	scheme, host, port, ok := splitOrigin(origin)
	if !ok {
		return false
	}
	for _, rule := range rules {
		rscheme, rhost, rport, ok := splitOrigin(rule)
		if !ok || rscheme != scheme || rport != port {
			continue
		}
		if rhost == host {
			return true
		}
		if suffix, wildcard := strings.CutPrefix(rhost, "*."); wildcard {
			if len(host) > len(suffix)+1 && strings.HasSuffix(host, "."+suffix) {
				return true
			}
		}
	}
	return false
}

// splitOrigin normalizes "scheme://host[:port]" into its three parts. Ports
// default to 443/80 by scheme so "https://a" and "https://a:443" are equal.
// Anything with a path, query, fragment, unknown scheme, or no host is
// rejected (an Origin header never carries those).
func splitOrigin(raw string) (scheme, host, port string, ok bool) {
	raw = strings.TrimSpace(raw)
	i := strings.Index(raw, "://")
	if i <= 0 {
		return "", "", "", false
	}
	scheme = strings.ToLower(raw[:i])
	if scheme != "http" && scheme != "https" {
		return "", "", "", false
	}
	rest := raw[i+3:]
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	rest = strings.ToLower(rest)
	if rest == "" {
		return "", "", "", false
	}
	if strings.HasPrefix(rest, "[") { // bracketed IPv6 literal, optional port
		end := strings.Index(rest, "]")
		if end < 0 {
			return "", "", "", false
		}
		host = rest[:end+1]
		if after := rest[end+1:]; after != "" {
			if !strings.HasPrefix(after, ":") {
				return "", "", "", false
			}
			port = after[1:]
		}
	} else if h, p, err := net.SplitHostPort(rest); err == nil {
		host, port = h, p
	} else {
		host = rest
	}
	if host == "" {
		return "", "", "", false
	}
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	for _, r := range port {
		if r < '0' || r > '9' {
			return "", "", "", false
		}
	}
	return scheme, host, port, true
}

// effectiveOrigin reports the scheme://host the browser actually used, as seen
// through a reverse proxy. The daemon listens on loopback behind Caddy (or the
// DevSpace gateway), which terminates TLS and forwards plain HTTP: r.TLS is nil
// and r.Host may be rewritten, so a same-origin request from the proxied UI
// would otherwise look cross-origin. X-Forwarded-Proto/Host are honored only
// for loopback peers — a remote client cannot spoof them, because any request
// carrying them is non-simple and needs a CORS preflight, and corsMiddleware
// only answers preflights for loopback origins (see corsMiddleware).
func effectiveOrigin(r *http.Request) (scheme, host string) {
	scheme, host = "http", r.Host
	if r.TLS != nil {
		scheme = "https"
	}
	if !loopbackRemoteAddr(r.RemoteAddr) {
		return scheme, host
	}
	if v, ok := forwardedHeaderValue(r.Header.Get("X-Forwarded-Proto")); ok {
		if v == "http" || v == "https" {
			scheme = v
		}
	}
	if v, ok := forwardedHeaderValue(r.Header.Get("X-Forwarded-Host")); ok {
		if _, _, _, valid := splitOrigin("http://" + v); valid {
			host = v
		}
	}
	return scheme, host
}

// forwardedHeaderValue takes the first element of a comma-separated proxy
// header (Caddy appends, so the first is the browser-facing value).
func forwardedHeaderValue(raw string) (string, bool) {
	first, _, _ := strings.Cut(raw, ",")
	first = strings.ToLower(strings.TrimSpace(first))
	return first, first != ""
}

// loopbackRemoteAddr reports whether addr (host:port) is a loopback peer.
func loopbackRemoteAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	switch host {
	case "localhost":
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
