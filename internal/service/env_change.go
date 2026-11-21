package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnvChange is one row of the scratch 环境变更 ledger: a non-git domain's
// deliverable is an environment mutation (version swap, snapshot), so the
// platform records before/after versions plus a rollback anchor instead of a
// git commit. Credentials never live here — only refs and hashes.
type EnvChange struct {
	ID             string `json:"id"`
	GoalID         string `json:"goal_id"`
	RunID          string `json:"run_id"`
	NodeKey        string `json:"node_key"`
	EnvID          string `json:"env_id"`
	BeforeVersion  string `json:"before_version"`
	AfterVersion   string `json:"after_version"`
	SnapshotRef    string `json:"snapshot_ref"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Status         string `json:"status"`
	Operator       string `json:"operator"`
	StartedAt      string `json:"started_at"`
	FinishedAt     string `json:"finished_at"`
	Error          string `json:"error"`
	CreatedAt      string `json:"created_at"`
}

// EnvChangeInput is the record request. GoalID is supplied by the caller
// (the RPC handler resolves it from the run token, never the request body).
type EnvChangeInput struct {
	GoalID         string
	RunID          string
	NodeKey        string
	EnvID          string
	BeforeVersion  string
	AfterVersion   string
	SnapshotRef    string
	ManifestSHA256 string
	Status         string
	Operator       string
	StartedAt      string
	FinishedAt     string
	Error          string
}

// envChangeValidSHA256 reports whether v is 64 lowercase hex characters. An
// empty value is allowed (the manifest hash is optional) — callers check the
// empty case before invoking this.
func envChangeValidSHA256(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// RecordEnvChange validates and appends one environment-mutation attempt.
// Status starts at "started" and settles to succeeded|failed|rolled_back;
// a terminal row records when it finished (default now) and a failed row
// MUST carry the error text, so an incident is never a bare red status.
func (s *GoalService) RecordEnvChange(ctx context.Context, in EnvChangeInput) (*EnvChange, error) {
	if err := s.st.DB().QueryRowContext(ctx, `SELECT 1 FROM goal WHERE id=?`, in.GoalID).Scan(new(int)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NewValidationError(fmt.Sprintf("goal %q not found", in.GoalID))
		}
		return nil, fmt.Errorf("record env change: goal: %w", err)
	}

	in.NodeKey = strings.TrimSpace(in.NodeKey)
	in.EnvID = strings.TrimSpace(in.EnvID)
	in.Status = strings.TrimSpace(in.Status)
	in.BeforeVersion = strings.TrimSpace(in.BeforeVersion)
	in.AfterVersion = strings.TrimSpace(in.AfterVersion)
	in.SnapshotRef = strings.TrimSpace(in.SnapshotRef)
	in.ManifestSHA256 = strings.TrimSpace(in.ManifestSHA256)
	in.Operator = strings.TrimSpace(in.Operator)
	in.StartedAt = strings.TrimSpace(in.StartedAt)
	in.FinishedAt = strings.TrimSpace(in.FinishedAt)
	in.Error = strings.TrimSpace(in.Error)

	if in.EnvID == "" {
		return nil, NewValidationError("env_id is required")
	}
	switch in.Status {
	case "started", "succeeded", "failed", "rolled_back":
	default:
		return nil, NewValidationError(fmt.Sprintf("status %q is not one of started|succeeded|failed|rolled_back", in.Status))
	}
	if in.Status == "started" && in.FinishedAt != "" {
		return nil, NewValidationError("status started cannot carry finished_at (settle the row as succeeded|failed|rolled_back)")
	}
	if in.Status == "succeeded" && (in.BeforeVersion == "" || in.AfterVersion == "") {
		return nil, NewValidationError("a succeeded env change requires both before_version and after_version")
	}
	if in.Status == "failed" && in.Error == "" {
		return nil, NewValidationError("a failed env change requires error")
	}
	if in.ManifestSHA256 != "" && !envChangeValidSHA256(in.ManifestSHA256) {
		return nil, NewValidationError("manifest_sha256 must be 64 lowercase hex characters")
	}

	// node_key is the bridge to the plan ledger. On a pinned goal it must name a
	// real compiled node; on an unpinned goal it stays free-form (no spec exists
	// to validate against). Empty is always allowed: not every change is a node.
	if in.NodeKey != "" {
		var pinned int
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM goal WHERE id=? AND COALESCE(blueprint_version_id,'')<>''`, in.GoalID).Scan(&pinned); err != nil {
			return nil, fmt.Errorf("record env change: node check: %w", err)
		}
		if pinned > 0 {
			var nodes int
			if err := s.st.DB().QueryRowContext(ctx,
				`SELECT COUNT(*) FROM blueprint_node WHERE goal_id=? AND node_key=?`, in.GoalID, in.NodeKey).Scan(&nodes); err != nil {
				return nil, fmt.Errorf("record env change: node check: %w", err)
			}
			if nodes == 0 {
				return nil, NewValidationError(fmt.Sprintf("node_key %q is not a node of goal %s", in.NodeKey, in.GoalID))
			}
		}
	}

	if in.StartedAt == "" {
		in.StartedAt = now()
	}
	startedAt, err := time.Parse(time.RFC3339Nano, in.StartedAt)
	if err != nil {
		return nil, NewValidationError("started_at must be RFC3339Nano: " + err.Error())
	}
	switch in.Status {
	case "succeeded", "failed", "rolled_back":
		if in.FinishedAt == "" {
			in.FinishedAt = now()
		}
	}
	if in.FinishedAt != "" {
		finishedAt, err := time.Parse(time.RFC3339Nano, in.FinishedAt)
		if err != nil {
			return nil, NewValidationError("finished_at must be RFC3339Nano: " + err.Error())
		}
		if finishedAt.Before(startedAt) {
			return nil, NewValidationError("finished_at must not be before started_at")
		}
	}

	row := &EnvChange{
		ID:             newID(),
		GoalID:         in.GoalID,
		RunID:          in.RunID,
		NodeKey:        in.NodeKey,
		EnvID:          in.EnvID,
		BeforeVersion:  in.BeforeVersion,
		AfterVersion:   in.AfterVersion,
		SnapshotRef:    in.SnapshotRef,
		ManifestSHA256: in.ManifestSHA256,
		Status:         in.Status,
		Operator:       in.Operator,
		StartedAt:      in.StartedAt,
		FinishedAt:     in.FinishedAt,
		Error:          in.Error,
		CreatedAt:      now(),
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO env_change
		   (id, goal_id, run_id, node_key, env_id, before_version, after_version,
		    snapshot_ref, manifest_sha256, status, operator, started_at, finished_at, error, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID, row.GoalID, row.RunID, row.NodeKey, row.EnvID, row.BeforeVersion, row.AfterVersion,
		row.SnapshotRef, row.ManifestSHA256, row.Status, row.Operator, row.StartedAt, row.FinishedAt,
		row.Error, row.CreatedAt); err != nil {
		return nil, fmt.Errorf("record env change: insert: %w", err)
	}
	return row, nil
}

// ListEnvChanges returns the goal's environment-mutation history, newest
// first (started_at DESC — the ledger's timeline, not insertion order).
func (s *GoalService) ListEnvChanges(ctx context.Context, goalID string) ([]EnvChange, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id, goal_id, run_id, node_key, env_id, before_version, after_version,
		        snapshot_ref, manifest_sha256, status, operator, started_at, finished_at,
		        error, created_at
		   FROM env_change WHERE goal_id=? ORDER BY started_at DESC`, goalID)
	if err != nil {
		return nil, fmt.Errorf("list env changes: %w", err)
	}
	defer rows.Close()
	out := []EnvChange{}
	for rows.Next() {
		var e EnvChange
		if err := rows.Scan(&e.ID, &e.GoalID, &e.RunID, &e.NodeKey, &e.EnvID, &e.BeforeVersion,
			&e.AfterVersion, &e.SnapshotRef, &e.ManifestSHA256, &e.Status, &e.Operator,
			&e.StartedAt, &e.FinishedAt, &e.Error, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("list env changes: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list env changes: %w", err)
	}
	return out, nil
}
