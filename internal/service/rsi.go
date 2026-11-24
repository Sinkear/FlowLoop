package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/logging"
	"github.com/Sinkear/ouroflow/internal/store"
)

// W-RSI (2026-11-08): the training loop observes the ORCHESTRATION LAYER,
// not the agent internals. Inference-mode goals are never touched. A
// train-mode goal gets a training session; when the goal reaches a terminal
// state the session is sealed, the workflow graph is frozen read-time from
// the entity tables (workflow.go), the frozen copy is stored in
// rsi_report.workflow_json, and a deterministic structure analysis emits
// reviewable findings. RSI never changes executing behavior by itself — the
// human decides what to adopt.
type RsiSession struct {
	ID              string  `json:"id"`
	GoalID          string  `json:"goal_id"`
	GoalTitle       string  `json:"goal_title"`
	DomainID        string  `json:"domain_id"`
	Status          string  `json:"status"` // waiting|analyzing|ready|failed|skipped
	ContentCapture  string  `json:"content_capture"`
	SampleRate      float64 `json:"sample_rate"`
	BudgetUSD       float64 `json:"budget_usd"`
	NodeCount       int     `json:"node_count"`
	CriticalPathMS  int64   `json:"critical_path_ms"`
	ParallelSpeedup float64 `json:"parallel_speedup"`
	FindingCount    int     `json:"finding_count"`
	TopCategory     string  `json:"top_category"`
	TopTitle        string  `json:"top_title"`
	Error           string  `json:"error"`
	StartedAt       string  `json:"started_at"`
	SealedAt        string  `json:"sealed_at"`
	FinishedAt      string  `json:"finished_at"`
}

// RsiReport is the deterministic analysis output for one session.
type RsiReport struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	Status     string         `json:"status"` // ready|failed
	Summary    string         `json:"summary"`
	Metrics    map[string]any `json:"metrics"`
	Baseline   map[string]any `json:"baseline"`
	Narrative  string         `json:"narrative"`
	Error      string         `json:"error"`
	CreatedAt  string         `json:"created_at"`
	FinishedAt string         `json:"finished_at"`
}

// RsiFinding is one candidate optimization with its evidence and confidence.
type RsiFinding struct {
	ID          string   `json:"id"`
	ReportID    string   `json:"report_id"`
	Category    string   `json:"category"`
	Severity    string   `json:"severity"`
	Confidence  string   `json:"confidence"` // L1 observed | L2 recurring | L3 A/B verified
	Title       string   `json:"title"`
	Explanation string   `json:"explanation"`
	Suggestion  string   `json:"suggestion"`
	Evidence    []string `json:"evidence"`
	Score       float64  `json:"score"`
	Status      string   `json:"status"` // open|confirmed|dismissed
	CreatedAt   string   `json:"created_at"`
}

// RsiOverview is the aggregate strip above the Training list.
type RsiOverview struct {
	Sessions           int             `json:"sessions"`
	SuccessRate        float64         `json:"success_rate"`
	P95LeadTimeMS      int64           `json:"p95_lead_time_ms"`
	HumanWaitMS        int64           `json:"human_wait_ms"`
	AvgParallelSpeedup float64         `json:"avg_parallel_speedup"`
	AvgVerifyOverhead  float64         `json:"avg_verify_overhead_ratio"`
	AvgReworkRatio     float64         `json:"avg_rework_ratio"`
	Findings           int             `json:"findings"`
	Trend              []RsiTrendPoint `json:"trend"`
}

// RsiTrendPoint is one insight-panel data point: a finished optimization
// round, oldest first. Charts read this — no separate snapshot table.
type RsiTrendPoint struct {
	RoundID             string  `json:"round_id"`
	SessionID           string  `json:"session_id"`
	GoalID              string  `json:"goal_id"`
	GoalTitle           string  `json:"goal_title"`
	RoundNo             int     `json:"round_no"`
	CreatedAt           string  `json:"created_at"`
	ParallelSpeedup     float64 `json:"parallel_speedup"`
	CriticalPathMS      int64   `json:"critical_path_ms"`
	ReworkRatio         float64 `json:"rework_ratio"`
	VerifyOverhead      float64 `json:"verify_overhead_ratio"`
	SerializationLossMS int64   `json:"serialization_loss_ms"`
	FindingCount        int     `json:"finding_count"`
}

