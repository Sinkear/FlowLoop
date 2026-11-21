package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// BlueprintWrite is one delivered commit recorded against a pinned goal
// (BUG-4 attribution). A commit whose message carries a
// "Blueprint-Node: <node_key>" trailer naming a node of the goal's compiled
// ledger is ATTRIBUTED to that spec node; every other delivered commit is an
// UNATTRIBUTED owner write — recorded, never blocked. PlanAlignment turns the
// unattributed rows into a 计划外写入 deviation.
type BlueprintWrite struct {
	ID         string `json:"id"`
	GoalID     string `json:"goal_id"`
	NodeKey    string `json:"node_key"`
	CommitSHA  string `json:"commit_sha"`
	Subject    string `json:"subject"`
	Attributed bool   `json:"attributed"`
	CreatedAt  string `json:"created_at"`
}

// blueprintNodeTrailerRE matches a line-anchored commit trailer:
//
//	Blueprint-Node: <node_key>
//
// The label is case-insensitive, the key stays strictly lowercase (≤64
// chars) — the inline (?i:…) scopes case folding to the label only, so
// "Blueprint-Node: N1" is a malformed key, not an attribution. (?m) anchors
// ^/$ at line boundaries, so a mid-line mention (e.g. in prose quoting the
// trailer syntax) is not a trailer. A trailing \r from a CRLF message is
// tolerated; [ \t] (not \s) keeps the match on ONE line — \s would happily
// swallow a newline and match a key on the next line.
var blueprintNodeTrailerRE = regexp.MustCompile(`(?m)^(?i:blueprint-node):[ \t]*([a-z][a-z0-9_]{0,63})[ \t]*\r?$`)

// ParseBlueprintNodeTrailer extracts the node key from a commit message
// trailer. It returns ok=false when no trailer line is present; when several
// are present the LAST one wins (a later line is the author's final word —
// e.g. an amended trailer appended to the body). A malformed key (uppercase,
// leading digit, too long, inner whitespace) is not a trailer at all.
func ParseBlueprintNodeTrailer(message string) (nodeKey string, ok bool) {
	for _, line := range strings.Split(message, "\n") {
		m := blueprintNodeTrailerRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		nodeKey, ok = m[1], true
	}
	return nodeKey, ok
}

// ParseBlueprintWrites splits a delivered git log into one BlueprintWrite per
// commit. fixLog is the output of
//
//	git log -z --format=%H %s%n%b <base>..<branch>
//
// i.e. NUL-separated entries, each "<sha> <subject>\n<body>".
//
// The parser is pure so the daemon can call it without a DB handle: a known
// node ledger may be supplied (at most one; the goal's blueprint_node keys)
// and a trailer is only an attribution when it names a node in it. Without a
// ledger the trailer's node key is kept PROVISIONALLY (Attributed=true) and
// RecordBlueprintWrites revalidates it against the ledger before inserting.
// A trailer naming a node that does not exist for the goal is a stray label,
// not an attribution: NodeKey stays empty and Attributed=false.
// ParseBlueprintWrites parses the deliver log (NUL-separated entries) into
// per-commit rows. known is the goal's ledger key set: a trailer naming an
// unknown node is still recorded (attributed=false) so it shows up as a
// deviation instead of disappearing.
func ParseBlueprintWrites(goalID, fixLog string, known map[string]bool) []BlueprintWrite {
	out := []BlueprintWrite{}
	for _, entry := range strings.Split(fixLog, "\x00") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		lines := strings.Split(entry, "\n")
		sha, subject, cut := strings.Cut(strings.TrimSpace(lines[0]), " ")
		// A junk entry (not produced by the %H %s format) is skipped rather
		// than recorded with a bogus sha.
		if !cut || !bwIsHexSHA(sha) {
			continue
		}
		w := BlueprintWrite{
			GoalID:    goalID,
			CommitSHA: sha,
			Subject:   strings.TrimSpace(subject),
		}
		if key, ok := ParseBlueprintNodeTrailer(entry); ok {
			switch {
			case known == nil:
				// No ledger supplied: provisional; Record revalidates.
				w.NodeKey, w.Attributed = key, true
			case known[key]:
				w.NodeKey, w.Attributed = key, true
			}
		}
		out = append(out, w)
	}
	return out
}

