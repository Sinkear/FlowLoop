package handler

import (
	"net/url"
	"strings"

	"github.com/Sinkear/flowloop/internal/service"
)

// allowedOriginsKey is the app_settings key holding the CSRF origin allowlist.
// The daemon's csrf middleware (internal/server/origin.go) reads it on every
// state-changing request, so updating it takes effect immediately.
const allowedOriginsKey = "platform.allowed_origins"

// normalizeAllowedOrigins validates a comma/semicolon/newline separated list of
// origin patterns and returns it in canonical comma-joined form. Each entry must
// be "scheme://host[:port]" with scheme http or https; the host may use a
// leading "*." wildcard (any subdomain). Ports are kept as written — the
// matcher normalizes http→80 and https→443.
func normalizeAllowedOrigins(raw string) (string, error) {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		u, err := url.Parse(f)
		if err != nil {
			return "", service.NewValidationError("allowed_origins: invalid entry " + f)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "http" && scheme != "https" {
			return "", service.NewValidationError("allowed_origins: " + f + " must start with http:// or https://")
		}
		if u.Hostname() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return "", service.NewValidationError("allowed_origins: " + f + " must be scheme://host[:port] with no path")
		}
		if strings.Contains(f, "*") && !strings.HasPrefix(u.Hostname(), "*.") {
			return "", service.NewValidationError("allowed_origins: " + f + " only supports a leading *. wildcard")
		}
		out = append(out, scheme+"://"+strings.ToLower(u.Host))
	}
	return strings.Join(out, ","), nil
}