// RsiRound is one frozen optimization round in the ledger (rsi_round).
// Metrics/baseline/findings are copies taken at round time; delta compares
// this round against the previous one (empty for round 1).
type RsiRound struct {
	ID            string         `json:"id"`
	SessionID     string         `json:"session_id"`
	GoalID        string         `json:"goal_id"`
	RoundNo       int            `json:"round_no"`
	Trigger       string         `json:"trigger"` // terminal | manual_retry
	Metrics       map[string]any `json:"metrics"`
	Baseline      map[string]any `json:"baseline"`
	Delta         map[string]any `json:"delta"`
	FindingCount  int            `json:"finding_count"`
	CriticalCount int            `json:"critical_count"`
	TopCategory   string         `json:"top_category"`
	TopTitle      string         `json:"top_title"`
	Findings      []RsiFinding   `json:"findings"`
	Summary       string         `json:"summary"`
	Narrative     string         `json:"narrative"`
	CreatedAt     string         `json:"created_at"`
}

// RSIService owns training-session bookkeeping and the deterministic
// workflow analyzer. One instance is shared by the daemon (terminal hook)
// and the HTTP handler (read APIs). There is no live collection path: the
// entity tables are already the record.
type RSIService struct {
	st  *store.Store
	bus *events.Bus

	// blueprintSvc is wired from main (SetBlueprint, Phase C §12.4): the
	// 3-isomorphism emergent draft needs the blueprint validator. Optional —
	// without it RecordInferShape logs and skips drafting.
	blueprintSvc *BlueprintService

	// analyzeMu serializes analyses: analysis is fast (SQL + arithmetic) and
	// a single lock keeps a retry from racing a terminal trigger.
	analyzeMu sync.Mutex
}

func NewRSIService(st *store.Store, bus *events.Bus) *RSIService {
	return &RSIService{st: st, bus: bus}
}

// EnsureSession creates the training session for a train-mode goal if it
// does not exist. Sampling happens ONCE here: a sampled-out goal gets status
// 'skipped' and is never analyzed.
func (s *RSIService) EnsureSession(ctx context.Context, g *Goal) error {
	if g == nil || g.ExecutionMode != "train" {
		return nil
	}
	var exists int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM training_session WHERE goal_id=?`, g.ID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	status := "waiting"
	if g.SampleRate > 0 && g.SampleRate < 1 && rand.Float64() > g.SampleRate {
		status = "skipped"
	}
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO training_session
		 (id,goal_id,content_capture,sample_rate,budget_usd,status,started_at)
		 VALUES (?,?,?,?,?,?,?)`,
		newID(), g.ID, g.ContentCapture, g.SampleRate, g.BudgetUSD, status, now())
	if err != nil {
		return fmt.Errorf("insert training session: %w", err)
	}
	return nil
}

// OnGoalTerminal seals the session and launches the analyzer in the
// background. Called AFTER the reconcile transaction commits, so the
// analysis always sees the goal's terminal status.
func (s *RSIService) OnGoalTerminal(ctx context.Context, goalID string) {
	var sid, status string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,status FROM training_session WHERE goal_id=? ORDER BY started_at DESC LIMIT 1`, goalID).
		Scan(&sid, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return // inference goal or pre-RSI goal: nothing to analyze
	}
	if err != nil {
		logging.Infof("rsi: terminal lookup %s: %v", goalID, err)
		return
	}
	if status == "skipped" {
		return
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', sealed_at=? WHERE id=? AND status IN ('waiting','analyzing')`, now(), sid)
	if err != nil {
		logging.Infof("rsi: seal session %s: %v", sid, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already analyzing/ready/failed
	}
	go s.runAnalysis(sid)
}

func (s *RSIService) runAnalysis(sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("rsi: analysis panic for %s: %v", sessionID, r)
		}
	}()
	s.analyzeMu.Lock()
	defer s.analyzeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.Analyze(ctx, sessionID); err != nil {
		logging.Infof("rsi: analyze %s: %v", sessionID, err)
		_, _ = s.st.DB().ExecContext(context.Background(),
			`UPDATE training_session SET status='failed', error=?, finished_at=? WHERE id=?`,
			err.Error(), now(), sessionID)
	}
}

