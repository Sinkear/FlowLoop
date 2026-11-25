// Package paths is the single source of truth for flowloop's filesystem
// layout.
//
// All durable state lives under one data root. By default that root is
// ~/.flowloop, but it can — and for production deployments SHOULD — be
// moved outside the release artifact with FLOWLOOP_HOME:
//
//	FLOWLOOP_HOME=/var/lib/flowloop ./bin/flowloop-daemon
//
// The release directory (bin/, web/) is stateless and can be replaced on every
// upgrade; only this root must survive. Keeping the root stable is what makes
// "replace the artifact, keep the agents/teams/config" safe.
//
// FLOWLOOP_HOME is read on every call (never cached) so tests that set HOME
// or FLOWLOOP_HOME with t.Setenv keep working.
//
// AdoptLegacyEnv and MigrateLegacyHome are the one-release bridge from the
// pre-rename OuroFlow layout: processes call them once at startup so an
// existing ~/.ouroflow root keeps serving under the new name.
package paths

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnvHome is the environment variable that overrides the data root.
const EnvHome = "FLOWLOOP_HOME"

// EnvDB optionally overrides just the SQLite file (useful for tests and for
// splitting the DB onto a different volume). Relative paths stay relative to
// the process working directory, matching the daemon's -db flag.
const EnvDB = "FLOWLOOP_DB"

// Home returns the state root: $FLOWLOOP_HOME when set, otherwise
// ~/.flowloop. A leading "~" in FLOWLOOP_HOME is expanded.
func Home() string {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		return expandUser(v)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".flowloop")
	}
	// Last-resort fallback: keep the process alive when HOME is unavailable
	// (containers). Operators should always set FLOWLOOP_HOME explicitly.
	return filepath.Join(os.TempDir(), "flowloop-home")
}

// EnsureHome creates the data root if needed.
func EnsureHome() (string, error) {
	home := Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return home, err
	}
	return home, nil
}

// legacyEnvPrefix is the pre-rename environment prefix. The renamed binaries
// prefer FLOWLOOP_* but still adopt OUROFLOW_* so shells, systemd units and CI
// scripts written for the old release keep working.
const legacyEnvPrefix = "OUROFLOW_"

// legacyRootDir is the pre-rename per-user data root (relative to $HOME).
const legacyRootDir = ".ouroflow"

// legacyStateFiles are the pre-rename state files, in the order they are
// renamed: the SQLite sidecars first so the database only appears under its
// new name once its write-ahead log is in place.
var legacyStateFiles = [][2]string{
	{"ouroflow.db-wal", "flowloop.db-wal"},
	{"ouroflow.db-shm", "flowloop.db-shm"},
	{"ouroflow.db", "flowloop.db"},
	{"ouroflow.db.manifest.json", "flowloop.db.manifest.json"},
	{"ouroflow-cli-state.json", "flowloop-cli-state.json"},
}

// AdoptLegacyEnv copies every OUROFLOW_<NAME> to FLOWLOOP_<NAME> when the new
// variable is unset. Call once per process, before anything reads the layout.
func AdoptLegacyEnv() {
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, legacyEnvPrefix) {
			continue
		}
		renamed := "FLOWLOOP_" + strings.TrimPrefix(key, legacyEnvPrefix)
		// An empty new variable is treated as unset: our own readers treat ""
		// the same way, so an exported-but-blank FLOWLOOP_X must not shadow the
		// legacy value the operator actually wants picked up.
		if strings.TrimSpace(os.Getenv(renamed)) == "" {
			_ = os.Setenv(renamed, value)
		}
	}
}

