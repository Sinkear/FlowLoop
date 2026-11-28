package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// skillFileSyncCmd implements `flowloop skill sync <name>` — the one-command
// health check + guided remediation for skill↔repository drift. It is the
// CLI face of the sync flow (see AGENTS.md "Skill → Repository Sync"): a
// single call that answers "is my local skill up to date with its repo?"
// and, when it is not, tells the agent exactly which sub-command to run.
//
// Output contract (stdout, JSON — agents parse this):
//
//	{"skill": "...", "state": "in_sync|remote_ahead|local_ahead|diverged",
//	 "has_update": true, "summary": "...", "next_step": "pull|none|..."}
//
// Exit code 0 both when in sync and when updates exist — a state is not an
// error. Network/auth failures print {"error": "..."} and exit 1.
func skillFileSyncCmd(args []string) {
	if len(args) < 1 {
		fail("usage: flowloop skill sync <name>")
	}
	name := args[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	syncURL := fmt.Sprintf("%s/skills/%s/sync", base, skillID)
	var syncMap map[string]any
	if err := getJSONTo(syncURL, &syncMap); err != nil {
		fail("compare skill sync: %v", err)
	}

	state, _ := syncMap["state"].(string)
	remoteSHA, _ := syncMap["remote_sha"].(string)
	baseBranch, _ := syncMap["base_branch"].(string)
	if baseBranch == "" {
		baseBranch = "main"
	}

	payload := map[string]any{
		"skill":   name,
		"state":   state,
		"summary": "",
	}
	if state == "" {
		state = "unknown"
		payload["state"] = state
	}
	switch state {
	case "in_sync":
		payload["summary"] = "Local skill matches the repository."
		payload["next_step"] = "none"
	case "remote_ahead":
		payload["summary"] = "Repository has newer files than the local skill."
		payload["next_step"] = "pull"
	case "local_ahead":
		payload["summary"] = "Local skill has newer files than the repository."
		payload["next_step"] = "pr"
	case "diverged":
		payload["summary"] = "Both sides have unmerged changes."
		payload["next_step"] = "compare"
	default: // unknown
		payload["summary"] = "Could not determine sync state."
		payload["next_step"] = "compare"
	}
	if remoteSHA != "" {
		payload["remote_sha"] = remoteSHA
	}
	printJSONMap(payload)
}

// skillFilePullCmd implements `flowloop skill pull <name>` — fetch the
// latest files from the bound repository and REPLACE the local skill
// (irreversible: local-only edits are overwritten). Prints the sync result
// JSON on success.
func skillFilePullCmd(args []string) {
	if len(args) < 1 {
		fail("usage: flowloop skill pull <name>")
	}
	name := args[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	body := map[string]any{"direction": "pull"}
	result, err := postJSONMap(fmt.Sprintf("%s/skills/%s/sync", base, skillID), body)
	if err != nil {
		fail("pull skill: %v", err)
	}
	printJSONMap(result)
}

// skillFilePrCmd implements `flowloop skill pr <name>` — submit local skill
// edits to the bound repository as a pull request on a
// `flowloop/skill-<id>-<ts>` branch. Push permission required (403
// otherwise). Prints the PR result JSON (pr_url, branch, base, commit).
func skillFilePrCmd(args []string) {
	fs := flag.NewFlagSet("skill pr", flag.ExitOnError)
	title := fs.String("title", "", "pull request title")
	body := fs.String("body", "", "pull request body")
	// The documented usage puts the flags AFTER the skill name.
	pos := parseFlagsReordered(fs, args)

	if len(pos) < 1 {
		fail("usage: flowloop skill pr <name> [--title t] [--body b]")
	}
	name := pos[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	reqBody := map[string]any{}
	if *title != "" {
		reqBody["title"] = *title
	}
	if *body != "" {
		reqBody["body"] = *body
	}
	result, err := postJSONMap(fmt.Sprintf("%s/skills/%s/pull-request", base, skillID), reqBody)
	if err != nil {
		fail("create pull request: %v", err)
	}
	printJSONMap(result)
}

// skillSourceSetCmd implements `flowloop skill source set <name>` — bind or
// rebind a skill to a remote repository (repo URL, optional subpath /
// branch / token). Subsequent check/pull/pr commands operate on this
// binding.
func skillSourceSetCmd(args []string) {
	fs := flag.NewFlagSet("skill source set", flag.ExitOnError)
	repo := fs.String("repo", "", "repository URL (https://host/owner/repo[.git])")
	path := fs.String("path", "", "subdirectory inside the repository holding the skill")
	branch := fs.String("branch", "", "branch to track (default main)")
	token := fs.String("token", "", "git host token for private repositories")
	provider := fs.String("provider", "", "repo platform: github|gitcode (default: infer from URL host)")
	// The documented usage puts the flags AFTER the skill name.
	pos := parseFlagsReordered(fs, args)

	if len(pos) < 1 {
		fail("usage: flowloop skill source set <name> --repo <url> [--path p] [--branch b] [--provider github|gitcode] [--token t]")
	}
	if *repo == "" {
		fail("--repo is required")
	}
	name := pos[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	reqBody := map[string]any{"repo_url": *repo}
	if *path != "" {
		reqBody["path"] = *path
	}
	if *branch != "" {
		reqBody["branch"] = *branch
	}
	if *token != "" {
		reqBody["token"] = *token
	}
	if *provider != "" {
		reqBody["provider"] = *provider
	}
	result, err := putJSONMap(fmt.Sprintf("%s/skills/%s/source", base, skillID), reqBody)
	if err != nil {
		fail("set skill source: %v", err)
	}
	printJSONMap(result)
}

// skillSourceClearCmd implements `flowloop skill source clear <name>` —
// remove the repository binding and the sync bookkeeping. Local files stay.
func skillSourceClearCmd(args []string) {
	if len(args) < 1 {
		fail("usage: flowloop skill source clear <name>")
	}
	name := args[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	result, err := deleteJSONMap(fmt.Sprintf("%s/skills/%s/source", base, skillID))
	if err != nil {
		fail("clear skill source: %v", err)
	}
	printJSONMap(result)
}

// skillCheckCmd implements `flowloop skill check <name>` — the lightweight
// SHA-level probe: `git ls-remote` against the bound repo, compared with
// the SHA recorded at the last pull/PR. One network round trip, no clone.
func skillCheckCmd(args []string) {
	if len(args) < 1 {
		fail("usage: flowloop skill check <name>")
	}
	name := args[0]
	base := serverURL()

	skillList, err := getJSONMap(base + "/skills")
	if err != nil {
		fail("list skills: %v", err)
	}
	skillID, ok := skillIDByName(skillList, name)
	if !ok {
		fail("skill %q not found", name)
	}

	var check map[string]any
	if err := getJSONTo(fmt.Sprintf("%s/skills/%s/check", base, skillID), &check); err != nil {
		fail("check skill source: %v", err)
	}
	payload := map[string]any{
		"skill":      name,
		"state":      "unknown",
		"has_update": false,
	}
	if v, ok := check["state"].(string); ok && v != "" {
		payload["state"] = v
	}
	if v, ok := check["has_update"].(bool); ok {
		payload["has_update"] = v
	}
	if v, ok := check["remote_sha"].(string); ok && v != "" {
		payload["remote_sha"] = v
	}
	if v, ok := check["last_sha"].(string); ok && v != "" {
		payload["last_sha"] = v
	}
	printJSONMap(payload)
}

// skillSourceCmd is the `flowloop skill source <set|clear> ...` dispatcher.
func skillSourceCmd(args []string) {
	if len(args) < 1 {
		fail("usage: flowloop skill source <set|clear> <name> ...")
	}
	switch args[0] {
	case "set":
		skillSourceSetCmd(args[1:])
	case "clear":
		skillSourceClearCmd(args[1:])
	default:
		fail("unknown skill source subcommand %q (want set|clear)", args[0])
	}
}

// skillInstallCmd implements `flowloop skill install` — install a NEW skill
// from a remote repository in one command: clone, register, bind the source
// and record the remote SHA as the sync baseline. Errors if a skill with
// the same name already exists.
func skillInstallCmd(args []string) {
	fs := flag.NewFlagSet("skill install", flag.ExitOnError)
	repo := fs.String("repo", "", "repository URL (https://host/owner/repo[.git])")
	path := fs.String("path", "", "subdirectory inside the repository holding the skill")
	branch := fs.String("branch", "", "branch to install from (default main)")
	token := fs.String("token", "", "git host token for private repositories")
	provider := fs.String("provider", "", "repo platform: github|gitcode (default: infer from URL host)")
	fs.Parse(args)

	if *repo == "" {
		fail("usage: flowloop skill install --repo <url> [--path p] [--branch b] [--provider github|gitcode] [--token t]")
	}
	base := serverURL()

	reqBody := map[string]any{"repo_url": *repo}
	if *path != "" {
		reqBody["path"] = *path
	}
	if *branch != "" {
		reqBody["branch"] = *branch
	}
	if *token != "" {
		reqBody["token"] = *token
	}
	if *provider != "" {
		reqBody["provider"] = *provider
	}
	result, err := postJSONMap(base+"/skills/install-from-repo", reqBody)
	if err != nil {
		fail("install skill from repo: %v", err)
	}
	printJSONMap(result)
}

// skillIDByName resolves a skill name to its platform ID from the
// GET /skills listing (id, name pairs in a JSON array).
func skillIDByName(skillList map[string]any, name string) (string, bool) {
	items, ok := skillList["skills"].([]any)
	if !ok {
		return "", false
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			id, _ := m["id"].(string)
			return id, true
		}
	}
	return "", false
}

// parseFlagsReordered parses args where flags may appear BEFORE or AFTER
// the first positional argument. Go's flag package stops at the first
// non-flag argument, so the documented usage `skill pr <name> --title t`
// would silently drop every flag after the name; this helper collects the
// leading positional(s) and re-parses the remaining tail. Returns the
// positional arguments.
func parseFlagsReordered(fs *flag.FlagSet, args []string) []string {
	fs.Parse(args)
	var positional []string
	for {
		rest := fs.Args()
		if len(rest) <= 1 || strings.HasPrefix(rest[0], "-") {
			return append(positional, rest...)
		}
		// rest[0] is a positional followed by more arguments: the flag
		// package stopped there, so take it and re-parse the tail.
		positional = append(positional, rest[0])
		fs.Parse(rest[1:])
	}
}

// printJSONMap is the agent-facing output format: a single compact JSON
// object on stdout (encoding/json sorts map keys — a stable contract for
// agents parsing the output).
func printJSONMap(m map[string]any) {
	b, err := json.Marshal(m)
	if err != nil {
		fail("encode output: %v", err)
	}
	fmt.Println(string(b))
}

// getJSONMap fetches a JSON document from url as a map. GET /skills returns
// a BARE ARRAY (the handler writes the skill list directly), so an array
// response is wrapped as {"skills": [...]} to keep callers map-shaped. The
// body is fetched ONCE and both shapes are decoded from the same bytes —
// every skill sub-command starts with this list, a double fetch would double
// every command's latency.
func getJSONMap(url string) (map[string]any, error) {
	resp, err := httpDoJSON("GET", url, nil)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var m map[string]any
	objErr := json.Unmarshal(data, &m)
	if objErr == nil && m != nil {
		return m, nil
	}
	var list []any
	if arrErr := json.Unmarshal(data, &list); arrErr == nil {
		return map[string]any{"skills": list}, nil
	}
	return nil, fmt.Errorf("decode response: %w", objErr)
}

// getJSONTo fetches a JSON document from url and decodes it into out.
func getJSONTo(url string, out any) error {
	return getJSON(url, out)
}

// putJSONMap PUTs body as JSON to url and decodes the response into a map.
func putJSONMap(url string, body map[string]any) (map[string]any, error) {
	resp, err := httpDoJSON("PUT", url, body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := jsonDecode(resp.Body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// deleteJSONMap DELETEs url and decodes the response into a map.
func deleteJSONMap(url string) (map[string]any, error) {
	resp, err := httpDoJSON("DELETE", url, nil)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := jsonDecode(resp.Body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// postJSONMap POSTs body as JSON to url and decodes the response.
func postJSONMap(url string, body map[string]any) (map[string]any, error) {
	resp, err := httpDoJSON("POST", url, body)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := jsonDecode(resp.Body, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// httpDoJSON performs an HTTP request with a JSON body (nil body sends no
// payload) and returns the response with its body fully read.
func httpDoJSON(method, url string, body map[string]any) (*http.Response, error) {
	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal: %w", err)
		}
		payload = b
	}
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response: %w", method, url, err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, data)
	}
	return &http.Response{StatusCode: resp.StatusCode, Body: io.NopCloser(bytes.NewReader(data))}, nil
}

// jsonDecode decodes a JSON document from an io.Reader.
func jsonDecode(r io.Reader, out any) error {
	if err := json.NewDecoder(r).Decode(out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}