// Retry re-runs a failed (or completed) analysis.
func (s *RSIService) Retry(ctx context.Context, sessionID string) error {
	var status string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status FROM training_session WHERE id=?`, sessionID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// waiting is retryable: covers a goal that reached a terminal state
	// before the RSI hook existed (backfill), and re-analysis on demand.
	if status != "failed" && status != "ready" && status != "waiting" {
		return NewValidationError("session is not retryable (only waiting/failed/ready sessions can be re-analyzed)")
	}
	// Clear the previous round's headline numbers as well: while the new
	// analysis runs, GetSession still returns the old report row, and stale
	// node_count/speedup values leaked into the list/detail badges.
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', error='', finished_at='',
		 node_count=0, critical_path_ms=0, parallel_speedup=0, finding_count=0 WHERE id=?`, sessionID); err != nil {
		return err
	}
	go s.runAnalysis(sessionID)
	return nil
}

const sessionColumns = `ts.id,ts.goal_id,g.title,COALESCE(g.domain_id,''),ts.status,ts.content_capture,ts.sample_rate,ts.budget_usd,
	COALESCE(ts.node_count,0),COALESCE(ts.critical_path_ms,0),COALESCE(ts.parallel_speedup,0),
	ts.error,ts.started_at,ts.sealed_at,ts.finished_at,
	(SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id WHERE rp.session_id=ts.id)`

func scanSession(sc func(...any) error) (*RsiSession, error) {
	var x RsiSession
	if err := sc(&x.ID, &x.GoalID, &x.GoalTitle, &x.DomainID, &x.Status, &x.ContentCapture, &x.SampleRate, &x.BudgetUSD,
		&x.NodeCount, &x.CriticalPathMS, &x.ParallelSpeedup,
		&x.Error, &x.StartedAt, &x.SealedAt, &x.FinishedAt, &x.FindingCount); err != nil {
		return nil, err
	}
	return &x, nil
}

// ListSessions returns the Training list, newest first.
func (s *RSIService) ListSessions(ctx context.Context, limit int) ([]RsiSession, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM training_session ts JOIN goal g ON g.id=ts.goal_id
		 ORDER BY ts.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiSession{}
	for rows.Next() {
		x, err := scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		_ = s.st.DB().QueryRowContext(ctx,
			`SELECT f.category,f.title FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id
			 WHERE rp.session_id=? ORDER BY f.score DESC LIMIT 1`, out[i].ID).
			Scan(&out[i].TopCategory, &out[i].TopTitle)
	}
	return out, nil
}