// MigrateLegacyHome is the one-time upgrade step away from the OuroFlow name.
// It renames the default data root ~/.ouroflow to ~/.flowloop, then renames the
// state files inside the effective root (flowloop.db and the CLI link state)
// wherever that root lives — including an operator-configured FLOWLOOP_HOME
// that still holds the old file names.
//
// When ~/.flowloop already exists (a half-set-up env, a stray process that
// created the dir), the legacy entries are MERGED into it instead of being
// ignored: an empty new root must never shadow a populated legacy one, or the
// upgrade would silently start from an empty database. Nothing is ever
// overwritten — a name that exists on both sides stays in the old root for the
// operator — and an explicitly configured home is never moved behind their
// back. A missing legacy root is a no-op, so a fresh install pays nothing.
func MigrateLegacyHome() error {
	// Only the default root is moved. An explicit FLOWLOOP_HOME (or a legacy
	// OUROFLOW_HOME, already adopted into it) is an operator choice: the file
	// names inside are upgraded below, the directory itself is left alone.
	explicit := strings.TrimSpace(os.Getenv(EnvHome)) != ""
	if !explicit {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			oldRoot := filepath.Join(home, legacyRootDir)
			newRoot := filepath.Join(home, ".flowloop")
			if _, statErr := os.Stat(oldRoot); statErr == nil {
				if info, statErr := os.Stat(newRoot); statErr == nil && info.IsDir() {
					if err := mergeLegacyRoot(oldRoot, newRoot); err != nil {
						return err
					}
				} else if err := os.Rename(oldRoot, newRoot); err != nil {
					return fmt.Errorf("rename legacy data root: %w", err)
				}
			}
		}
	}

	root := Home()
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil
	}
	return renameLegacyStateFiles(root)
}

// mergeLegacyRoot moves every top-level entry of oldRoot into newRoot that the
// new root does not already have, then removes oldRoot once it is empty.
// Colliding names are left in place: picking a winner automatically would be
// data loss in one direction or the other, so the operator decides.
func mergeLegacyRoot(oldRoot, newRoot string) error {
	entries, err := os.ReadDir(oldRoot)
	if err != nil {
		return fmt.Errorf("read legacy data root: %w", err)
	}
	for _, entry := range entries {
		from := filepath.Join(oldRoot, entry.Name())
		to := filepath.Join(newRoot, entry.Name())
		if _, statErr := os.Stat(to); statErr == nil {
			continue // both sides have it: keep both, leave the old copy behind
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("merge legacy entry %s: %w", entry.Name(), err)
		}
	}
	if rest, err := os.ReadDir(oldRoot); err == nil && len(rest) == 0 {
		_ = os.Remove(oldRoot)
	}
	return nil
}

// renameLegacyStateFiles upgrades the state file names inside root, skipping
// any pair whose new name already exists.
func renameLegacyStateFiles(root string) error {
	for _, pair := range legacyStateFiles {
		from := filepath.Join(root, pair[0])
		to := filepath.Join(root, pair[1])
		if _, err := os.Stat(from); err != nil {
			continue
		}
		if _, err := os.Stat(to); err == nil {
			continue // never clobber the renamed file
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("rename legacy state file %s: %w", pair[0], err)
		}
	}
	return nil
}

