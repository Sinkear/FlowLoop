package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// Issue #13: verification_result gained verify_ms / machine_run_id through the
// best-effort ALTER TABLE list, because CREATE TABLE IF NOT EXISTS cannot change
// a table that already exists. An old DB must gain both columns on Open, and a
// SECOND Open must not fail on the duplicate columns (SQLite has no
// ADD COLUMN IF NOT EXISTS) — the daemon opens the same file on every boot.
func TestOpenMigratesVerificationResultIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	// A pre-#13 database: verification_result without the two new columns.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE verification_result (
	    id              TEXT PRIMARY KEY,
	    goal_id         TEXT NOT NULL,
	    sub_goal_id     TEXT NOT NULL,
	    verifier_run_id TEXT NOT NULL DEFAULT '',
	    status          TEXT NOT NULL,
	    summary         TEXT NOT NULL DEFAULT '',
	    evidence        TEXT NOT NULL DEFAULT '',
	    created_at      TEXT NOT NULL
	)`); err != nil {
		t.Fatalf("create old table: %v", err)
	}
	if _, err := raw.Exec(`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,created_at)
	 VALUES ('vr-old','g','sg','','passed','2026-11-01T00:00:00Z')`); err != nil {
		t.Fatalf("insert old row: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	for i := 0; i < 2; i++ { // second Open must survive the duplicate columns
		st, err := Open(path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		cols := map[string]bool{}
		rows, err := st.DB().Query(`PRAGMA table_info(verification_result)`)
		if err != nil {
			st.Close()
			t.Fatalf("pragma: %v", err)
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
				rows.Close()
				st.Close()
				t.Fatalf("scan pragma: %v", err)
			}
			cols[name] = true
		}
		rows.Close()
		for _, want := range []string{"verify_ms", "machine_run_id"} {
			if !cols[want] {
				st.Close()
				t.Fatalf("Open #%d: column %q missing after migration (%v)", i+1, want, cols)
			}
		}
		// An old row keeps the documented "not timed / no machine run" defaults.
		var verifyMS int64
		var machineRun string
		if err := st.DB().QueryRow(`SELECT verify_ms,machine_run_id FROM verification_result WHERE id='vr-old'`).
			Scan(&verifyMS, &machineRun); err != nil {
			st.Close()
			t.Fatalf("read old row: %v", err)
		}
		if verifyMS != 0 || machineRun != "" {
			st.Close()
			t.Fatalf("old row defaults = (%d,%q), want (0,\"\")", verifyMS, machineRun)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close #%d: %v", i+1, err)
		}
	}
}