// GetSession returns one session with its report, findings, and the
// orchestration graph. The graph is the frozen analysis-time copy when a
// report exists, otherwise a live build (so a waiting session already shows
// the DAG forming).
func (s *RSIService) GetSession(ctx context.Context, id string) (*RsiSession, *RsiReport, []RsiFinding, *WorkflowGraph, error) {
	x, err := scanSession(func(dst ...any) error {
		return s.st.DB().QueryRowContext(ctx,
			`SELECT `+sessionColumns+` FROM training_session ts JOIN goal g ON g.id=ts.goal_id WHERE ts.id=?`, id).
			Scan(dst...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// Fill the list/detail headline finding here too. ListSessions did this
	// per row, but GetSession never did — the detail page's top-finding block
	// was permanently empty.
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT f.category,f.title FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id
		 WHERE rp.session_id=? ORDER BY f.score DESC LIMIT 1`, id).
		Scan(&x.TopCategory, &x.TopTitle)
	var report *RsiReport
	var frozen *WorkflowGraph
	{
		var r RsiReport
		var metrics, baseline, wf string
		err := s.st.DB().QueryRowContext(ctx,
			`SELECT id,session_id,status,summary,metrics_json,baseline_json,workflow_json,narrative,error,created_at,finished_at
			 FROM rsi_report WHERE session_id=?`, id).
			Scan(&r.ID, &r.SessionID, &r.Status, &r.Summary, &metrics, &baseline, &wf, &r.Narrative, &r.Error, &r.CreatedAt, &r.FinishedAt)
		if err == nil {
			r.Metrics = decodeMap(metrics)
			r.Baseline = decodeMap(baseline)
			report = &r
			frozen = decodeWorkflow(wf)
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, nil, err
		}
	}
	findings := []RsiFinding{}
	if report != nil {
		rows, err := s.st.DB().QueryContext(ctx,
			`SELECT id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at
			 FROM rsi_finding WHERE report_id=? ORDER BY score DESC, created_at ASC`, report.ID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var f RsiFinding
			var ev string
			if err := rows.Scan(&f.ID, &f.ReportID, &f.Category, &f.Severity, &f.Confidence, &f.Title, &f.Explanation, &f.Suggestion, &ev, &f.Score, &f.Status, &f.CreatedAt); err != nil {
				return nil, nil, nil, nil, err
			}
			f.Evidence = decodeStrings(ev)
			findings = append(findings, f)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	graph := frozen
	if graph == nil && x.Status != "skipped" {
		graph, err = s.BuildGoalWorkflow(ctx, x.GoalID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return x, report, findings, graph, nil
}

func decodeWorkflow(s string) *WorkflowGraph {
	if s == "" || s == "{}" {
		return nil
	}
	var g WorkflowGraph
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return nil
	}
	return &g
}

// GoalWorkflow builds the live orchestration graph for a goal (read API for
// the goal detail view; no training session required).
func (s *RSIService) GoalWorkflow(ctx context.Context, goalID string) (*WorkflowGraph, error) {
	graph, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		// Unknown goal id → 404, not a 500 with a raw driver error.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return graph, nil
}

// SquadWorkflows returns compact workflow summaries for the most recent
// goals assigned to a squad — the "orchestration visibility" the squad page
// never had.
func (s *RSIService) SquadWorkflows(ctx context.Context, squadID string, limit int) ([]WorkflowGoalSummary, error) {
	if squadID == "" {
		return nil, NewValidationError("squad id is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id FROM goal WHERE assignee_type='squad' AND assignee_id=?
		 ORDER BY created_at DESC LIMIT ?`, squadID, limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []WorkflowGoalSummary{}
	for _, id := range ids {
		g, err := s.BuildGoalWorkflow(ctx, id)
		if err != nil {
			continue // a torn-down goal must not break the squad page
		}
		out = append(out, WorkflowGoalSummaryFromGraph(g))
	}
	return out, nil
}

// UpdateFinding sets a finding's triage status (confirmed/dismissed/open).
func (s *RSIService) UpdateFinding(ctx context.Context, id, status string) error {
	if status != "confirmed" && status != "dismissed" && status != "open" {
		return NewValidationError("status must be confirmed, dismissed, or open")
	}
	res, err := s.st.DB().ExecContext(ctx, `UPDATE rsi_finding SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Overview aggregates the Training strip. days<=0 means all time.
func (s *RSIService) Overview(ctx context.Context, days int) (RsiOverview, error) {
	var out RsiOverview
	where := ""
	args := []any{}
	if days > 0 {
		where = " WHERE ts.started_at >= ?"
		args = append(args, time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano))
	}
	var success, total int
	var leadTimes []int64
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT g.status,ts.started_at,ts.finished_at,ts.status
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id`+where, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var gs, ss, start, finish string
		if err := rows.Scan(&gs, &start, &finish, &ss); err != nil {
			return out, err
		}
		if ss == "skipped" {
			continue
		}
		total++
		if gs == "done" {
			success++
		}
		if t, ok := parseTimeMS(start); ok && finish != "" {
			if e, ok2 := parseTimeMS(finish); ok2 && e > t {
				leadTimes = append(leadTimes, e-t)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Sessions = total
	if total > 0 {
		out.SuccessRate = float64(success) / float64(total)
	}
	out.P95LeadTimeMS = percentile95(leadTimes)

	// Human wait: gate review seconds (all goals, not only train sessions).
	hwArgs := []any{}
	hwWhere := ""
	if days > 0 {
		hwWhere = " WHERE decided_at >= ?"
		hwArgs = append(hwArgs, args[len(args)-1])
	}
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision`+hwWhere, hwArgs...).Scan(&out.HumanWaitMS)

	// Workflow averages over ready reports in the window.
	mrows, err := s.st.DB().QueryContext(ctx,
		`SELECT rp.metrics_json FROM rsi_report rp JOIN training_session ts ON ts.id=rp.session_id
		 WHERE rp.status='ready'`+where, args...)
	if err == nil {
		var nSpeed, nOver, nRework int
		var sumSpeed, sumOver, sumRework float64
		for mrows.Next() {
			var mj string
			if err := mrows.Scan(&mj); err != nil {
				continue
			}
			m := decodeMap(mj)
			if v, ok := num(m["parallel_speedup"]); ok {
				sumSpeed += v
				nSpeed++
			}
			if v, ok := num(m["verify_overhead_ratio"]); ok {
				sumOver += v
				nOver++
			}
			if v, ok := num(m["rework_ratio"]); ok {
				sumRework += v
				nRework++
			}
		}
		mrows.Close()
		if nSpeed > 0 {
			out.AvgParallelSpeedup = sumSpeed / float64(nSpeed)
		}
		if nOver > 0 {
			out.AvgVerifyOverhead = sumOver / float64(nOver)
		}
		if nRework > 0 {
			out.AvgReworkRatio = sumRework / float64(nRework)
		}
	}

	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id JOIN training_session ts ON ts.id=rp.session_id`+where, args...).Scan(&out.Findings)

	// Insight-panel trend: every finished optimization round, oldest first.
	// Rounds — not sessions — are the unit of improvement, so a re-analysis
	// after applying a fix lands as its own point.
	out.Trend = []RsiTrendPoint{}
	tq := `SELECT r.id,r.session_id,r.goal_id,COALESCE(g.title,''),r.round_no,r.created_at,r.metrics_json,r.finding_count
	       FROM rsi_round r LEFT JOIN goal g ON g.id=r.goal_id`
	var targs []any
	if days > 0 {
		tq += ` WHERE r.created_at >= ?`
		targs = append(targs, args[len(args)-1])
	}
	tq += ` ORDER BY r.created_at ASC`
	if trows, err := s.st.DB().QueryContext(ctx, tq, targs...); err == nil {
		for trows.Next() {
			var p RsiTrendPoint
			var mj string
			if err := trows.Scan(&p.RoundID, &p.SessionID, &p.GoalID, &p.GoalTitle, &p.RoundNo, &p.CreatedAt, &mj, &p.FindingCount); err != nil {
				continue
			}
			m := decodeMap(mj)
			if v, ok := num(m["parallel_speedup"]); ok {
				p.ParallelSpeedup = v
			}
			if v, ok := m["critical_path_ms"].(float64); ok {
				p.CriticalPathMS = int64(v)
			} else if v, ok := m["critical_path_ms"].(int64); ok {
				p.CriticalPathMS = v
			}
			if v, ok := num(m["rework_ratio"]); ok {
				p.ReworkRatio = v
			}
			if v, ok := num(m["verify_overhead_ratio"]); ok {
				p.VerifyOverhead = v
			}
			if v, ok := num(m["serialization_loss_ms"]); ok {
				p.SerializationLossMS = int64(v)
			}
			out.Trend = append(out.Trend, p)
		}
		trows.Close()
	}
	return out, nil
}

// roundColumns mirrors the SELECT list in ListRounds.
const roundColumns = `id,session_id,goal_id,round_no,trigger,metrics_json,baseline_json,delta_json,
 finding_count,critical_count,top_category,top_title,findings_json,summary,narrative,created_at`

// ListRounds returns the full optimization-round ledger for a session,
// oldest first — the insight panel's timeline.
func (s *RSIService) ListRounds(ctx context.Context, sessionID string) ([]RsiRound, error) {
	if sessionID == "" {
		return nil, NewValidationError("session id is required")
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT `+roundColumns+` FROM rsi_round WHERE session_id=? ORDER BY round_no ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiRound{}
	for rows.Next() {
		var r RsiRound
		var mj, bj, dj, fj string
		if err := rows.Scan(&r.ID, &r.SessionID, &r.GoalID, &r.RoundNo, &r.Trigger,
			&mj, &bj, &dj, &r.FindingCount, &r.CriticalCount, &r.TopCategory, &r.TopTitle,
			&fj, &r.Summary, &r.Narrative, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Metrics = decodeMap(mj)
		r.Baseline = decodeMap(bj)
		r.Delta = decodeMap(dj)
		r.Findings = []RsiFinding{}
		_ = json.Unmarshal([]byte(fj), &r.Findings)
		out = append(out, r)
	}
	return out, rows.Err()
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
}

func decodeMap(s string) map[string]any {
	m := map[string]any{}
	if s == "" {
		return m
	}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func decodeStrings(s string) []string {
	out := []string{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func percentile95(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]int64(nil), v...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * 0.95)
	return cp[idx]
}

// ═══════════════════════════════════════════════════════════════════════
// Phase C (proposal §12.4): 涌现固化 — infer 执行图同构检测.
// goal:finished(done) & infer-mode → shape signature 入账 infer_shape；
// 同域连续 3 次同签名 → BlueprintService.DraftFromGoalExecution 自动起草
// v1 草稿（确定性翻译，永不自动发布）。
// §14.1 收敛补强: L1 全量签名 + L0 骨架签名双层 — 复杂场景下三次全量同构
// 很难凑齐（验证轮/咨询/时序波动都会扰动 L1）；L0 剥离这些运行间噪声后
// 命中即出「近似固化」草稿（仍只起草、仍等人工审查）。
// ═══════════════════════════════════════════════════════════════════════

// Auto-iteration round cap (§14.2). Semantics: analysis itself is NEVER
// capped (visibility must not regress); the cap limits the automatic
// production of optimization artifacts — emergent drafts per domain
// (infer_shape.drafted) and translator proposals per blueprint
// (created_by_id='rsi-translator'). Human-created proposals, approvals and
// publishes are never subject to it. Default 10; 0 = auto-drafting off.
const (
	rsiMaxAutoRoundsKey  = "rsi.max_auto_rounds"
	defaultMaxAutoRounds = 10
)

// SetBlueprint wires the blueprint service for the emergent draft (§12.4).
func (s *RSIService) SetBlueprint(bp *BlueprintService) { s.blueprintSvc = bp }

// skeletonShapeSignature signs the WORK SKELETON only (§14.1): support loops
// that legitimately vary run-to-run — verify/review/consult nodes, inferred
// seq temporal edges, rework rounds — are stripped before signing. Two runs
// of the same real process share a skeleton even when their verification or
// timing details differ; that is the L0 lens that lets convergence happen in
// complex scenarios. Same honest caveat as L1: a false positive only yields
// an extra 近似固化 draft awaiting human review — zero risk by construction.
func skeletonShapeSignature(wf *WorkflowGraph) string {
	dropKind := map[string]bool{"verify": true, "review": true, "consult": true}
	dropEdge := map[string]bool{"seq": true, "verified_by": true, "consult": true}
	kindOf := map[string]string{}
	for _, n := range wf.Nodes {
		kindOf[n.Key] = n.Kind
	}
	kinds := make([]string, 0, len(wf.Nodes))
	for _, n := range wf.Nodes {
		if !dropKind[n.Kind] {
			kinds = append(kinds, n.Kind)
		}
	}
	sort.Strings(kinds)
	triples := make([]string, 0, len(wf.Edges))
	for _, e := range wf.Edges {
		if dropEdge[e.Kind] || dropKind[kindOf[e.From]] || dropKind[kindOf[e.To]] {
			continue
		}
		triples = append(triples, kindOf[e.From]+"|"+kindOf[e.To]+"|"+e.Kind)
	}
	sort.Strings(triples)
	parts := []string{
		"N:" + strings.Join(kinds, ","),
		"E:" + strings.Join(triples, ","),
	}
	return hashContent(strings.Join(parts, "\n"))
}

// executionShapeSignature is the v1 heuristic (§12.4, honestly labeled):
// node-kind multiset + normalized (from_kind|to_kind|edge_kind) triple
// multiset, sorted, SHA256. Identity-free (agents swapping does not change
// it); NOT full graph canonization — a false positive only produces an
// extra human-reviewed draft, which is zero-risk by construction.
func executionShapeSignature(wf *WorkflowGraph) string {
	parts := make([]string, 0, len(wf.Nodes)+len(wf.Edges))
	kinds := make([]string, len(wf.Nodes))
	for i, n := range wf.Nodes {
		kinds[i] = n.Kind
	}
	sort.Strings(kinds)
	parts = append(parts, "N:"+strings.Join(kinds, ","))
	triples := make([]string, 0, len(wf.Edges))
	kindOf := map[string]string{}
	for _, n := range wf.Nodes {
		kindOf[n.Key] = n.Kind
	}
	for _, e := range wf.Edges {
		triples = append(triples, kindOf[e.From]+"|"+kindOf[e.To]+"|"+e.Kind)
	}
	sort.Strings(triples)
	parts = append(parts, "E:"+strings.Join(triples, ","))
	return hashContent(strings.Join(parts, "\n"))
}

// RecordInferShape runs on goal:finished(done) for unbound (infer) goals. It
// computes the execution graph's shape signature, appends it to infer_shape,
// and on the 3rd consecutive equal signature in the same domain asks the
// blueprint service to auto-draft a v1 (idempotent by name, never published).
func (s *RSIService) RecordInferShape(ctx context.Context, goalID string) error {
	var status, bpVersion, domainID, assigneeType, assigneeID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status, COALESCE(blueprint_version_id,''), COALESCE(domain_id,''), assignee_type, COALESCE(assignee_id,'') FROM goal WHERE id=?`, goalID).
		Scan(&status, &bpVersion, &domainID, &assigneeType, &assigneeID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return fmt.Errorf("load goal for shape: %w", err)
	}
	// Only successful, unbound (infer) goals carry an emergent signal:
	// spec-driven goals already HAVE a blueprint; failed/cancelled runs don't
	// represent a repeatable success shape.
	if status != "done" || bpVersion != "" {
		return nil
	}
	wf, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		return fmt.Errorf("build workflow for shape: %w", err)
	}
	sig := executionShapeSignature(wf)
	skel := skeletonShapeSignature(wf)
	squadID := ""
	if assigneeType == "squad" {
		squadID = assigneeID
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO infer_shape (id,goal_id,domain_id,squad_id,signature,skeleton,node_count,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		newID(), goalID, domainID, squadID, sig, skel, len(wf.Nodes), now()); err != nil {
		return fmt.Errorf("insert infer shape: %w", err)
	}
	// 3-consecutive check in the same domain (Q4 定稿: 阈值 3; §14.1 layering):
	// L1 exact ×3 → 固化 draft; L0 skeleton ×3 with L1 differing → 近似固化
	// draft built from the LATEST execution, named by the skeleton signature
	// (so later near-miss triples dedup into the same reviewable draft).
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT signature, skeleton FROM infer_shape WHERE domain_id=? ORDER BY created_at DESC LIMIT 3`, domainID)
	if err != nil {
		return fmt.Errorf("load recent shapes: %w", err)
	}
	defer rows.Close()
	type shapeRow struct{ sig, skel string }
	var recent []shapeRow
	for rows.Next() {
		var r shapeRow
		if err := rows.Scan(&r.sig, &r.skel); err != nil {
			return err
		}
		recent = append(recent, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(recent) < 3 {
		return nil
	}
	exact := recent[0].sig == recent[1].sig && recent[1].sig == recent[2].sig
	approx := !exact && recent[0].skel == recent[1].skel && recent[1].skel == recent[2].skel
	if !exact && !approx {
		return nil
	}
	if s.blueprintSvc == nil {
		logging.Warnf("rsi: blueprint service not wired — emergent draft skipped")
		return nil
	}
	// Auto-iteration round cap (§14.2): analysis is NEVER capped — only the
	// automatic production of optimization artifacts is. Emergent drafts are
	// counted per domain via infer_shape.drafted.
	if cap := s.autoRoundsCap(ctx); cap >= 0 {
		var drafted int
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM infer_shape WHERE domain_id=? AND drafted=1`, domainID).Scan(&drafted); err != nil {
			return fmt.Errorf("count drafted shapes: %w", err)
		}
		if drafted >= cap {
			logging.Infof("rsi: auto-iteration cap %d reached for domain %s — emergent draft skipped (rsi.max_auto_rounds)",
				cap, shortID(domainID))
			return nil
		}
	}
	label := "固化"
	nameSig := sig
	if approx {
		label = "近似固化"
		nameSig = skel
		logging.Infof("rsi: 3 consecutive isomorphic WORK SKELETONS (L0) in domain %s — details differ — drafting approximate blueprint", shortID(domainID))
	} else {
		logging.Infof("rsi: 3 consecutive isomorphic infer shapes in domain %s — drafting blueprint v1", shortID(domainID))
	}
	bp, err := s.blueprintSvc.DraftFromGoalExecution(ctx, goalID, domainID, nameSig, label)
	if err != nil {
		// A draft failure must never break the goal's terminal flow.
		logging.Errorf("rsi: emergent draft failed: %v", err)
		return nil
	}
	if bp != nil {
		if _, err := s.st.DB().ExecContext(ctx,
			`UPDATE infer_shape SET drafted=1 WHERE goal_id=?`, goalID); err != nil {
			return fmt.Errorf("mark shape drafted: %w", err)
		}
	}
	return nil
}

// shortID keeps log lines narrow (ids are 32-hex; 8 is enough to eyeball).
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// autoRoundsCap reads the auto-iteration round cap from app_settings
// (§14.2). Key rsi.max_auto_rounds; default 10; 0 disables auto-drafting
// entirely; unparseable values fall back to the default. Human actions —
// manual proposals, approvals, publishes — are NEVER subject to this cap.
func (s *RSIService) autoRoundsCap(ctx context.Context) int {
	var v string
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(value,'') FROM app_settings WHERE key=?`, rsiMaxAutoRoundsKey).Scan(&v)
	v = strings.Trim(strings.TrimSpace(v), `"`)
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n
	}
	return defaultMaxAutoRounds
}

// ═══════════════════════════════════════════════════════════════════════
// Phase C (proposal §12.3): 同题 A/B — two goals' execution metrics side by
// side with deltas. The pinned blueprint version of each goal is echoed so
// the caller can verify the pair actually compares two variants. Metric set
// is the one the platform really computes (verify_pass_rate landed in
// Phase D §13 from verification_result-derived graph data; cost_per_success
// still honestly absent — run table carries no token/cost columns).
// ═══════════════════════════════════════════════════════════════════════

// ABMetricKeys is the ordered comparison set (label shown by the frontend).
var ABMetricKeys = []string{
	"critical_path_ms",
	"wall_ms",
	"parallel_speedup",
	"idle_gap_share",
	"rework_ratio",
	"verify_pass_rate",
	"verify_overhead_ratio",
	"human_wait_ms",
	"human_wait_share",
	"gate_wait_ms",
	"lane_imbalance",
	"agent_busy_ms",
}

// ABGoalFace is one side of the comparison.
type ABGoalFace struct {
	GoalID             string         `json:"goal_id"`
	GoalTitle          string         `json:"goal_title"`
	Status             string         `json:"status"`
	BlueprintVersionID string         `json:"blueprint_version_id"`
	Metrics            map[string]any `json:"metrics"`
}

// ABCompareResult carries both faces + deltas (b - a, computed where numeric).
type ABCompareResult struct {
	GoalA  ABGoalFace     `json:"goal_a"`
	GoalB  ABGoalFace     `json:"goal_b"`
	Deltas map[string]any `json:"deltas"`
	Notes  []string       `json:"notes,omitempty"`
}

func abGoalFace(ctx context.Context, s *RSIService, goalID string) (ABGoalFace, error) {
	var title, status, bpVersion string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT title, status, COALESCE(blueprint_version_id,'') FROM goal WHERE id=?`, goalID).
		Scan(&title, &status, &bpVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ABGoalFace{}, NewValidationError(fmt.Sprintf("goal %q not found", goalID))
		}
		return ABGoalFace{}, fmt.Errorf("load goal %s: %w", goalID, err)
	}
	wf, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		return ABGoalFace{}, fmt.Errorf("build workflow %s: %w", goalID, err)
	}
	metrics := map[string]any{}
	for _, k := range ABMetricKeys {
		if v, ok := wf.Metrics[k]; ok {
			metrics[k] = v
		}
	}
	return ABGoalFace{GoalID: goalID, GoalTitle: title, Status: status, BlueprintVersionID: bpVersion, Metrics: metrics}, nil
}

// numLike coerces the metric map values (int64|float64|int|float32) to float64.
func numLike(v any) (float64, bool) {
	switch t := v.(type) {
	case int64:
		return float64(t), true
	case float64:
		return t, true
	case int:
		return float64(t), true
	case float32:
		return float64(t), true
	}
	return 0, false
}

func (s *RSIService) GoalABCompare(ctx context.Context, goalAID, goalBID string) (*ABCompareResult, error) {
	a, err := abGoalFace(ctx, s, goalAID)
	if err != nil {
		return nil, err
	}
	b, err := abGoalFace(ctx, s, goalBID)
	if err != nil {
		return nil, err
	}
	deltas := map[string]any{}
	var notes []string
	for _, k := range ABMetricKeys {
		av, aok := a.Metrics[k]
		bv, bok := b.Metrics[k]
		if !aok || !bok {
			continue
		}
		af, aIsNum := numLike(av)
		bf, bIsNum := numLike(bv)
		if aIsNum && bIsNum {
			deltas[k] = bf - af
		}
	}
	if a.Status != "done" || b.Status != "done" {
		notes = append(notes, "one or both goals are still mid-flight — metrics are partial until both are done")
	}
	if a.BlueprintVersionID == b.BlueprintVersionID && a.BlueprintVersionID != "" {
		notes = append(notes, "both goals pin the SAME blueprint version — this is not a variant comparison")
	}
	if a.BlueprintVersionID == "" || b.BlueprintVersionID == "" {
		notes = append(notes, "one or both goals are infer-mode (no pinned version) — comparison still shown, but not a v1-vs-v2 A/B")
	}
	return &ABCompareResult{GoalA: a, GoalB: b, Deltas: deltas, Notes: notes}, nil
}
