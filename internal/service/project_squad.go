package service

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Sinkear/ouroflow/internal/store"
)

// ProjectSquad is a squad's membership in a project. It is intentionally
// separate from Connection: a squad can work for a project without touching
// that project's repository (a scratch project may have no repo at all), and
// a repo connection is an optional event-routing binding on top of membership.
type ProjectSquad struct {
	DomainID  string `json:"domain_id"`
	SquadID   string `json:"squad_id"`
	CreatedAt string `json:"created_at"`
}

type ProjectSquadService struct {
	st *store.Store
}

func NewProjectSquadService(st *store.Store) *ProjectSquadService {
	return &ProjectSquadService{st: st}
}

// List returns the squads explicitly joined to a project (including rows
// backfilled from legacy connections/assignees).
func (s *ProjectSquadService) List(ctx context.Context, domainID string) ([]ProjectSquad, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT domain_id, squad_id, created_at FROM project_squad WHERE domain_id=? ORDER BY created_at ASC`, domainID)
	if err != nil {
		return nil, fmt.Errorf("list project squads: %w", err)
	}
	defer rows.Close()
	out := []ProjectSquad{}
	for rows.Next() {
		var m ProjectSquad
		if err := rows.Scan(&m.DomainID, &m.SquadID, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan project squad: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Add joins a squad to a project. It is idempotent (joining twice is a no-op),
// and requires neither a repo nor webhook events.
func (s *ProjectSquadService) Add(ctx context.Context, domainID, squadID string) (*ProjectSquad, error) {
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM domain WHERE id=?`, domainID, "project"); err != nil {
		return nil, err
	}
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, squadID, "squad"); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO project_squad (domain_id, squad_id, created_at) VALUES (?,?,?)`,
		domainID, squadID, now); err != nil {
		return nil, fmt.Errorf("add project squad: %w", err)
	}
	m := &ProjectSquad{DomainID: domainID, SquadID: squadID}
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT created_at FROM project_squad WHERE domain_id=? AND squad_id=?`, domainID, squadID).
		Scan(&m.CreatedAt); err != nil {
		return nil, fmt.Errorf("read project squad: %w", err)
	}
	return m, nil
}

// Remove unjoins a squad. It refuses while a repository connection still
// exists for the pair: the connection keeps firing webhooks and would create
// goals for a squad the project no longer lists. The user should remove the
// connection first (squad → 接入), then remove the membership.
func (s *ProjectSquadService) Remove(ctx context.Context, domainID, squadID string) error {
	var n int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM connection WHERE domain_id=? AND squad_id=?`, domainID, squadID).Scan(&n); err != nil {
		return fmt.Errorf("check connection: %w", err)
	}
	if n > 0 {
		return NewValidationError("该小队还接入了本项目的仓库；请先在小队「接入」里删除仓库连接，再移出项目")
	}
	res, err := s.st.DB().ExecContext(ctx,
		`DELETE FROM project_squad WHERE domain_id=? AND squad_id=?`, domainID, squadID)
	if err != nil {
		return fmt.Errorf("remove project squad: %w", err)
	}
	if ra, _ := res.RowsAffected(); ra == 0 {
		return ErrNotFound
	}
	return nil
}

// AddTx is used by Connection.Create so a repo binding immediately implies
// membership without a second service call.
func (s *ProjectSquadService) AddTx(ctx context.Context, tx *sql.Tx, domainID, squadID string) error {
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO project_squad (domain_id, squad_id, created_at) VALUES (?,?,?)`,
		domainID, squadID, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("add project squad: %w", err)
	}
	return nil
}

// deleteForSquad clears membership when a squad is deleted. Connections are
// removed by the caller (SquadService.Delete) in the same transaction.
func (s *ProjectSquadService) deleteForSquadTx(ctx context.Context, tx *sql.Tx, squadID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM project_squad WHERE squad_id=?`, squadID); err != nil {
		return fmt.Errorf("delete project squads: %w", err)
	}
	return nil
}
