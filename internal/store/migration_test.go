package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestBlueprintOwnershipMigrationRebuildsLegacyTable covers the one-time
// Phase B rebuild: an existing DB has blueprint with UNIQUE(name) and no
// squad_id. Open must keep every row/version, add the legacy ” bucket, and
// replace the global unique constraint with UNIQUE(squad_id,name).
func TestBlueprintOwnershipMigrationRebuildsLegacyTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE blueprint (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			created_by_type TEXT NOT NULL DEFAULT 'human',
			created_by_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`CREATE TABLE blueprint_version (
			id TEXT PRIMARY KEY,
			blueprint_id TEXT NOT NULL REFERENCES blueprint(id) ON DELETE CASCADE,
			version INTEGER NOT NULL,
			content TEXT NOT NULL,
			content_hash TEXT NOT NULL,
			status TEXT NOT NULL,
			changelog TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`,
		`INSERT INTO blueprint (id,name,description,created_by_type,created_by_id,created_at)
		 VALUES ('bp1','legacy-flow','old','human','', '2026-01-01T00:00:00Z')`,
		`INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,changelog,created_at)
		 VALUES ('v1','bp1',1,'{}','h','published','', '2026-01-01T00:00:00Z')`,
	}
	for _, stmt := range stmts {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("seed legacy db: %v", err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open legacy db: %v", err)
	}
	defer s.Close()

	var squadID string
	if err := s.db.QueryRow(`SELECT squad_id FROM blueprint WHERE id='bp1'`).Scan(&squadID); err != nil {
		t.Fatalf("select squad_id: %v", err)
	}
	if squadID != "" {
		t.Fatalf("legacy row must land in '' bucket, got %q", squadID)
	}
	var versions int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM blueprint_version WHERE blueprint_id='bp1'`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("version lost in rebuild: %d", versions)
	}
	// Same name is now allowed in different squads, still rejected in the same.
	if _, err := s.db.Exec(`INSERT INTO blueprint (id,name,squad_id,created_at) VALUES ('bp2','legacy-flow','sq1','2026-01-02T00:00:00Z')`); err != nil {
		t.Fatalf("same name in another squad must be allowed: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO blueprint (id,name,squad_id,created_at) VALUES ('bp3','legacy-flow','sq1','2026-01-03T00:00:00Z')`); err == nil {
		t.Fatal("same name in the same squad must still be rejected")
	}
}
