package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sinkear/flowloop/internal/store"
	"github.com/Sinkear/flowloop/internal/zipx"
)

// ── fakes: no network / no git binary in these tests ──

type fakeRemoteGit struct {
	files         map[string]string
	sha           string
	commit        string
	fetchErr      error
	pushErr       error
	fetches       int
	pushes        int
	pushedFiles   map[string]string
	pushedBranch  string
	pushedMessage string
	pushedToken   string
	lsRemoteSha   string
	lsRemoteErr   error
	lsRemotes     int
}

func copyFiles(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (f *fakeRemoteGit) Fetch(_ context.Context, cfg remoteConfig) (*remoteState, error) {
	f.fetches++
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	return &remoteState{SHA: f.sha, Files: copyFiles(f.files)}, nil
}

func (f *fakeRemoteGit) Push(_ context.Context, cfg remoteConfig, files map[string]string, branch, message string) (string, error) {
	f.pushes++
	f.pushedFiles = copyFiles(files)
	f.pushedBranch = branch
	f.pushedMessage = message
	f.pushedToken = cfg.Token
	if f.pushErr != nil {
		return "", f.pushErr
	}
	return f.commit, nil
}

// RemoteSHA serves the lightweight Check probe: the branch head is f.sha
// by default; lsRemoteSha (when set) lets a test move the remote forward.
func (f *fakeRemoteGit) RemoteSHA(_ context.Context, _ remoteConfig) (string, error) {
	f.lsRemotes++
	if f.lsRemoteErr != nil {
		return "", f.lsRemoteErr
	}
	if f.lsRemoteSha != "" {
		return f.lsRemoteSha, nil
	}
	return f.sha, nil
}

type fakePullProvider struct {
	identity  string
	canPush   bool
	permErr   error
	prURL     string
	prNumber  int
	prErr     error
	permCalls int
	prCalls   int
	gotRepo   string
	gotTitle  string
	gotHead   string
	gotBase   string
	gotBody   string
}

func (p *fakePullProvider) CreatePull(_ context.Context, repo, title, head, base, body string) (string, int, error) {
	p.prCalls++
	p.gotRepo, p.gotTitle, p.gotHead, p.gotBase, p.gotBody = repo, title, head, base, body
	if p.prErr != nil {
		return "", 0, p.prErr
	}
	return p.prURL, p.prNumber, nil
}

func (p *fakePullProvider) CheckPushPermission(_ context.Context, repo string) (string, bool, error) {
	p.permCalls++
	p.gotRepo = repo
	if p.permErr != nil {
		return p.identity, false, p.permErr
	}
	return p.identity, p.canPush, nil
}

// remoteTestHarness builds an isolated skill service (temp FLOWLOOP_HOME)
// with one skill and fake git/provider surfaces.
func remoteTestHarness(t *testing.T, local map[string]string) (*SkillService, *Skill, *fakeRemoteGit, *fakePullProvider) {
	t.Helper()
	t.Setenv("FLOWLOOP_HOME", t.TempDir())
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkillService(st)
	git := &fakeRemoteGit{sha: "remote-sha-1", commit: "commit-1", files: map[string]string{}}
	prov := &fakePullProvider{identity: "alice", canPush: true, prURL: "https://github.com/acme/skills/pull/7", prNumber: 7}
	svc.SetRemoteGit(git)
	svc.SetPullProviderFactory(func(name, token string) (PullProvider, error) {
		return prov, nil
	})
	zipData, err := zipx.Build(local)
	if err != nil {
		t.Fatal(err)
	}
	sk, err := svc.CreateFromZip(context.Background(), zipData)
	if err != nil {
		t.Fatalf("create skill: %v", err)
	}
	return svc, sk, git, prov
}

func mustSetSource(t *testing.T, svc *SkillService, id string, in SkillSourceInput) *Skill {
	t.Helper()
	sk, err := svc.SetSource(context.Background(), id, in)
	if err != nil {
		t.Fatalf("SetSource: %v", err)
	}
	return sk
}

// ── source configuration ──

func TestSkillSourceConfig(t *testing.T) {
	ctx := context.Background()
	svc, sk, _, _ := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nbody\n",
	})

	// Configure: provider derived from host, branch defaults to main.
	got := mustSetSource(t, svc, sk.ID, SkillSourceInput{
		RepoURL: "https://github.com/acme/skills",
		Token:   "secret-token",
	})
	if got.Source == nil {
		t.Fatal("source must be exposed after PUT")
	}
	if got.Source.Provider != "github" || got.Source.Branch != "main" || got.Source.Path != "" {
		t.Fatalf("derived source = %+v", got.Source)
	}
	if !got.Source.HasToken || got.Source.Token != "secret-token" {
		t.Fatalf("token config = has_token=%v token=%q", got.Source.HasToken, got.Source.Token)
	}

	// The serialized skill never carries the token; it exposes has_token.
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"token"`) || strings.Contains(string(raw), "secret-token") {
		t.Fatalf("skill JSON leaks the token: %s", raw)
	}
	if !strings.Contains(string(raw), `"has_token":true`) {
		t.Fatalf("skill JSON must expose has_token: %s", raw)
	}

	// Service list exposes the same source object.
	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 || list[0].Source == nil || list[0].Source.RepoURL != "https://github.com/acme/skills" {
		t.Fatalf("List = %+v, %v", list, err)
	}

	// Prior sync bookkeeping survives a re-PUT with an empty token (token
	// kept) on the SAME repo.
	if err := svc.markSynced(ctx, sk.ID, "pulled", "abc123", "digest-1"); err != nil {
		t.Fatal(err)
	}
	got = mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills"})
	if got.Source.Token != "secret-token" || got.Source.LastSyncAction != "pulled" || got.Source.LastRemoteSHA != "abc123" {
		t.Fatalf("same-repo update = %+v", got.Source)
	}
	if got.Source.LastSyncDigest != "digest-1" {
		t.Fatalf("same-repo update must preserve the sync digest, got %q", got.Source.LastSyncDigest)
	}

	// Same repo but a different path targets a different tree: the recorded
	// baseline would misattribute later compares, so it resets too.
	got = mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Path: "skills/other"})
	if got.Source.LastSyncAction != "" || got.Source.LastRemoteSHA != "" || got.Source.LastSyncedAt != "" || got.Source.LastSyncDigest != "" {
		t.Fatalf("same-repo different-path update = %+v", got.Source)
	}

	// A different repo resets the sync bookkeeping and re-derives the host.
	got = mustSetSource(t, svc, sk.ID, SkillSourceInput{
		RepoURL: "https://gitcode.com/acme/other",
		Token:   "t2",
	})
	if got.Source.Provider != "gitcode" || got.Source.LastSyncAction != "" || got.Source.LastRemoteSHA != "" || got.Source.LastSyncedAt != "" {
		t.Fatalf("different-repo update = %+v", got.Source)
	}

	// Validation: bad URL / unknown provider.
	if _, err := svc.SetSource(ctx, sk.ID, SkillSourceInput{RepoURL: "ftp://acme/skills"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad scheme err = %v, want ErrValidation", err)
	}
	if _, err := svc.SetSource(ctx, sk.ID, SkillSourceInput{RepoURL: "https://example.com/acme/skills", Provider: "bitbucket"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown provider err = %v, want ErrValidation", err)
	}

	// Delete clears the binding and the token; source is omitted from JSON.
	got, err = svc.ClearSource(ctx, sk.ID)
	if err != nil {
		t.Fatalf("ClearSource: %v", err)
	}
	if got.Source != nil {
		t.Fatalf("source after DELETE = %+v, want nil", got.Source)
	}
	raw, _ = json.Marshal(got)
	if strings.Contains(string(raw), `"source"`) {
		t.Fatalf("cleared skill JSON still has source: %s", raw)
	}
	var token string
	if err := svc.st.DB().QueryRowContext(ctx, `SELECT repo_token FROM skill WHERE id=?`, sk.ID).Scan(&token); err != nil || token != "" {
		t.Fatalf("stored token after clear = %q, %v", token, err)
	}
}

// ── compare ──

// TestDiffSkillFilesPullViewpoint pins the contract/frontend semantics:
// added = remote-only, removed = local-only, modified = both differ;
// local changes drive local_ahead, remote changes drive remote_ahead.
func TestDiffSkillFilesPullViewpoint(t *testing.T) {
	entries, counts := diffSkillFiles(
		map[string]string{"SKILL.md": "local", "local-only.md": "x"},
		map[string]string{"SKILL.md": "remote", "remote-only.md": "y"},
	)
	byPath := map[string]SyncEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if byPath["remote-only.md"].Status != "added" || byPath["remote-only.md"].RemoteSHA256 == "" || byPath["remote-only.md"].LocalSHA256 != "" {
		t.Fatalf("remote-only entry = %+v", byPath["remote-only.md"])
	}
	if byPath["local-only.md"].Status != "removed" || byPath["local-only.md"].LocalSHA256 == "" || byPath["local-only.md"].RemoteSHA256 != "" {
		t.Fatalf("local-only entry = %+v", byPath["local-only.md"])
	}
	if byPath["SKILL.md"].Status != "modified" || byPath["SKILL.md"].LocalSHA256 == byPath["SKILL.md"].RemoteSHA256 {
		t.Fatalf("modified entry = %+v", byPath["SKILL.md"])
	}
	if counts.Added != 1 || counts.Removed != 1 || counts.Modified != 1 || counts.Unchanged != 0 {
		t.Fatalf("counts = %+v", counts)
	}
	// These pin the baseline-less heuristic (used only for bindings that
	// were never synced): added = remote-only drives remote_ahead,
	// removed/modified = local changes drive local_ahead.
	if got := syncCountsHeuristic(SyncCounts{Removed: 1, Modified: 1}); got != "local_ahead" {
		t.Fatalf("local-only state = %q", got)
	}
	if got := syncCountsHeuristic(SyncCounts{Added: 1}); got != "remote_ahead" {
		t.Fatalf("remote-only state = %q", got)
	}
	if got := syncCountsHeuristic(SyncCounts{Added: 1, Removed: 1}); got != "diverged" {
		t.Fatalf("diverged state = %q", got)
	}
	if got := syncCountsHeuristic(SyncCounts{}); got != "in_sync" {
		t.Fatalf("in-sync state = %q", got)
	}
}

func TestSkillCompareStates(t *testing.T) {
	const skillMD = "---\nname: sync-skill\ndescription: demo\n---\nbody\n"
	ctx := context.Background()

	cases := []struct {
		name       string
		local      map[string]string
		remote     map[string]string
		wantState  string
		wantCounts SyncCounts
		wantStatus map[string]string
	}{
		{
			name:       "in sync",
			local:      map[string]string{"SKILL.md": skillMD},
			remote:     map[string]string{"SKILL.md": skillMD, "package.zip": "ignored"},
			wantState:  "in_sync",
			wantCounts: SyncCounts{Unchanged: 1},
		},
		{
			name:       "local modified SKILL.md",
			local:      map[string]string{"SKILL.md": skillMD + "local edit\n"},
			remote:     map[string]string{"SKILL.md": skillMD},
			wantState:  "local_ahead",
			wantCounts: SyncCounts{Modified: 1},
			wantStatus: map[string]string{"SKILL.md": "modified"},
		},
		{
			name:       "local-only extra file",
			local:      map[string]string{"SKILL.md": skillMD, "local-only.md": "mine"},
			remote:     map[string]string{"SKILL.md": skillMD},
			wantState:  "local_ahead",
			wantCounts: SyncCounts{Removed: 1, Unchanged: 1},
			wantStatus: map[string]string{"local-only.md": "removed"},
		},
		{
			name:       "remote-only file",
			local:      map[string]string{"SKILL.md": skillMD},
			remote:     map[string]string{"SKILL.md": skillMD, "remote-only.md": "theirs"},
			wantState:  "remote_ahead",
			wantCounts: SyncCounts{Added: 1, Unchanged: 1},
			wantStatus: map[string]string{"remote-only.md": "added"},
		},
		{
			name:       "diverged",
			local:      map[string]string{"SKILL.md": skillMD + "local edit\n", "local-only.md": "mine"},
			remote:     map[string]string{"SKILL.md": skillMD, "remote-only.md": "theirs"},
			wantState:  "diverged",
			wantCounts: SyncCounts{Added: 1, Removed: 1, Modified: 1},
			wantStatus: map[string]string{"SKILL.md": "modified", "local-only.md": "removed", "remote-only.md": "added"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, sk, git, prov := remoteTestHarness(t, c.local)
			git.files = c.remote
			prov.canPush = true
			mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

			cmp, err := svc.Compare(ctx, sk.ID)
			if err != nil {
				t.Fatalf("Compare: %v", err)
			}
			if cmp.State != c.wantState || cmp.Counts != c.wantCounts {
				t.Fatalf("Compare state/counts = %q %+v, want %q %+v", cmp.State, cmp.Counts, c.wantState, c.wantCounts)
			}
			if cmp.Identity != "alice" || !cmp.CanPush {
				t.Fatalf("identity/can_push = %q %v", cmp.Identity, cmp.CanPush)
			}
			if cmp.BaseBranch != "main" || cmp.RemoteSHA != "remote-sha-1" || cmp.CheckedAt == "" {
				t.Fatalf("compare metadata = %+v", cmp)
			}
			statuses := map[string]string{}
			for _, e := range cmp.Entries {
				statuses[e.Path] = e.Status
				if e.Path == "package.zip" {
					t.Fatalf("package.zip must be excluded from the diff: %+v", cmp.Entries)
				}
			}
			for p, want := range c.wantStatus {
				if statuses[p] != want {
					t.Fatalf("entry %q status = %q, want %q (entries %+v)", p, statuses[p], want, cmp.Entries)
				}
			}
			if len(statuses) != len(c.wantStatus) {
				t.Fatalf("entries = %+v, want %d entries", cmp.Entries, len(c.wantStatus))
			}
		})
	}
}

// TestSkillCompareReportsNoPermission: the GET compare is read-only and
// reports can_push=false instead of failing.
func TestSkillCompareReportsNoPermission(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\n"})
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\n"}
	prov.canPush = false
	prov.identity = "viewer"
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatalf("Compare must not fail on missing push permission: %v", err)
	}
	if cmp.CanPush || cmp.Identity != "viewer" || cmp.State != "in_sync" {
		t.Fatalf("compare = %+v", cmp)
	}
}

// ── pull ──

func TestSkillSyncPull(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{
		"SKILL.md":  "---\nname: sync-skill\ndescription: demo\n---\nold\n",
		"stale.txt": "remove me",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok", Path: "skills/demo"})
	prov.canPush = true

	git.files = map[string]string{
		"SKILL.md":      "---\nname: sync-skill\ndescription: demo\n---\nnew remote body\n",
		"sub/helper.md": "helper\n",
		"package.zip":   "remote artifact must be ignored",
	}

	out, err := svc.Sync(ctx, sk.ID, "pull")
	if err != nil {
		t.Fatalf("Sync pull: %v", err)
	}
	if out.Action != "pulled" || out.RemoteSHA != "remote-sha-1" || out.Branch != "main" {
		t.Fatalf("pull result = %+v", out)
	}
	// Pull no longer requires push permission, so the result carries no
	// identity (the old two-way sync stamped the token identity here).
	if out.Identity != "" {
		t.Fatalf("pull result identity = %q, want empty", out.Identity)
	}

	files, err := svc.Files(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["stale.txt"]; ok {
		t.Fatalf("pull must delete local-only files: %v", files)
	}
	if files["SKILL.md"] != git.files["SKILL.md"] || files["sub/helper.md"] != "helper\n" {
		t.Fatalf("pull must replace local content: %v", files)
	}

	// package.zip is regenerated from the fetched files (not copied from the
	// remote) and must contain the new layout.
	archive, err := svc.PackageZip(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := zipxEntries(archive)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range entries {
		got[e] = true
	}
	if !got["SKILL.md"] || !got["sub/helper.md"] || got["package.zip"] || got["stale.txt"] {
		t.Fatalf("package.zip entries after pull = %v", entries)
	}

	gotSkill, err := svc.Get(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSkill.Source.LastSyncAction != "pulled" || gotSkill.Source.LastRemoteSHA != "remote-sha-1" || gotSkill.Source.LastSyncedAt == "" {
		t.Fatalf("last_* after pull = %+v", gotSkill.Source)
	}
	if git.pushes != 0 {
		t.Fatalf("pull must not push, pushes=%d", git.pushes)
	}
}

// ── pull permission semantics (push was removed) ──

func TestSkillSyncPullDoesNotRequirePushPermission(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}
	// A viewer token (no push rights) must still be able to PULL — reading
	// the remote is the pull-only sync flow's core operation.
	prov.canPush = false
	prov.identity = "viewer"
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

	out, err := svc.Sync(ctx, sk.ID, "pull")
	if err != nil {
		t.Fatalf("pull err = %v, want nil (pull needs no push permission)", err)
	}
	if out.Action != "pulled" {
		t.Fatalf("pull action = %q", out.Action)
	}
	if git.pushes != 0 || git.fetches == 0 {
		t.Fatalf("pull must fetch without pushing (pushes=%d fetches=%d)", git.pushes, git.fetches)
	}
}

func TestSkillSyncPullWithoutToken(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills"}) // no token

	// Public repos are pullable without a token: the pull must go through.
	out, err := svc.Sync(ctx, sk.ID, "pull")
	if err != nil {
		t.Fatalf("tokenless pull err = %v, want nil (public repos need no token)", err)
	}
	if out.Action != "pulled" || git.fetches == 0 || git.pushes != 0 {
		t.Fatalf("pull = %+v (fetches=%d pushes=%d)", out, git.fetches, git.pushes)
	}
}

func TestSkillSyncPullFetchFailureHintsToken(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills"}) // no token
	git.fetchErr = errors.New("could not read Username: terminal prompts disabled")

	_, err := svc.Sync(ctx, sk.ID, "pull")
	if err == nil {
		t.Fatal("tokenless pull against an auth-failing remote must fail")
	}
	// The raw git error stays, plus a factual note pointing at the token.
	if !strings.Contains(err.Error(), "could not read Username") {
		t.Fatalf("error should keep the git cause: %v", err)
	}
	if !strings.Contains(err.Error(), "no git token is configured") {
		t.Fatalf("error should hint the missing token: %v", err)
	}

	// With a token configured the note is not appended (a wrong token is a
	// different problem and the hint would mislead).
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	_, err = svc.Sync(ctx, sk.ID, "pull")
	if err == nil || strings.Contains(err.Error(), "no git token is configured") {
		t.Fatalf("tokened pull error should not carry the tokenless hint: %v", err)
	}
}

func TestSkillSyncPushRemoved(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, _ := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nold remote\n"}

	// Direct push is REMOVED: the API must reject it without touching git.
	_, err := svc.Sync(ctx, sk.ID, "push")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("push err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "pull-request") {
		t.Fatalf("push removal error should point at the PR flow: %v", err)
	}
	if git.pushes != 0 || git.fetches != 0 {
		t.Fatalf("removed push must not touch git (pushes=%d fetches=%d)", git.pushes, git.fetches)
	}

	// Unknown direction is still a validation error.
	if _, err := svc.Sync(ctx, sk.ID, "sideways"); !errors.Is(err, ErrValidation) {
		t.Fatalf("bad direction err = %v, want ErrValidation", err)
	}
}

// ── pull request ──

func TestSkillPullRequest(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{
		"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n",
	})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true
	prov.prURL = "https://github.com/acme/skills/pull/7"
	prov.prNumber = 7
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nold remote\n"}

	out, err := svc.PullRequest(ctx, sk.ID, "Update skill", "why")
	if err != nil {
		t.Fatalf("PullRequest: %v", err)
	}
	if out.PRURL != prov.prURL || out.PRNumber != 7 || out.Base != "main" || out.Identity != "alice" || out.Commit != "commit-1" {
		t.Fatalf("PR result = %+v", out)
	}
	wantBranch := "flowloop/skill-" + firstID8(sk.ID) + "-"
	if !strings.HasPrefix(out.Branch, wantBranch) {
		t.Fatalf("PR branch = %q, want prefix %q", out.Branch, wantBranch)
	}
	if git.pushes != 1 || git.pushedBranch != out.Branch {
		t.Fatalf("PR push = %d branch=%q (want %q)", git.pushes, git.pushedBranch, out.Branch)
	}
	if prov.prCalls != 1 || prov.gotRepo != "acme/skills" || prov.gotHead != out.Branch ||
		prov.gotBase != "main" || prov.gotTitle != "Update skill" || prov.gotBody != "why" {
		t.Fatalf("CreatePull args = repo=%q title=%q head=%q base=%q body=%q", prov.gotRepo, prov.gotTitle, prov.gotHead, prov.gotBase, prov.gotBody)
	}
	gotSkill, err := svc.Get(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotSkill.Source.LastSyncAction != "pr" || gotSkill.Source.LastRemoteSHA != "commit-1" {
		t.Fatalf("last_* after PR = %+v", gotSkill.Source)
	}

	// No changes → validation error, no branch push / PR call.
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\n---\nlocal change\n"}
	if _, err := svc.PullRequest(ctx, sk.ID, "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("no-change PR err = %v, want ErrValidation", err)
	}
	if git.pushes != 1 || prov.prCalls != 1 {
		t.Fatalf("no-change PR contacted git/provider: pushes=%d prCalls=%d", git.pushes, prov.prCalls)
	}
}

// TestSkillPullRequestForbidden: PR submission is gated by the same push
// permission check as sync.
func TestSkillPullRequestForbidden(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = false
	prov.identity = "viewer"
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}

	_, err := svc.PullRequest(ctx, sk.ID, "", "")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("PR err = %v, want ErrForbidden", err)
	}
	if git.pushes != 0 || prov.prCalls != 0 {
		t.Fatalf("forbidden PR must not push/open: pushes=%d prCalls=%d", git.pushes, prov.prCalls)
	}
}

// TestSkillSyncUnconfigured: operations on a local-only skill are validation
// errors (no remote to talk to).
func TestSkillSyncUnconfigured(t *testing.T) {
	ctx := context.Background()
	svc, sk, _, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\n"})
	if _, err := svc.Compare(ctx, sk.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("Compare err = %v, want ErrValidation", err)
	}
	if _, err := svc.Sync(ctx, sk.ID, "pull"); !errors.Is(err, ErrValidation) {
		t.Fatalf("Sync err = %v, want ErrValidation", err)
	}
	if _, err := svc.PullRequest(ctx, sk.ID, "", ""); !errors.Is(err, ErrValidation) {
		t.Fatalf("PullRequest err = %v, want ErrValidation", err)
	}
}

// TestIgnoredSkillFile: local build artifacts (package.zip, Python bytecode
// caches, OS metadata) and git metadata never sync; ordinary files do.
func TestIgnoredSkillFile(t *testing.T) {
	ignored := []string{
		"package.zip",
		"./package.zip",
		".git/config",
		"scripts/__pycache__/base_cases.cpython-312.pyc",
		"nested/__pycache__/mod.pyc",
		"cache.pyo",
		".DS_Store",
		"docs/Thumbs.db",
	}
	for _, p := range ignored {
		if !ignoredSkillFile(p) {
			t.Fatalf("ignoredSkillFile(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"SKILL.md", "scripts/run.py", "docs/notes.md", ".gitkeep"} {
		if ignoredSkillFile(p) {
			t.Fatalf("ignoredSkillFile(%q) = true, want false", p)
		}
	}
}

// The git error path must never echo the credential: the command line in the
// error message contains the clone URL with the embedded token, and that
// string reaches the HTTP API response and the daemon log.
func TestGitCLIErrorRedactsToken(t *testing.T) {
	_, err := (&gitCLI{}).git(context.Background(), "",
		"clone", "https://oauth2:supersecret-token@127.0.0.1:1/skills", t.TempDir())
	if err == nil {
		t.Fatal("expected clone failure")
	}
	msg := err.Error()
	if strings.Contains(msg, "supersecret-token") {
		t.Fatalf("token leaked into error: %s", msg)
	}
	if !strings.Contains(msg, "127.0.0.1:1/skills") {
		t.Fatalf("sanitised error should keep the repo URL: %s", msg)
	}
}

// TestSkillCompareToleratesMissingLocalFiles: a skill row can outlive its
// files (runs/skills wiped or a DB restored without runs/). The compare must
// still show the remote diff — flagged local_missing — so the UI can offer the
// pull that restores the local copy, instead of failing with "load local skill
// files: lstat ...: no such file".
func TestSkillCompareToleratesMissingLocalFiles(t *testing.T) {
	const skillMD = "---\nname: restore-skill\ndescription: demo\n---\nbody\n"
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": skillMD})
	git.files = map[string]string{"SKILL.md": skillMD, "helper.md": "helper\n"}
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true

	if err := os.RemoveAll(skillDir(sk.ID)); err != nil {
		t.Fatalf("remove local skill dir: %v", err)
	}

	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatalf("Compare with missing local files must not fail: %v", err)
	}
	if !cmp.LocalMissing {
		t.Fatalf("LocalMissing = false, want true (%+v)", cmp)
	}
	if cmp.State != "remote_ahead" || cmp.Counts.Added != 2 || cmp.Counts.Removed != 0 {
		t.Fatalf("state/counts = %q %+v, want remote_ahead added=2", cmp.State, cmp.Counts)
	}

	if _, err := svc.Sync(ctx, sk.ID, "pull"); err != nil {
		t.Fatalf("pull must restore missing local files: %v", err)
	}
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatalf("Compare after restore: %v", err)
	}
	if cmp.LocalMissing || cmp.State != "in_sync" {
		t.Fatalf("after pull: local_missing=%v state=%q, want in_sync", cmp.LocalMissing, cmp.State)
	}
}

// ── sync-state attribution (regression) ──
//
// The counts-only heuristic read a modified-only diff after a pull as
// "local_ahead" even when the local copy was untouched — the reported bug:
// a skill pulled 2 days ago, then a PR merged on the remote (all files
// modified, nothing added/removed), showed 本地有新改动 and nudged a push
// of stale content. These tests pin attribution against the last-sync
// baseline: WHO moved since the last sync, not just THAT the sides differ.

// seedPulled binds the source and pulls the given remote content once, so
// the skill starts from a synced baseline.
func seedPulled(t *testing.T, svc *SkillService, sk *Skill, git *fakeRemoteGit, prov *fakePullProvider, files map[string]string) {
	t.Helper()
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true
	git.files = copyFiles(files)
	if _, err := svc.Sync(context.Background(), sk.ID, "pull"); err != nil {
		t.Fatalf("seed pull: %v", err)
	}
}

// TestSkillCompareAfterPullRemoteMoves: THE bug. Remote merges a PR after
// the last pull; local is untouched. Diff shape: modified=2, added=0,
// removed=0 — exactly the shape the old heuristic misread as local_ahead.
func TestSkillCompareAfterPullRemoteMoves(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nseed\n"})
	seedPulled(t, svc, sk, git, prov, map[string]string{
		"SKILL.md":  "---\nname: sync-skill\ndescription: demo\n---\nv1\n",
		"helper.md": "h1\n",
	})

	// The pull recorded a digest baseline of the synced file set.
	var digest string
	if err := svc.st.DB().QueryRowContext(ctx, `SELECT last_sync_digest FROM skill WHERE id=?`, sk.ID).Scan(&digest); err != nil || digest == "" {
		t.Fatalf("last_sync_digest after pull = %q, %v", digest, err)
	}

	// Remote moves on (PR merged); nobody touched the local copy.
	git.sha = "db39ab33"
	git.files = map[string]string{
		"SKILL.md":  "---\nname: sync-skill\ndescription: demo\n---\nv2\n",
		"helper.md": "h2\n",
	}
	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "remote_ahead" {
		t.Fatalf("state = %q, want remote_ahead (local untouched; counts %+v)", cmp.State, cmp.Counts)
	}
	if cmp.Counts.Modified != 2 || cmp.Counts.Added != 0 || cmp.Counts.Removed != 0 {
		t.Fatalf("counts = %+v, want modified=2 added=0 removed=0", cmp.Counts)
	}

	// Catching up settles the badge back to in_sync.
	if _, err := svc.Sync(ctx, sk.ID, "pull"); err != nil {
		t.Fatalf("catch-up pull: %v", err)
	}
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "in_sync" {
		t.Fatalf("state after catch-up pull = %q, want in_sync", cmp.State)
	}
}

// TestSkillCompareAfterPullLocalEdits: remote stays at the synced
// baseline, the local copy is edited → the diff is local → local_ahead.
func TestSkillCompareAfterPullLocalEdits(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nseed\n"})
	seedPulled(t, svc, sk, git, prov, map[string]string{
		"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nv1\n",
	})

	if err := os.WriteFile(filepath.Join(skillDir(sk.ID), "SKILL.md"),
		[]byte("---\nname: sync-skill\ndescription: demo\n---\nlocal edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "local_ahead" {
		t.Fatalf("state = %q, want local_ahead (remote untouched; counts %+v)", cmp.State, cmp.Counts)
	}
}

// TestSkillCompareAfterPullBothMoved: both sides moved since the sync →
// diverged (a human decides).
func TestSkillCompareAfterPullBothMoved(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nseed\n"})
	seedPulled(t, svc, sk, git, prov, map[string]string{
		"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nv1\n",
	})

	if err := os.WriteFile(filepath.Join(skillDir(sk.ID), "SKILL.md"),
		[]byte("---\nname: sync-skill\ndescription: demo\n---\nlocal edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git.sha = "moved-on"
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nv2\n"}

	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "diverged" {
		t.Fatalf("state = %q, want diverged (both sides moved; counts %+v)", cmp.State, cmp.Counts)
	}
}

// TestSkillCompareLegacySHARows: rows synced by a daemon predating the
// digest column have no baseline fingerprint — only the synced SHA. An
// unmoved remote head attributes every diff to local edits (the old
// behavior); a moved head cannot be attributed → diverged, honestly, with
// both SHAs on the panel for the human. One pull upgrades the row to
// digest-backed attribution.
func TestSkillCompareLegacySHARows(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nlocal edit\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true

	// Simulate a row written by the pre-digest daemon.
	if _, err := svc.st.DB().ExecContext(ctx,
		`UPDATE skill SET last_sync_action='pulled', last_remote_sha='remote-sha-1', last_sync_digest='' WHERE id=?`, sk.ID); err != nil {
		t.Fatal(err)
	}

	// Remote head still at the synced SHA → the diff must be local edits.
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nremote v1\n"}
	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "local_ahead" {
		t.Fatalf("legacy same-SHA state = %q, want local_ahead (counts %+v)", cmp.State, cmp.Counts)
	}

	// Remote head moved → cannot attribute without a baseline → diverged.
	git.sha = "moved-sha"
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "diverged" {
		t.Fatalf("legacy moved-SHA state = %q, want diverged (counts %+v)", cmp.State, cmp.Counts)
	}
}

// TestSkillCompareAfterPullRequest: the PR baseline is the pre-PR remote
// base (the common ancestor). While the PR is pending the diff reads
// local_ahead — local moved beyond the ancestor; once main merges the PR
// the sides read in_sync; if main moves on afterwards the badge tracks
// remote_ahead, not a stale push nudge.
func TestSkillCompareAfterPullRequest(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, prov := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nlocal change\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	prov.canPush = true
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nold remote\n"}

	if _, err := svc.PullRequest(ctx, sk.ID, "Update skill", "why"); err != nil {
		t.Fatalf("PullRequest: %v", err)
	}

	// PR pending: main still at the pre-PR base; local sits on the branch.
	cmp, err := svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "local_ahead" {
		t.Fatalf("state with PR pending = %q, want local_ahead (counts %+v)", cmp.State, cmp.Counts)
	}

	// PR merged into main: sides agree again.
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nlocal change\n"}
	git.sha = "merged-sha"
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "in_sync" {
		t.Fatalf("state after merge = %q, want in_sync (counts %+v)", cmp.State, cmp.Counts)
	}

	// Main moves on past the merge while local rests. The recorded baseline
	// is still the PRE-PR base (Compare never mutates bookkeeping), so
	// neither side can be proven untouched relative to it → diverged: the
	// safe "human decides" state, never a wrong push nudge. The window
	// closes at the next pull, which refreshes the baseline.
	git.sha = "moved-past-merge"
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nremote v2\n"}
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "diverged" {
		t.Fatalf("state after main moves past merge, no intervening pull = %q, want diverged (counts %+v)", cmp.State, cmp.Counts)
	}

	// Catch up (pull the merged+moved main), then the same shape — remote
	// moves again, local rests — reads remote_ahead precisely.
	if _, err := svc.Sync(ctx, sk.ID, "pull"); err != nil {
		t.Fatalf("catch-up pull: %v", err)
	}
	git.sha = "moved-again"
	git.files = map[string]string{"SKILL.md": "---\nname: sync-skill\ndescription: demo\n---\nremote v3\n"}
	cmp, err = svc.Compare(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.State != "remote_ahead" {
		t.Fatalf("state after catch-up, remote moves on = %q, want remote_ahead (counts %+v)", cmp.State, cmp.Counts)
	}
}

// TestSyncDigestBasics pins the fingerprint contract: order-independent,
// content- and file-set-sensitive, and never the empty string (empty means
// "no baseline" — never synced — so the sentinel must not collide).
func TestSyncDigestBasics(t *testing.T) {
	base := map[string]string{"SKILL.md": "x", "sub/h.md": "y"}
	if syncDigest(base) != syncDigest(map[string]string{"sub/h.md": "y", "SKILL.md": "x"}) {
		t.Fatal("syncDigest must be insertion-order independent")
	}
	if syncDigest(base) == syncDigest(map[string]string{"SKILL.md": "x", "sub/h.md": "z"}) {
		t.Fatal("syncDigest must change with content")
	}
	if syncDigest(base) == syncDigest(map[string]string{"SKILL.md": "x", "sub/h.md": "y", "new.md": "n"}) {
		t.Fatal("syncDigest must change with the file set")
	}
	if got := syncDigest(map[string]string{}); got == "" {
		t.Fatal("syncDigest of the empty set must be non-empty (\"\" is the never-synced sentinel)")
	}
}

// ── lightweight check (sha-level update probe) ──

func TestSkillCheckStates(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})

	// Never synced → unknown, has_update=true (first pull is meaningful).
	chk, err := svc.Check(ctx, sk.ID)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if chk.State != "unknown" || !chk.HasUpdate || chk.RemoteSHA != "remote-sha-1" || chk.LastSHA != "" {
		t.Fatalf("never-synced check = %+v", chk)
	}
	if git.lsRemotes != 1 || git.fetches != 0 {
		t.Fatalf("check must ls-remote without cloning (lsRemotes=%d fetches=%d)", git.lsRemotes, git.fetches)
	}

	// Pull stamps the baseline; head unchanged → in_sync.
	git.files = map[string]string{"SKILL.md": "---\nname: s\n---\nremote\n"}
	if _, err := svc.Sync(ctx, sk.ID, "pull"); err != nil {
		t.Fatal(err)
	}
	chk, err = svc.Check(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chk.State != "in_sync" || chk.HasUpdate || chk.LastSHA != "remote-sha-1" {
		t.Fatalf("after-pull check = %+v", chk)
	}

	// Remote moves → remote_ahead with has_update.
	git.lsRemoteSha = "remote-sha-2"
	chk, err = svc.Check(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chk.State != "remote_ahead" || !chk.HasUpdate || chk.RemoteSHA != "remote-sha-2" {
		t.Fatalf("moved-remote check = %+v", chk)
	}
}

func TestSkillCheckRequiresSource(t *testing.T) {
	ctx := context.Background()
	svc, sk, _, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})

	if _, err := svc.Check(ctx, sk.ID); !errors.Is(err, ErrValidation) {
		t.Fatalf("sourceless check err = %v, want ErrValidation", err)
	}
}

func TestSkillCheckRemoteError(t *testing.T) {
	ctx := context.Background()
	svc, sk, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: s\n---\nlocal\n"})
	mustSetSource(t, svc, sk.ID, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"})
	git.lsRemoteErr = errors.New("network down")

	if _, err := svc.Check(ctx, sk.ID); err == nil || !strings.Contains(err.Error(), "check remote branch") {
		t.Fatalf("check err = %v, want wrapped probe failure", err)
	}
}

// ── install from repo ──

func TestSkillInstallFromRepo(t *testing.T) {
	ctx := context.Background()
	svc, _, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: seed\n---\nseed\n"})
	git.files = map[string]string{
		"SKILL.md":        "---\nname: installed-skill\ndescription: from repo\n---\nremote body\n",
		"references/x.md": "ref\n",
	}

	sk, err := svc.InstallFromRepo(ctx, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Path: "skills/demo", Token: "tok"})
	if err != nil {
		t.Fatalf("InstallFromRepo: %v", err)
	}
	if sk.Name != "installed-skill" || sk.Source == nil || sk.Source.RepoURL != "https://github.com/acme/skills" {
		t.Fatalf("installed skill = %+v", sk)
	}
	// Install stamps the baseline: the first check reports in_sync, not unknown.
	if sk.Source.LastSyncAction != "installed" || sk.Source.LastRemoteSHA != "remote-sha-1" || sk.Source.LastSyncDigest == "" {
		t.Fatalf("install baseline = %+v", sk.Source)
	}
	files, err := svc.Files(ctx, sk.ID)
	if err != nil {
		t.Fatal(err)
	}
	if files["SKILL.md"] != git.files["SKILL.md"] || files["references/x.md"] != "ref\n" {
		t.Fatalf("installed files = %v", files)
	}

	// Installing the same repo again now collides on the skill name.
	if _, err := svc.InstallFromRepo(ctx, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"}); err == nil {
		t.Fatal("duplicate install must fail on skill name")
	}
}

func TestSkillInstallFromRepoRequiresSKILLMD(t *testing.T) {
	ctx := context.Background()
	svc, _, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: seed\n---\nseed\n"})
	git.files = map[string]string{"docs/only.md": "no skill here"}

	if _, err := svc.InstallFromRepo(ctx, SkillSourceInput{RepoURL: "https://github.com/acme/skills", Token: "tok"}); !errors.Is(err, ErrValidation) {
		t.Fatalf("skillless repo install err = %v, want ErrValidation", err)
	}
}

func TestSkillInstallFromRepoTokenless(t *testing.T) {
	ctx := context.Background()
	svc, _, git, _ := remoteTestHarness(t, map[string]string{"SKILL.md": "---\nname: seed\n---\nseed\n"})
	git.files = map[string]string{"SKILL.md": "---\nname: s2\n---\nx\n"}

	// Public repos need no token: install goes through and stamps the baseline.
	sk, err := svc.InstallFromRepo(ctx, SkillSourceInput{RepoURL: "https://github.com/acme/skills"})
	if err != nil {
		t.Fatalf("tokenless install err = %v, want nil (public repos need no token)", err)
	}
	if git.fetches == 0 {
		t.Fatal("tokenless install must fetch")
	}
	if sk.Source == nil || sk.Source.LastSyncAction != "installed" {
		t.Fatalf("installed source = %+v", sk.Source)
	}
}

// ── builtin skill protection ──

// SetSource must refuse built-in skills: EnsureBuiltin rewrites their files
// from the embedded assets at every boot, so a binding would be silently
// reverted on restart while the recorded baseline still claims in_sync.
func TestSetSourceBuiltinRejected(t *testing.T) {
	ctx := context.Background()
	t.Setenv("FLOWLOOP_HOME", t.TempDir())
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSkillService(st)
	if _, err := SeedBuiltinSkills(ctx, svc); err != nil {
		t.Fatal(err)
	}

	_, err = svc.SetSource(ctx, BuiltinChatCreateID, SkillSourceInput{
		RepoURL: "https://github.com/acme/skills",
		Token:   "tok",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("builtin SetSource err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "built-in") {
		t.Fatalf("rejection should name the built-in constraint: %v", err)
	}

	// The rejection leaves no partial binding behind.
	sk, err := svc.Get(ctx, BuiltinChatCreateID)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Source != nil {
		t.Fatalf("rejected SetSource must not persist a source: %+v", sk.Source)
	}
}
