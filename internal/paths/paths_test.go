package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	t.Setenv(EnvDB, "")

	if got, want := Home(), dir; got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
	if got, want := DB(), filepath.Join(dir, "flowloop.db"); got != want {
		t.Fatalf("DB() = %q, want %q", got, want)
	}
	if got, want := Runs(), filepath.Join(dir, "runs"); got != want {
		t.Fatalf("Runs() = %q, want %q", got, want)
	}
}

func TestHomeDefaultsToDotFlowLoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, "")
	t.Setenv(EnvDB, "")
	t.Setenv("HOME", home)

	if got, want := Home(), filepath.Join(home, ".flowloop"); got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}

func TestHomeExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvHome, "~/custom-flowloop")

	if got, want := Home(), filepath.Join(home, "custom-flowloop"); got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}

func TestDBOverride(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "other", "state.db")
	t.Setenv(EnvHome, filepath.Join(root, "home"))
	t.Setenv(EnvDB, db)

	if got := DB(); got != db {
		t.Fatalf("DB() = %q, want %q", got, db)
	}
}

func TestEnsureHome(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "flowloop")
	t.Setenv(EnvHome, dir)

	got, err := EnsureHome()
	if err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if got != dir {
		t.Fatalf("EnsureHome() = %q, want %q", got, dir)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("data root not created: %v", err)
	}
}

func TestLayoutCoversEveryRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvHome, root)
	t.Setenv(EnvDB, "")

	l := Current()
	for name, got := range map[string]string{
		"Home":           l.Home,
		"Database":       l.Database,
		"Runs":           l.Runs,
		"Digest":         l.Digest,
		"DaemonLog":      l.DaemonLog,
		"CLILog":         l.CLILog,
		"CLIState":       l.CLIState,
		"AgentsConfig":   l.AgentsConfig,
		"PendingReports": l.PendingReports,
		"Agents":         l.Agents,
		"Skills":         l.Skills,
		"Chat":           l.Chat,
		"Bin":            l.Bin,
	} {
		if got == "" {
			t.Errorf("%s is empty", name)
		}
		if !strings.HasPrefix(got, root+string(os.PathSeparator)) && got != root {
			t.Errorf("%s = %q, want under %q", name, got, root)
		}
	}
	if !strings.Contains(l.JSON(), `"database"`) {
		t.Fatalf("JSON missing database field: %s", l.JSON())
	}
}

func TestAdoptLegacyEnvRenamesVariables(t *testing.T) {
	t.Setenv("OUROFLOW_SERVER_URL", "http://legacy:1")
	t.Setenv("FLOWLOOP_SERVER_URL", "")

	AdoptLegacyEnv()

	if got, want := os.Getenv("FLOWLOOP_SERVER_URL"), "http://legacy:1"; got != want {
		t.Fatalf("FLOWLOOP_SERVER_URL = %q, want %q", got, want)
	}
}

func TestAdoptLegacyEnvKeepsExplicitNewValue(t *testing.T) {
	t.Setenv("OUROFLOW_SERVER_URL", "http://legacy:1")
	t.Setenv("FLOWLOOP_SERVER_URL", "http://new:2")

	AdoptLegacyEnv()

	if got, want := os.Getenv("FLOWLOOP_SERVER_URL"), "http://new:2"; got != want {
		t.Fatalf("FLOWLOOP_SERVER_URL = %q, want %q", got, want)
	}
}

func TestMigrateLegacyHomeRenamesRootAndStateFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvHome, "")
	t.Setenv(EnvDB, "")

	oldRoot := filepath.Join(home, ".ouroflow")
	if err := os.MkdirAll(oldRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ouroflow.db", "ouroflow.db-wal", "ouroflow-cli-state.json"} {
		if err := os.WriteFile(filepath.Join(oldRoot, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome: %v", err)
	}

	if _, err := os.Stat(oldRoot); !os.IsNotExist(err) {
		t.Fatalf("legacy root still present: %v", err)
	}
	newRoot := filepath.Join(home, ".flowloop")
	if got, want := Home(), newRoot; got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
	for _, name := range []string{"flowloop.db", "flowloop.db-wal", "flowloop-cli-state.json"} {
		if _, err := os.Stat(filepath.Join(newRoot, name)); err != nil {
			t.Fatalf("%s not migrated: %v", name, err)
		}
	}
}

func TestMigrateLegacyHomeUpgradesExplicitRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvHome, root)
	t.Setenv(EnvDB, "")

	// A renamed file that already exists wins: migration must never clobber it.
	if err := os.WriteFile(filepath.Join(root, "flowloop.db"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ouroflow-cli-state.json"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome: %v", err)
	}

	if b, err := os.ReadFile(filepath.Join(root, "flowloop.db")); err != nil || string(b) != "new" {
		t.Fatalf("existing flowloop.db was clobbered: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "flowloop-cli-state.json")); err != nil {
		t.Fatalf("cli state not migrated: %v", err)
	}
}

func TestMigrateLegacyHomeNoopOnFreshInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvHome, "")
	t.Setenv(EnvDB, "")

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".flowloop")); !os.IsNotExist(err) {
		t.Fatalf("fresh install grew a data root: %v", err)
	}
}

// An already-present ~/.flowloop must not shadow a populated ~/.ouroflow: the
// legacy entries are merged in, so the upgrade never starts from an empty DB.
func TestMigrateLegacyHomeMergesIntoExistingRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvHome, "")
	t.Setenv(EnvDB, "")

	oldRoot := filepath.Join(home, ".ouroflow")
	newRoot := filepath.Join(home, ".flowloop")
	if err := os.MkdirAll(filepath.Join(oldRoot, "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(newRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldRoot, "ouroflow.db"), []byte("db"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newRoot, "cli.log"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := MigrateLegacyHome(); err != nil {
		t.Fatalf("MigrateLegacyHome: %v", err)
	}

	if _, err := os.Stat(filepath.Join(newRoot, "flowloop.db")); err != nil {
		t.Fatalf("legacy db not merged: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(newRoot, "cli.log")); err != nil || string(b) != "stub" {
		t.Fatalf("existing file was clobbered: %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(newRoot, "runs")); err != nil {
		t.Fatalf("legacy runs dir not merged: %v", err)
	}
}
