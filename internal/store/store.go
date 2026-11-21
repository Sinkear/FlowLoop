// Package store is the SQLite persistence layer.
//
// Single-file SQLite (WAL mode) at $AGENTWORK_HOME/agentwork.db by default
// (~/.agentwork/agentwork.db when AGENTWORK_HOME is unset).
// Schema is applied on Open via schema.sql (CREATE TABLE IF NOT EXISTS) —
// no migration tool for now.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/eushing/agentwork/internal/paths"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the SQLite handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and applies
// the schema. If path is empty, defaults to $AGENTWORK_HOME/agentwork.db.
func Open(path string) (*Store, error) {
	if path == "" {
		path = paths.DB()
	}
	if path != ":memory:" {
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	// DSN pragmas apply to every pooled connection. foreign_keys,
	// journal_mode, and busy_timeout are per-connection in SQLite, so setting
	// them in schema.sql only affects the one connection that runs it — pooled
	// connections would silently get foreign_keys=0 and busy_timeout=0. The
	// DSN form modernc.org/sqlite supports is ?_pragma=name(value).
	// busy_timeout=5000: when two writes collide (rare under WAL), block up to
	// 5s for the lock instead of returning SQLITE_BUSY immediately — without
	// this, concurrent task handlers persisting messages get (5) SQLITE_BUSY.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// Single writer + many readers is fine for SQLite WAL; one connection
	// for writes keeps the model simple. Pool a few for concurrent reads.
	db.SetMaxOpenConns(8)
	if path == ":memory:" {
		// An in-memory database is PER-CONNECTION in SQLite — a pool would
		// fragment the schema across connections ("no such table" on queries
		// that land on a different connection than the one that ran the
		// schema). Tests only: pin a single connection.
		db.SetMaxOpenConns(1)
	}

	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// Migrations: ALTER TABLE ADD COLUMN has no IF NOT EXISTS in SQLite, so a
	// repeated Open on an existing DB errors on "duplicate column." Each
	// migration is best-effort — ignore the duplicate error, execute the rest.
	// The goal_id column and its index are in the migration (not schema.sql)
	// because CREATE TABLE IF NOT EXISTS does not alter an existing table —
	// an index on the missing column would fail on the old DB before the
	// ALTER runs.
	for _, mig := range []string{
		`ALTER TABLE team_import ADD COLUMN goal_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_team_import_goal ON team_import(goal_id)`,
		// M4-B PR pull (GitCode PR → goal): the PR handler reuses the issue
		// tracking's repo/provider — only the assignee side is new.
		`ALTER TABLE domain ADD COLUMN pull_assignee TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE domain ADD COLUMN pull_assignee_type TEXT NOT NULL DEFAULT 'agent'`,
		// Repo-level webhook secret ('' = fall back to the platform secret).
		// The webhook_delivery table comes from schema.sql; only its indexes
		// need migrating here for DBs created before the table existed.
		`ALTER TABLE domain ADD COLUMN webhook_secret TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_delivery_domain ON webhook_delivery(domain_id, received_at)`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_delivery_repo ON webhook_delivery(repo, received_at)`,
		// Per-connection webhook routing: one delivery row can now name the
		// connection it was routed to ('' = legacy /webhooks/{provider} path).
		// The column lives in the migration (not schema.sql) for the same
		// reason as goal_id above: an index on a missing column would fail on
		// an old DB before the ALTER runs.
		`ALTER TABLE webhook_delivery ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX IF NOT EXISTS idx_webhook_delivery_connection ON webhook_delivery(connection_id, received_at)`,
		// Per-connection goal dedupe: the same external issue/PR may be
		// ingested by several repo connections (each with its own squad and
		// webhook secret), so a source ref is no longer globally unique —
		// uniqueness is (source_ref, connection_id). connection_id lives in
		// the migration because the index references it and schema.sql runs
		// before the ALTER on an old DB.
		`ALTER TABLE goal ADD COLUMN connection_id TEXT NOT NULL DEFAULT ''`,

		// RSI (self-improvement): per-goal execution mode + capture knobs.
		// Columns live in the migration for the same reason as
		// connection_id — an old DB gains them before any index/query runs.
		`ALTER TABLE goal ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'infer'`,
		`ALTER TABLE goal ADD COLUMN content_capture TEXT NOT NULL DEFAULT 'redacted'`,
		`ALTER TABLE goal ADD COLUMN budget_usd REAL NOT NULL DEFAULT 2`,
		`ALTER TABLE goal ADD COLUMN sample_rate REAL NOT NULL DEFAULT 1`,
		// W-RSI (2026-11-08): workflow-level observation replaces span-level
		// trace. Sub-goal gains declared dependencies; rsi_report freezes the
		// orchestration graph; trace_span is retired outright.
		`ALTER TABLE sub_goal ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]'`,
		// Gating liveness + anchor (2026-11-14): a gated sub-goal's dispatch
		// comment id is persisted at creation so the run released LATER by
		// wakeDependentsTx can still carry the causal anchor as its
		// trigger_comment_id (without the column the wake path had to pass "",
		// and the report comment lost its dispatch parent). Old rows read ''.
		`ALTER TABLE sub_goal ADD COLUMN dispatch_comment_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE rsi_report ADD COLUMN workflow_json TEXT NOT NULL DEFAULT '{}'`,
		// training_session: span counters retire with trace_span; workflow
		// summary columns take their place. Old trace_count/cost_usd columns
		// stay in existing DBs as dead weight (SQLite has no DROP COLUMN in
		// the SQLite versions we target).
		`ALTER TABLE training_session ADD COLUMN node_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE training_session ADD COLUMN critical_path_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE training_session ADD COLUMN parallel_speedup REAL NOT NULL DEFAULT 0`,
		`ALTER TABLE training_session ADD COLUMN finding_count INTEGER NOT NULL DEFAULT 0`,
		`DROP TABLE IF EXISTS trace_span`,
		// Insight panel (2026-11-08): the optimization-round ledger replaces
		// the never-written v1 snapshot table — trend data derives from rounds.
		`DROP TABLE IF EXISTS rsi_metric_snapshot`,
		`CREATE TABLE IF NOT EXISTS rsi_round (
		    id               TEXT PRIMARY KEY,
		    session_id       TEXT NOT NULL REFERENCES training_session(id),
		    goal_id          TEXT NOT NULL,
		    round_no         INTEGER NOT NULL,
		    trigger          TEXT NOT NULL DEFAULT 'terminal',
		    metrics_json     TEXT NOT NULL DEFAULT '{}',
		    baseline_json    TEXT NOT NULL DEFAULT '{}',
		    delta_json       TEXT NOT NULL DEFAULT '{}',
		    finding_count    INTEGER NOT NULL DEFAULT 0,
		    critical_count   INTEGER NOT NULL DEFAULT 0,
		    top_category     TEXT NOT NULL DEFAULT '',
		    top_title        TEXT NOT NULL DEFAULT '',
		    findings_json    TEXT NOT NULL DEFAULT '[]',
		    summary          TEXT NOT NULL DEFAULT '',
		    narrative        TEXT NOT NULL DEFAULT '',
		    created_at       TEXT NOT NULL
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_rsi_round_session ON rsi_round(session_id, round_no)`,
		`CREATE INDEX IF NOT EXISTS idx_rsi_round_goal ON rsi_round(goal_id, created_at)`,

		// HITL/HOOTL task category: a goal created by a human/agent defaults
		// to hitl (the existing human-checkpoint flow); hootl goals finish
		// directly on completion without entering review.
		`ALTER TABLE goal ADD COLUMN category TEXT NOT NULL DEFAULT 'hitl'`,
		// A schedule clones its category onto every fired goal.
		`ALTER TABLE schedule ADD COLUMN category TEXT NOT NULL DEFAULT 'hitl'`,
		// Remote skill source (git repo binding). Tokens are stored like the
		// domain.git_credentials column (single-user daemon, local SQLite).
		`ALTER TABLE skill ADD COLUMN repo_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN repo_provider TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN repo_branch TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN repo_path TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN repo_token TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN last_synced_at TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN last_sync_action TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN last_remote_sha TEXT NOT NULL DEFAULT ''`,
		// Content fingerprint of the last-synced file set: compare uses it to
		// tell which side moved since the last sync (the diff shape cannot).
		`ALTER TABLE skill ADD COLUMN last_sync_digest TEXT NOT NULL DEFAULT ''`,
		// Hook params (flat {string:string}). schema.sql carries the columns
		// for fresh DBs; these ALTERs retrofit DBs whose hook tables were
		// created before params existed. hook_binding.params is the
		// binding-level override; hook.params holds the defaults.
		`ALTER TABLE hook ADD COLUMN params TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE hook_binding ADD COLUMN params TEXT NOT NULL DEFAULT '{}'`,
		`DROP INDEX IF EXISTS uq_goal_source_ref`,
		`CREATE UNIQUE INDEX IF NOT EXISTS uq_goal_source_ref_conn ON goal(source_ref, connection_id) WHERE source_ref != ''`,

		// Blueprint Phase A (docs/BLUEPRINT-PROPOSAL.md §5): serialized
		// orchestration authority. goal.blueprint_version_id pins the spec
		// version a goal runs ('' = infer mode, unchanged semantics); the
		// three tables mirror schema.sql for old DBs (double-idempotent:
		// schema.sql CREATE IF NOT EXISTS + this list).
		`ALTER TABLE goal ADD COLUMN blueprint_version_id TEXT NOT NULL DEFAULT ''`,
		// Phase B (proposal §11.5): the squad whose roster this work item's
		// runs are briefed with ('' = fall back to the goal's own squad).
		`ALTER TABLE sub_goal ADD COLUMN team_squad_id TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS blueprint (
		    id              TEXT PRIMARY KEY,
		    name            TEXT NOT NULL UNIQUE,
		    description     TEXT NOT NULL DEFAULT '',
		    created_by_type TEXT NOT NULL DEFAULT 'human',
		    created_by_id   TEXT NOT NULL DEFAULT '',
		    created_at      TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS blueprint_version (
		    id           TEXT PRIMARY KEY,
		    blueprint_id TEXT NOT NULL REFERENCES blueprint(id) ON DELETE CASCADE,
		    version      INTEGER NOT NULL,
		    content      TEXT NOT NULL,
		    content_hash TEXT NOT NULL,
		    status       TEXT NOT NULL DEFAULT 'draft',
		    changelog    TEXT NOT NULL DEFAULT '',
		    created_at   TEXT NOT NULL,
		    UNIQUE(blueprint_id, version)
		)`,
		`CREATE TABLE IF NOT EXISTS blueprint_node (
		    id              TEXT PRIMARY KEY,
		    goal_id         TEXT NOT NULL REFERENCES goal(id) ON DELETE CASCADE,
		    node_key        TEXT NOT NULL,
		    parent_key      TEXT NOT NULL DEFAULT '',
		    node_type       TEXT NOT NULL,
		    sub_goal_id     TEXT NOT NULL DEFAULT '',
		    state           TEXT NOT NULL DEFAULT 'pending',
		    resolved_config TEXT NOT NULL DEFAULT '',
		    created_at      TEXT NOT NULL,
		    UNIQUE(goal_id, node_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_blueprint_node_goal ON blueprint_node(goal_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_blueprint_node_sub_goal ON blueprint_node(sub_goal_id)`,

		// Phase B-2.1 (proposal §11.8): per-squad role vocabulary registry.
		// Mirrors schema.sql for old DBs; zero rows = free-text roles kept
		// (template apply / team import / legacy squads unchanged).
		`CREATE TABLE IF NOT EXISTS squad_role (
		    id          TEXT PRIMARY KEY,
		    squad_id    TEXT NOT NULL REFERENCES squad(id) ON DELETE CASCADE,
		    name        TEXT NOT NULL,
		    description TEXT NOT NULL DEFAULT '',
		    created_at  TEXT NOT NULL,
		    UNIQUE(squad_id, name)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_squad_role_squad ON squad_role(squad_id, name)`,

		// Phase C (proposal §12): blueprint patch proposals + infer shape
		// signatures. Mirrors schema.sql for old DBs.
		`CREATE TABLE IF NOT EXISTS blueprint_proposal (
		    id                 TEXT PRIMARY KEY,
		    blueprint_id       TEXT NOT NULL REFERENCES blueprint(id) ON DELETE CASCADE,
		    base_version_id    TEXT NOT NULL REFERENCES blueprint_version(id),
		    title              TEXT NOT NULL,
		    hypothesis         TEXT NOT NULL DEFAULT '',
		    expected_metric    TEXT NOT NULL DEFAULT '',
		    expected_direction TEXT NOT NULL DEFAULT '',
		    patches_json       TEXT NOT NULL DEFAULT '[]',
		    finding_id         TEXT NOT NULL DEFAULT '',
		    status             TEXT NOT NULL DEFAULT 'pending',
		    applied_version_id TEXT NOT NULL DEFAULT '',
		    decided_at         TEXT NOT NULL DEFAULT '',
		    decided_by_type    TEXT NOT NULL DEFAULT '',
		    decided_by_id      TEXT NOT NULL DEFAULT '',
		    created_by_type    TEXT NOT NULL DEFAULT 'human',
		    created_by_id      TEXT NOT NULL DEFAULT '',
		    created_at         TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_blueprint_proposal_bp ON blueprint_proposal(blueprint_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_blueprint_proposal_status ON blueprint_proposal(status, created_at)`,
		`CREATE TABLE IF NOT EXISTS infer_shape (
		    id         TEXT PRIMARY KEY,
		    goal_id    TEXT NOT NULL,
		    domain_id  TEXT NOT NULL DEFAULT '',
		    squad_id   TEXT NOT NULL DEFAULT '',
		    signature  TEXT NOT NULL,
		    skeleton   TEXT NOT NULL DEFAULT '',
		    node_count INTEGER NOT NULL DEFAULT 0,
		    drafted    INTEGER NOT NULL DEFAULT 0,
		    created_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_infer_shape_domain ON infer_shape(domain_id, created_at)`,
		`ALTER TABLE infer_shape ADD COLUMN skeleton TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE infer_shape ADD COLUMN drafted INTEGER NOT NULL DEFAULT 0`,

		// Platform built-in skills (2026-11-20): title is the human-facing
		// display name from SKILL.md frontmatter; builtin=1 marks a skill
		// shipped inside the daemon binary (re-seeded every boot, attached to
		// the steward, delete-refused). Both columns live in schema.sql for
		// fresh DBs; these ALTERs retrofit existing ones.
		`ALTER TABLE skill ADD COLUMN title TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE skill ADD COLUMN builtin INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(mig); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			db.Close()
			return nil, fmt.Errorf("migration %q: %w", mig, err)
		}
	}

	return &Store{db: db}, nil
}

// DB exposes the underlying handle for handlers/services that run queries.
func (s *Store) DB() *sql.DB { return s.db }

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the connection is alive.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