// bwIsHexSHA reports whether s is a full git object id (40 hex chars for
// SHA-1, up to 64 for SHA-256).
func bwIsHexSHA(s string) bool {
	if len(s) < 40 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// blueprintNodeKeys loads the goal's compiled node ledger (one query).
func (s *GoalService) blueprintNodeKeys(ctx context.Context, goalID string) (map[string]bool, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT node_key FROM blueprint_node WHERE goal_id=?`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys[k] = true
	}
	return keys, rows.Err()
}

// RecordBlueprintWrites persists the delivered-commit ledger for a goal. It is
// a no-op for a non-pinned goal (blueprint_version_id=”): 计划外写入 only
// means something where a spec graph exists. Attribution is recomputed here —
// at insert time — against blueprint_node, so a caller's Attributed flag is
// never trusted: a NodeKey is kept only when it names a node of this goal.
// The caller's slice is normalized in place (NodeKey/Attributed updated) so
// the caller can report accurate counts afterwards.
func (s *GoalService) RecordBlueprintWrites(ctx context.Context, goalID string, writes []BlueprintWrite) error {
	if len(writes) == 0 {
		return nil
	}
	var versionID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(blueprint_version_id,'') FROM goal WHERE id=?`, goalID).Scan(&versionID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil // goal vanished concurrently — nothing to attribute
		}
		return fmt.Errorf("blueprint writes: goal: %w", err)
	}
	if versionID == "" {
		return nil // non-pinned goals are out of scope
	}
	known, err := s.blueprintNodeKeys(ctx, goalID)
	if err != nil {
		return fmt.Errorf("blueprint writes: nodes: %w", err)
	}
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("blueprint writes: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	inserted := 0
	for i := range writes {
		w := &writes[i]
		if w.CommitSHA == "" {
			continue
		}
		if w.NodeKey == "" || !known[w.NodeKey] {
			w.NodeKey = ""
			w.Attributed = false
		} else {
			w.Attributed = true
		}
		w.GoalID = goalID
		if w.ID == "" {
			w.ID = newID()
		}
		w.CreatedAt = now()
		// INSERT OR REPLACE lands on UNIQUE(goal_id, commit_sha): a replayed
		// deliver (crash-resume) overwrites its row instead of duplicating it.
		if _, err := tx.ExecContext(ctx,
			`INSERT OR REPLACE INTO blueprint_write
			   (id, goal_id, node_key, commit_sha, subject, attributed, created_at)
			 VALUES (?,?,?,?,?,?,?)`,
			w.ID, goalID, w.NodeKey, w.CommitSHA, w.Subject, bwBoolToInt(w.Attributed), w.CreatedAt); err != nil {
			return fmt.Errorf("blueprint writes: insert %s: %w", w.CommitSHA, err)
		}
		inserted++
	}
	if inserted == 0 {
		return nil
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("blueprint writes: commit: %w", err)
	}
	return nil
}

// ListBlueprintWrites returns the goal's delivered-commit ledger, newest first.
func (s *GoalService) ListBlueprintWrites(ctx context.Context, goalID string) ([]BlueprintWrite, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id, goal_id, node_key, commit_sha, subject, attributed, created_at
		   FROM blueprint_write WHERE goal_id=?
		  ORDER BY created_at DESC, rowid DESC`, goalID)
	if err != nil {
		return nil, fmt.Errorf("blueprint writes: list: %w", err)
	}
	defer rows.Close()
	out := []BlueprintWrite{}
	for rows.Next() {
		var w BlueprintWrite
		var attributed int
		if err := rows.Scan(&w.ID, &w.GoalID, &w.NodeKey, &w.CommitSHA, &w.Subject, &attributed, &w.CreatedAt); err != nil {
			return nil, fmt.Errorf("blueprint writes: scan: %w", err)
		}
		w.Attributed = attributed != 0
		out = append(out, w)
	}
	return out, rows.Err()
}

// bwBoolToInt stores the attribution flag the way SQLite does.
func bwBoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