func expandUser(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// DB is the SQLite database file.
func DB() string {
	if v := strings.TrimSpace(os.Getenv(EnvDB)); v != "" {
		return expandUser(v)
	}
	return filepath.Join(Home(), "flowloop.db")
}

// Runs is the root of run/domain filesystem state.
func Runs() string { return filepath.Join(Home(), "runs") }

// Repos is the daemon's per-domain bare mirror root.
func Repos() string { return filepath.Join(Runs(), "repos") }

// RunWorktree is one run's execution worktree.
func RunWorktree(runID string) string { return filepath.Join(Runs(), runID) }

// GoalWorktree is the ephemeral deliver worktree for a goal.
func GoalWorktree(goalID string) string { return filepath.Join(Runs(), "deliver-"+goalID) }

// ScratchDomain is a scratch domain's persistent project root.
func ScratchDomain(name string) string { return filepath.Join(Runs(), "scratch", name) }

// ScratchGoal is a scratch goal's durable project directory.
func ScratchGoal(domainName, goalID string) string {
	return filepath.Join(ScratchDomain(domainName), "goals", goalID)
}

// Proc is a processor run's scratch directory.
func Proc(runID string) string { return filepath.Join(Runs(), "proc", runID) }

// GoalAgent is the machine-side persistent worktree for a writable run,
// keyed by (goal, agent) or (goal, sub-goal, agent).
func GoalAgent(goalID, subGoalID, agentID string) string {
	if subGoalID != "" {
		return filepath.Join(Runs(), "goal", goalID, "sg", subGoalID, agentID)
	}
	return filepath.Join(Runs(), "goal", goalID, agentID)
}

// Digest is where collected daily-digest articles land.
func Digest() string { return filepath.Join(Home(), "digest") }

// DaemonLog is the daemon's rotated log file.
func DaemonLog() string { return filepath.Join(Home(), "daemon.log") }

// CLILog is the machine-side CLI log.
func CLILog() string { return filepath.Join(Home(), "cli.log") }

// CLIState is the machine identity / last connection state.
func CLIState() string { return filepath.Join(Home(), "flowloop-cli-state.json") }

// AgentsConfig is the optional agents.yaml probe override.
func AgentsConfig() string { return filepath.Join(Home(), "agents.yaml") }

// PendingReports holds terminal run reports that could not be delivered.
func PendingReports() string { return filepath.Join(Home(), "pending-reports.json") }

// AgentProfileDir holds a pushed AGENTS.md per agent.
func AgentProfileDir(agentID string) string { return filepath.Join(Home(), "agents", agentID) }

// MachineSkillRoot holds pushed skill packages per agent on the machine.
func MachineSkillRoot(agentID string) string { return filepath.Join(Home(), "skills", agentID) }

// ChatDir is the stable, cwd-keyed chat session directory per agent.
func ChatDir(agentID string) string { return filepath.Join(Home(), "chat", agentID) }

// BinDir holds the `flowloop` shim injected into agent PATH.
func BinDir() string { return filepath.Join(Home(), "bin") }

// RepoDir is the machine-side bare repo mirror for a domain. The daemon keeps
// its own copy under Runs/repos; a remote machine has this one.
func RepoDir(domainID string) string { return filepath.Join(Home(), "repos", domainID) }

// HookRuns is the root for hook execution scratch dirs.
func HookRuns() string { return filepath.Join(Runs(), "hooks") }

// HookRunDir is the per-execution working directory of one hook run: the
// script is staged here as `script` and runs with this as cwd. Kept after the
// run for output forensics; safe to prune by age.
func HookRunDir(runID string) string { return filepath.Join(HookRuns(), runID) }

// Layout is a JSON-friendly snapshot of every top-level path, used by the
// `paths` command and the operations docs.
type Layout struct {
	Home           string `json:"home"`
	Database       string `json:"database"`
	Runs           string `json:"runs"`
	Repos          string `json:"repos"`
	Digest         string `json:"digest"`
	DaemonLog      string `json:"daemon_log"`
	CLILog         string `json:"cli_log"`
	CLIState       string `json:"cli_state"`
	AgentsConfig   string `json:"agents_config"`
	PendingReports string `json:"pending_reports"`
	Agents         string `json:"agents"`
	Skills         string `json:"skills"`
	Chat           string `json:"chat"`
	Bin            string `json:"bin"`
	Hooks          string `json:"hooks"`
}

// Current returns the current layout snapshot.
func Current() Layout {
	return Layout{
		Home:           Home(),
		Database:       DB(),
		Runs:           Runs(),
		Repos:          Repos(),
		Digest:         Digest(),
		DaemonLog:      DaemonLog(),
		CLILog:         CLILog(),
		CLIState:       CLIState(),
		AgentsConfig:   AgentsConfig(),
		PendingReports: PendingReports(),
		Agents:         filepath.Join(Home(), "agents"),
		Skills:         filepath.Join(Home(), "skills"),
		Chat:           filepath.Join(Home(), "chat"),
		Bin:            BinDir(),
		Hooks:          HookRuns(),
	}
}

// JSON renders the layout for CLI output.
func (l Layout) JSON() string {
	b, _ := json.MarshalIndent(l, "", "  ")
	return string(b)
}
