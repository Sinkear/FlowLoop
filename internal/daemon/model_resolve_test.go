package daemon

import (
	"testing"

	"github.com/Sinkear/flowloop/internal/link"
)

func TestResolveModel(t *testing.T) {
	t.Parallel()
	mk := func(ids ...string) []link.ModelInfo {
		out := make([]link.ModelInfo, 0, len(ids))
		for _, id := range ids {
			out = append(out, link.ModelInfo{ID: id})
		}
		return out
	}
	tests := []struct {
		name   string
		model  string
		models []link.ModelInfo
		want   string
	}{
		{"empty model is a no-op", "", mk("OpenGW/glm-5.3"), ""},
		{"qualified id passes through untouched", "OpenGW/glm-5.3", mk("OpenGW/glm-5.2"), "OpenGW/glm-5.3"},
		{"colon-qualified id passes through", "custom:opengw:glm-5.3", nil, "custom:opengw:glm-5.3"},
		{"exact advertised id is kept", "deepseek-v4-pro", mk("OpenGW/glm-5.3", "deepseek-v4-pro"), "deepseek-v4-pro"},
		{
			// Live case (opencode catalog): bare name, unique provider sibling.
			"unique slash sibling is qualified",
			"deepseek-v4.1-flash-1017",
			mk("OpenGW/deepseek-v4-flash-0731", "OpenGW/deepseek-v4.1-flash-1017", "OpenGW/glm-5.3"),
			"OpenGW/deepseek-v4.1-flash-1017",
		},
		{
			"unique colon sibling is qualified",
			"glm-5.3",
			mk("custom:hwdevspace:glm-5.2", "custom:opengw:glm-5.3"),
			"custom:opengw:glm-5.3",
		},
		{
			// Live case (hermes catalog): the same bare name sits under
			// several providers — picking one would reroute the run to
			// another gateway, so the bare id must survive.
			"ambiguous siblings keep the bare id",
			"deepseek-v4.1-flash-1017",
			mk("custom:deepseek-v4.1-flash-1017", "custom:hwdevspace:deepseek-v4.1-flash-1017", "custom:opengw:deepseek-v4.1-flash-1017"),
			"deepseek-v4.1-flash-1017",
		},
		{
			"no sibling keeps the bare id (stale probe)",
			"glm-99.9-turbo",
			mk("OpenGW/glm-5.3"),
			"glm-99.9-turbo",
		},
		{"nil catalog keeps the bare id", "glm-5.3-flash", nil, "glm-5.3-flash"},
		{"whitespace is trimmed", "  glm-5.3  ", mk("OpenGW/glm-5.3"), "OpenGW/glm-5.3"},
		{
			// A qualified id must not ALSO be suffix-matched: "OpenGW/x"
			// ends with ":..."? No — but "a/b/c" contains "/", so it is
			// passthrough before any sibling scan can fire.
			"qualified is never re-resolved against siblings",
			"OpenGW/deepseek-v4.1-flash-1017",
			mk("hwdevspace/OpenGW/deepseek-v4.1-flash-1017"),
			"OpenGW/deepseek-v4.1-flash-1017",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveModel(tt.model, tt.models); got != tt.want {
				t.Fatalf("resolveModel(%q, %v) = %q, want %q", tt.model, tt.models, got, tt.want)
			}
		})
	}
}

func TestParseRuntimeModels(t *testing.T) {
	t.Parallel()
	if got := parseRuntimeModels("not json"); got != nil {
		t.Fatalf("malformed JSON must parse to nil, got %v", got)
	}
	if got := parseRuntimeModels(""); got != nil {
		t.Fatalf("empty JSON must parse to nil, got %v", got)
	}
	got := parseRuntimeModels(`[{"id":"OpenGW/glm-5.3","name":"GLM-5.3"}]`)
	if len(got) != 1 || got[0].ID != "OpenGW/glm-5.3" || got[0].Name != "GLM-5.3" {
		t.Fatalf("unexpected parse result: %+v", got)
	}
}
