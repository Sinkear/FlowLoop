package service

// Hooks: owner-uploaded lifecycle scripts (spec: tmp/hooks-spec.md v0.2).
//
// Three entities: hook (registry), hook_binding (scope×event attachment),
// hook_run (execution record). The executor subscribes to the existing event
// bus topics — it is a CONSUMER of the goal lifecycle, never a decision maker:
// hook failure is recorded and never rolls back or blocks the goal.
//
// Trust model (spec §4): V1 has a single owner write path and a single trust
// plane, so there is deliberately NO pre-execution confirmation state machine.
// content_hash is audit-only provenance: the executor copies it into every
// hook_run so the panel can show "content changed since last run" (tripwire
// badge) — a visible drift signal, not a gate.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/Sinkear/ouroflow/internal/events"
	"github.com/Sinkear/ouroflow/internal/logging"
	"github.com/Sinkear/ouroflow/internal/paths"
	"github.com/Sinkear/ouroflow/internal/store"
)

// ── constants ──

const (
	HookMaxContent     = 256 << 10 // 256 KiB script cap (spec §1.1)
	hookMaxOutput      = 8 << 10   // 8 KiB captured output tail (spec §1.3)
	hookDefaultTimeout = 60
	hookMaxTimeout     = 600
	// hookDefaultServerURL matches the daemon's default listen address; the
	// daemon's own OUROFLOW_SERVER_URL (if set) overrides it.
	hookDefaultServerURL = "http://127.0.0.1:47813"
)

// hookNameRE: kebab-case, same family as skill names.
var hookNameRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

var hookInterpreters = map[string]bool{"": true, "bash": true, "python3": true, "node": true, "sh": true}

// hookEventTopics maps the public hook event names (used in bindings and the
// stdin contract) to the existing bus topics they subscribe to. V1 keeps the
// vocabulary identical to the bus (spec §2).
var hookEventTopics = map[string]string{
	"goal_finished":        "goal:finished",
	"goal_delivered":       "goal:delivered",
	"goal_deliver_failed":  "goal:deliver_failed",
	"goal_reviewing":       "goal:reviewing",
	"goal_review_resolved": "goal:review_resolved",
}

// hookTerminalEvents are the events whose payload implies the goal is (just)
// in a terminal state; those never skip with "goal terminal".
var hookTerminalEvents = map[string]bool{
	"goal_finished":       true,
	"goal_delivered":      true,
	"goal_deliver_failed": true,
}

// goalTerminalStatuses mirrors the goal layer's terminal set.
var goalTerminalStatuses = map[string]bool{"done": true, "failed": true, "cancelled": true}

func validHookEvent(event string) bool {
	_, ok := hookEventTopics[event]
	return ok
}

// HookEvents returns the list of bindable hook event names (for the web UI).
func HookEvents() []string {
	return []string{"goal_finished", "goal_delivered", "goal_deliver_failed", "goal_reviewing", "goal_review_resolved"}
}

// ── entities ──

// Hook is the registry entity. Content is only populated by Get; list queries
// omit it (scripts can be 256 KiB each).
type Hook struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Content     string            `json:"content,omitempty"`
	ContentHash string            `json:"content_hash"`
	Interpreter string            `json:"interpreter"`
	TimeoutSec  int               `json:"timeout_sec"`
	Params      map[string]string `json:"params,omitempty"` // flat {string:string} defaults; binding params override at run time
	Enabled     bool              `json:"enabled"`
	CreatedAt   string            `json:"created_at"`
	UpdatedAt   string            `json:"updated_at"`
}

const hookMetaColumns = "id,name,description,content_hash,interpreter,timeout_sec,params,enabled,created_at,updated_at"

// HookBinding attaches a hook to an event at a scope. Inherited is derived on
// read: a domain-scope binding as seen through a goal.
type HookBinding struct {
	ID        string            `json:"id"`
	HookID    string            `json:"hook_id"`
	HookName  string            `json:"hook_name,omitempty"`
	Scope     string            `json:"scope"` // goal|domain
	ScopeID   string            `json:"scope_id"`
	Event     string            `json:"event"`
	Seq       int               `json:"seq"`
	Params    map[string]string `json:"params,omitempty"` // per-binding overrides, shallow-merged over hook defaults
	CreatedAt string            `json:"created_at"`
}

// ── params: flat {string:string} map stored as JSON text ──

var hookParamKeyRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

// validateHookParams enforces the flat string map: values are strings by Go
// type, keys are conservative identifiers so they flatten to safe env names
// (OUROFLOW_PARAM_<KEY>). Nested objects/arrays are impossible here — the
// handler decodes straight into map[string]string.
func validateHookParams(m map[string]string) error {
	for k := range m {
		if !hookParamKeyRE.MatchString(k) {
			return NewValidationError(fmt.Sprintf("hook param key %q must match ^[A-Za-z_][A-Za-z0-9_-]{0,63}$", k))
		}
	}
	return nil
}

func encodeParams(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func decodeParams(s string) map[string]string {
	if s == "" || s == "{}" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(s), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}

// mergeHookParams shallow-merges binding overrides over hook defaults
// (binding wins, key-level). Result is the run-time param set.
func mergeHookParams(hookParams, bindingParams map[string]string) map[string]string {
	if len(hookParams) == 0 && len(bindingParams) == 0 {
		return nil
	}
	out := make(map[string]string, len(hookParams)+len(bindingParams))
	for k, v := range hookParams {
		out[k] = v
	}
	for k, v := range bindingParams {
		out[k] = v
	}
	return out
}

// HookRun records one execution (or one skip) of a hook for an event.
type HookRun struct {
	ID          string `json:"id"`
	HookID      string `json:"hook_id"`
	HookName    string `json:"hook_name,omitempty"`
	BindingID   string `json:"binding_id"`
	GoalID      string `json:"goal_id"`
	Event       string `json:"event"`
	ContentHash string `json:"content_hash"`
	Status      string `json:"status"` // running|completed|failed|timeout|skipped
	ExitCode    *int   `json:"exit_code"`
	StartedAt   string `json:"started_at"`
	FinishedAt  string `json:"finished_at"`
	Output      string `json:"output"`
}

// ── service ──

// HookService owns hook CRUD, bindings and the bus-subscribed executor.
// The executor follows the notify.Notifier pattern: Subscribe collects
// unsubscribe funcs; handlers must use their own context — the bus delivers
// the publisher's (likely canceled HTTP) context.
type HookService struct {
	st *store.Store
	// bus may be nil in tests; event publishing is skipped then.
	bus    *events.Bus
	mu     sync.Mutex
	unsubs []func()

	// per-hook serialization (spec §3.3): one hook runs globally one at a
	// time so a script can never race itself; different hooks run in
	// parallel.
	lockMu    sync.Mutex
	hookLocks map[string]*sync.Mutex
}

func NewHookService(st *store.Store, bus *events.Bus) *HookService {
	return &HookService{st: st, bus: bus, hookLocks: map[string]*sync.Mutex{}}
}

// Subscribe attaches the executor to every lifecycle topic in the event
// registry. Call once at daemon startup, after SweepOrphanRuns.
func (s *HookService) Subscribe() {
	if s.bus == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, topic := range hookEventTopics {
		topic := topic
		unsub := s.bus.Subscribe(topic, func(_ context.Context, e events.Event) {
			s.onLifecycleEvent(e)
		})
		s.unsubs = append(s.unsubs, unsub)
	}
}

// Unsubscribe detaches from the bus (tests / shutdown symmetry).
func (s *HookService) Unsubscribe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.unsubs {
		u()
	}
	s.unsubs = nil
}

// SweepOrphanRuns marks in-flight hook_run rows left by a previous daemon
// process as failed. Call once at startup, BEFORE Subscribe: after the bus is
// attached, running rows belong to this process.
func (s *HookService) SweepOrphanRuns(ctx context.Context) (int64, error) {
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE hook_run SET status='failed', finished_at=?, output='daemon restart' WHERE status='running'`,
		now())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *HookService) lockFor(hookID string) *sync.Mutex {
	s.lockMu.Lock()
	defer s.lockMu.Unlock()
	m, ok := s.hookLocks[hookID]
	if !ok {
		m = &sync.Mutex{}
		s.hookLocks[hookID] = m
	}
	return m
}

func (s *HookService) publishEvent(topic string, payload map[string]any) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(context.Background(), events.Event{Topic: topic, Payload: payload})
}

// ── validation ──

// validateHookContent checks size, UTF-8 and the shebang/interpreter rule.
func validateHookContent(content, interpreter string) error {
	if len(content) == 0 {
		return NewValidationError("hook content is required")
	}
	if len(content) > HookMaxContent {
		return NewValidationError(fmt.Sprintf("hook content exceeds %d bytes", HookMaxContent))
	}
	if !utf8.ValidString(content) {
		return NewValidationError("hook content must be valid UTF-8")
	}
	if interpreter == "" {
		first := content
		if i := strings.IndexByte(content, '\n'); i >= 0 {
			first = content[:i]
		}
		if !strings.HasPrefix(first, "#!") {
			return NewValidationError("hook content must start with a shebang (#!) — or set an explicit interpreter")
		}
	}
	return nil
}

func validateHookMeta(name, interpreter string, timeoutSec int) error {
	if !hookNameRE.MatchString(name) {
		return NewValidationError("hook name must match ^[a-z][a-z0-9-]{0,63}$")
	}
	if !hookInterpreters[interpreter] {
		return NewValidationError("interpreter must be one of: bash, python3, node, sh (or empty to honor the shebang)")
	}
	if timeoutSec < 1 || timeoutSec > hookMaxTimeout {
		return NewValidationError(fmt.Sprintf("timeout_sec must be 1..%d", hookMaxTimeout))
	}
	return nil
}

func hashContent(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// ── CRUD ──

// Create registers a new hook. Name uniqueness is enforced by a pre-check
// plus the UNIQUE constraint as the race backstop.
func (s *HookService) Create(ctx context.Context, name, description, content, interpreter string, timeoutSec int, params map[string]string) (*Hook, error) {
	if name == "" {
		return nil, NewFieldRequiredError("name")
	}
	if content == "" {
		return nil, NewFieldRequiredError("content")
	}
	if timeoutSec == 0 {
		timeoutSec = hookDefaultTimeout
	}
	if err := validateHookMeta(name, interpreter, timeoutSec); err != nil {
		return nil, err
	}
	if err := validateHookContent(content, interpreter); err != nil {
		return nil, err
	}
	if err := validateHookParams(params); err != nil {
		return nil, err
	}
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hook WHERE name=?`, name).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, NewCodedErrorDetail(CodeValidation, fmt.Sprintf("hook name %q already exists", name), map[string]any{"name": name})
	}
	h := &Hook{
		ID:          newID(),
		Name:        name,
		Description: description,
		Content:     content,
		ContentHash: hashContent(content),
		Interpreter: interpreter,
		TimeoutSec:  timeoutSec,
		Params:      params,
		Enabled:     true,
		CreatedAt:   now(),
		UpdatedAt:   now(),
	}
	_, err := s.st.DB().ExecContext(ctx, `INSERT INTO hook (id,name,description,content,content_hash,interpreter,timeout_sec,params,enabled,created_at,updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		h.ID, h.Name, h.Description, []byte(h.Content), h.ContentHash, h.Interpreter, h.TimeoutSec, encodeParams(h.Params), 1, h.CreatedAt, h.UpdatedAt)
	if err != nil {
		if coded := dupNameCodedError(err, CodeValidation, "hook", name); coded != nil {
			return nil, coded
		}
		return nil, err
	}
	return h, nil
}

// scanHookMeta scans the shared meta columns (no content).
func scanHookMeta(row interface{ Scan(dest ...any) error }) (*Hook, error) {
	var h Hook
	var enabled int
	var params string
	if err := row.Scan(&h.ID, &h.Name, &h.Description, &h.ContentHash, &h.Interpreter, &h.TimeoutSec, &params, &enabled, &h.CreatedAt, &h.UpdatedAt); err != nil {
		return nil, err
	}
	h.Enabled = enabled != 0
	h.Params = decodeParams(params)
	return &h, nil
}

// List returns every hook without content, plus binding count and the last
// run (status/finished_at/content_hash — the tripwire badge inputs).
// Deliberately a single query: iterating an open rows while issuing nested
// queries deadlocks :memory: stores (MaxOpenConns(1)) and pins pool
// connections on file stores.
func (s *HookService) List(ctx context.Context) ([]map[string]any, error) {
	rows, err := s.st.DB().QueryContext(ctx, `
		SELECT h.id,h.name,h.description,h.content_hash,h.interpreter,h.timeout_sec,h.params,h.enabled,h.created_at,h.updated_at,
		       (SELECT COUNT(*) FROM hook_binding b WHERE b.hook_id=h.id) AS binding_count,
		       lr.status, lr.finished_at, lr.content_hash AS last_content_hash
		FROM hook h
		LEFT JOIN hook_run lr ON lr.id = (
			SELECT r.id FROM hook_run r
			WHERE r.hook_id=h.id AND r.status IN ('completed','failed','timeout')
			ORDER BY r.started_at DESC, r.id DESC LIMIT 1)
		ORDER BY h.created_at, h.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var h Hook
		var enabled int
		var n int
		var params string
		var status, finishedAt, lastHash sql.NullString
		if err := rows.Scan(&h.ID, &h.Name, &h.Description, &h.ContentHash, &h.Interpreter,
			&h.TimeoutSec, &params, &enabled, &h.CreatedAt, &h.UpdatedAt, &n, &status, &finishedAt, &lastHash); err != nil {
			return nil, err
		}
		h.Enabled = enabled != 0
		h.Params = decodeParams(params)
		m := map[string]any{
			"hook":                  &h,
			"binding_count":         n,
			"last_run_status":       status.String,
			"last_run_finished_at":  finishedAt.String,
			"last_run_content_hash": lastHash.String,
		}
		if status.Valid && lastHash.Valid && lastHash.String != h.ContentHash {
			m["content_changed_since_last_run"] = true
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Get returns the full hook (content included).
func (s *HookService) Get(ctx context.Context, id string) (*Hook, error) {
	var h Hook
	var content []byte
	var enabled int
	var params string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT `+hookMetaColumns+`,content FROM hook WHERE id=?`, id).
		Scan(&h.ID, &h.Name, &h.Description, &h.ContentHash, &h.Interpreter, &h.TimeoutSec, &params, &enabled, &h.CreatedAt, &h.UpdatedAt, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	h.Enabled = enabled != 0
	h.Params = decodeParams(params)
	h.Content = string(content)
	return &h, nil
}

// UpdateMeta edits registry metadata. Script content is untouched — use
// UpdateContent for that.
func (s *HookService) UpdateMeta(ctx context.Context, id string, name, description, interpreter *string, timeoutSec *int, enabled *bool, params *map[string]string) (*Hook, error) {
	h, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if name != nil {
		h.Name = *name
	}
	if description != nil {
		h.Description = *description
	}
	if interpreter != nil {
		h.Interpreter = *interpreter
	}
	if timeoutSec != nil {
		h.TimeoutSec = *timeoutSec
	}
	if enabled != nil {
		h.Enabled = *enabled
	}
	if params != nil {
		// Replace-all semantics (not merge): the client sends the complete
		// object each time. *map pointing at an empty map clears params.
		h.Params = *params
	}
	if err := validateHookMeta(h.Name, h.Interpreter, h.TimeoutSec); err != nil {
		return nil, err
	}
	if err := validateHookParams(h.Params); err != nil {
		return nil, err
	}
	// Interpreter removal must not leave a shebang-less script.
	if h.Interpreter == "" {
		if err := validateHookContent(h.Content, ""); err != nil {
			return nil, err
		}
	}
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hook WHERE name=? AND id<>?`, h.Name, id).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, NewCodedErrorDetail(CodeValidation, fmt.Sprintf("hook name %q already exists", h.Name), map[string]any{"name": h.Name})
	}
	en := 0
	if h.Enabled {
		en = 1
	}
	_, err = s.st.DB().ExecContext(ctx,
		`UPDATE hook SET name=?,description=?,interpreter=?,timeout_sec=?,params=?,enabled=?,updated_at=? WHERE id=?`,
		h.Name, h.Description, h.Interpreter, h.TimeoutSec, encodeParams(h.Params), en, now(), id)
	if err != nil {
		if coded := dupNameCodedError(err, CodeValidation, "hook", h.Name); coded != nil {
			return nil, coded
		}
		return nil, err
	}
	return s.Get(ctx, id)
}

// UpdateContent replaces the script (content_hash recomputed; the tripwire
// badge on the panel clears after the next run records the new hash).
func (s *HookService) UpdateContent(ctx context.Context, id, content string) (*Hook, error) {
	h, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := validateHookContent(content, h.Interpreter); err != nil {
		return nil, err
	}
	_, err = s.st.DB().ExecContext(ctx,
		`UPDATE hook SET content=?, content_hash=?, updated_at=? WHERE id=?`,
		[]byte(content), hashContent(content), now(), id)
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Delete removes the hook; bindings cascade (FK), hook_run history is kept
// (no FK by design — provenance survives deletion).
func (s *HookService) Delete(ctx context.Context, id string) error {
	res, err := s.st.DB().ExecContext(ctx, `DELETE FROM hook WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Runs returns a hook's run history, newest first.
func (s *HookService) Runs(ctx context.Context, hookID string, limit int) ([]HookRun, error) {
	if _, err := s.Get(ctx, hookID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,binding_id,goal_id,event,content_hash,status,exit_code,started_at,finished_at,output
		FROM hook_run WHERE hook_id=? ORDER BY started_at DESC, id DESC LIMIT ?`, hookID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HookRun{}
	for rows.Next() {
		var r HookRun
		if err := rows.Scan(&r.ID, &r.HookID, &r.BindingID, &r.GoalID, &r.Event, &r.ContentHash, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &r.Output); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ── bindings ──

// BindGoal attaches a hook to an event for one goal. Terminal goals are
// read-only (HTTP 409 via ErrConflict).
func (s *HookService) BindGoal(ctx context.Context, goalID, hookID, event string, seq int, params map[string]string) (*HookBinding, error) {
	if !validHookEvent(event) {
		return nil, NewValidationError("unknown hook event " + event)
	}
	// Hook existence first: a missing hook is a 404 even on a terminal
	// goal — the read-only conflict must not mask the not-found.
	if _, err := s.Get(ctx, hookID); err != nil {
		return nil, err
	}
	if err := validateHookParams(params); err != nil {
		return nil, err
	}
	var status string
	err := s.st.DB().QueryRowContext(ctx, `SELECT status FROM goal WHERE id=?`, goalID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if goalTerminalStatuses[status] {
		return nil, NewConflictError("goal is in a terminal state; hook bindings are read-only")
	}
	return s.insertBinding(ctx, "goal", goalID, hookID, event, seq, params)
}

// BindDomain attaches a hook as a domain-level default (inherited by every
// goal of the domain; a goal-level binding of the same hook+event overrides).
func (s *HookService) BindDomain(ctx context.Context, domainID, hookID, event string, seq int, params map[string]string) (*HookBinding, error) {
	if !validHookEvent(event) {
		return nil, NewValidationError("unknown hook event " + event)
	}
	if _, err := s.Get(ctx, hookID); err != nil {
		return nil, err
	}
	if err := validateHookParams(params); err != nil {
		return nil, err
	}
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM domain WHERE id=?`, domainID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.insertBinding(ctx, "domain", domainID, hookID, event, seq, params)
}

func (s *HookService) insertBinding(ctx context.Context, scope, scopeID, hookID, event string, seq int, params map[string]string) (*HookBinding, error) {
	if seq < 0 {
		return nil, NewValidationError("seq must be >= 0")
	}
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM hook WHERE id=?`, hookID).Scan(&n); err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, ErrNotFound
	}
	if err := validateHookParams(params); err != nil {
		return nil, err
	}
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM hook_binding WHERE hook_id=? AND scope=? AND scope_id=? AND event=?`,
		hookID, scope, scopeID, event).Scan(&n); err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, NewValidationError("hook is already bound to this event at this scope")
	}
	b := &HookBinding{ID: newID(), HookID: hookID, Scope: scope, ScopeID: scopeID, Event: event, Seq: seq, Params: params, CreatedAt: now()}
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO hook_binding (id,hook_id,scope,scope_id,event,seq,params,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		b.ID, b.HookID, b.Scope, b.ScopeID, b.Event, b.Seq, encodeParams(b.Params), b.CreatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Unbind removes a goal-scope binding (checked against the given goal).
func (s *HookService) UnbindGoal(ctx context.Context, goalID, bindingID string) error {
	return s.unbindScoped(ctx, "goal", goalID, bindingID)
}

// UnbindDomain removes a domain-scope binding (checked against the domain).
func (s *HookService) UnbindDomain(ctx context.Context, domainID, bindingID string) error {
	return s.unbindScoped(ctx, "domain", domainID, bindingID)
}

func (s *HookService) unbindScoped(ctx context.Context, scope, scopeID, bindingID string) error {
	res, err := s.st.DB().ExecContext(ctx,
		`DELETE FROM hook_binding WHERE id=? AND scope=? AND scope_id=?`, bindingID, scope, scopeID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GoalEffectiveHooks resolves the bindings that WOULD fire for each event of a
// goal: domain-level defaults (inherited) + goal-level, goal overriding
// domain on the same (hook, event). Used by the executor and GET
// /goals/{id}/hooks.
func (s *HookService) GoalEffectiveHooks(ctx context.Context, goalID string) ([]HookBinding, error) {
	var domainID, status string
	err := s.st.DB().QueryRowContext(ctx, `SELECT COALESCE(domain_id,''), status FROM goal WHERE id=?`, goalID).Scan(&domainID, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	out := []HookBinding{}
	if domainID != "" {
		rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,scope,scope_id,event,seq,params,created_at
			FROM hook_binding WHERE scope='domain' AND scope_id=? ORDER BY event, seq, created_at`, domainID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBinding(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, *b)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,scope,scope_id,event,seq,params,created_at
		FROM hook_binding WHERE scope='goal' AND scope_id=? ORDER BY event, seq, created_at`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	goalSeen := map[string]bool{}
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
		goalSeen[b.HookID+"\x00"+b.Event] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Goal-level binding overrides the domain-level one for the same hook+event.
	filtered := out[:0]
	for _, b := range out {
		if b.Scope == "domain" && goalSeen[b.HookID+"\x00"+b.Event] {
			continue
		}
		filtered = append(filtered, b)
	}
	// Decorate with hook names.
	for i := range filtered {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM hook WHERE id=?`, filtered[i].HookID).Scan(&name)
		filtered[i].HookName = name
	}
	return filtered, nil
}

// DomainHooks lists the domain-scope bindings.
func (s *HookService) DomainHooks(ctx context.Context, domainID string) ([]HookBinding, error) {
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,scope,scope_id,event,seq,params,created_at
		FROM hook_binding WHERE scope='domain' AND scope_id=? ORDER BY event, seq, created_at`, domainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HookBinding{}
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM hook WHERE id=?`, out[i].HookID).Scan(&name)
		out[i].HookName = name
	}
	return out, nil
}

func scanBinding(row interface{ Scan(dest ...any) error }) (*HookBinding, error) {
	var b HookBinding
	var params string
	if err := row.Scan(&b.ID, &b.HookID, &b.Scope, &b.ScopeID, &b.Event, &b.Seq, &params, &b.CreatedAt); err != nil {
		return nil, err
	}
	b.Params = decodeParams(params)
	return &b, nil
}

// effectiveBindingsFor resolves the ordered binding list for one event of one
// goal: domain-level first (seq asc), then goal-level (seq asc), goal
// overriding domain on the same hook.
func (s *HookService) effectiveBindingsFor(ctx context.Context, domainID, goalID, event string) ([]HookBinding, error) {
	out := []HookBinding{}
	if domainID != "" {
		rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,scope,scope_id,event,seq,params,created_at
			FROM hook_binding WHERE scope='domain' AND scope_id=? AND event=? ORDER BY seq, created_at`, domainID, event)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			b, err := scanBinding(rows)
			if err != nil {
				return nil, err
			}
			out = append(out, *b)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	rows, err := s.st.DB().QueryContext(ctx, `SELECT id,hook_id,scope,scope_id,event,seq,params,created_at
		FROM hook_binding WHERE scope='goal' AND scope_id=? AND event=? ORDER BY seq, created_at`, goalID, event)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	goalSeen := map[string]bool{}
	goalBindings := []HookBinding{}
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		goalBindings = append(goalBindings, *b)
		goalSeen[b.HookID] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, b := range out {
		if goalSeen[b.HookID] {
			continue
		}
		filtered = append(filtered, b)
	}
	return append(filtered, goalBindings...), nil
}

// ── executor ──

// onLifecycleEvent runs in its own goroutine per Publish (bus semantics), so
// different events/goals execute in parallel naturally. It resolves the
// effective bindings and fires them sequentially (spec §3.3).
func (s *HookService) onLifecycleEvent(e events.Event) {
	// NOTE: e's context belongs to the publisher (usually a canceled HTTP
	// request) — the executor must use its own context, like notify.go.
	ctx := context.Background()
	m, _ := e.Payload.(map[string]any)
	if m == nil {
		return
	}
	goalID, _ := m["goal_id"].(string)
	if goalID == "" {
		return
	}
	hookEvent := topicToHookEvent(e.Topic)
	if hookEvent == "" {
		return
	}
	title, domainID, status, createdByType, err := s.goalRow(ctx, goalID)
	if err != nil {
		// Goal deleted concurrently — nothing to fire for.
		logging.Debugf("hooks: goal %s lookup: %v", goalID, err)
		return
	}
	bindings, err := s.effectiveBindingsFor(ctx, domainID, goalID, hookEvent)
	if err != nil {
		logging.Errorf("hooks: effective bindings for goal %s: %v", goalID, err)
		return
	}
	if len(bindings) == 0 {
		return
	}
	logging.Infof("hooks: event %s for goal %s fires %d binding(s)", hookEvent, goalID, len(bindings))
	for _, b := range bindings {
		s.fireBinding(ctx, b, hookEvent, goalID, domainID, title, status, createdByType, m)
	}
}

func topicToHookEvent(topic string) string {
	for ev, t := range hookEventTopics {
		if t == topic {
			return ev
		}
	}
	return ""
}

// goalRow loads the display fields the stdin payload needs (title/domain).
func (s *HookService) goalRow(ctx context.Context, goalID string) (title, domainID, status, createdByType string, err error) {
	err = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(title,''), COALESCE(domain_id,''), COALESCE(status,''), COALESCE(created_by_type,'') FROM goal WHERE id=?`,
		goalID).Scan(&title, &domainID, &status, &createdByType)
	return
}

// fireBinding runs (or skips) one binding for one event, recording a
// hook_run either way.
func (s *HookService) fireBinding(ctx context.Context, b HookBinding, hookEvent, goalID, domainID, title, goalStatus, createdByType string, busPayload map[string]any) {
	runID := newID()
	hook, err := s.Get(ctx, b.HookID)
	if err != nil {
		// Hook deleted between resolution and fire — binding would have
		// cascaded; just skip.
		logging.Debugf("hooks: binding %s hook gone: %v", b.ID, err)
		return
	}
	// Skip checks (spec §1.3): both leave a skipped hook_run row.
	if !hook.Enabled {
		s.finishRun(ctx, &runRecord{ID: runID, Hook: hook, BindingID: b.ID, GoalID: goalID, Event: hookEvent,
			Status: "skipped", Output: "hook disabled"})
		return
	}
	if !hookTerminalEvents[hookEvent] && goalTerminalStatuses[goalStatus] {
		s.finishRun(ctx, &runRecord{ID: runID, Hook: hook, BindingID: b.ID, GoalID: goalID, Event: hookEvent,
			Status: "skipped", Output: "goal terminal"})
		return
	}
	// Run-time params: binding overrides shallow-merged over hook defaults.
	merged := mergeHookParams(hook.Params, b.Params)
	payload := s.buildPayload(hookEvent, goalID, title, domainID, createdByType, busPayload, merged)
	rec := &runRecord{ID: runID, Hook: hook, BindingID: b.ID, GoalID: goalID, Event: hookEvent, StartedAt: now()}
	if err := s.insertRunningRun(ctx, rec); err != nil {
		logging.Errorf("hooks: insert run %s: %v", runID, err)
		return
	}
	s.publishEvent("hook:started", map[string]any{
		"hook_run_id": runID, "hook_id": hook.ID, "hook_name": hook.Name,
		"binding_id": b.ID, "goal_id": goalID, "event": hookEvent, "status": "running",
	})
	// Per-hook global serialization: one script never races itself.
	lock := s.lockFor(hook.ID)
	lock.Lock()
	rec2 := s.executeScript(ctx, hook, runID, hookEvent, payload, merged)
	lock.Unlock()
	s.finishRun(ctx, rec2)
}

// buildPayload assembles the stdin JSON contract (spec §2): a fixed envelope
// plus the event-specific fields forwarded from the bus payload. The existing
// topic payloads are untouched — this layer adds title/domain_id/timestamp.
// merged params (if any) ride along as a top-level "params" object.
func (s *HookService) buildPayload(hookEvent, goalID, title, domainID, createdByType string, m map[string]any, params map[string]string) map[string]any {
	p := map[string]any{
		"platform":        "ouroflow",
		"hook_schema":     1,
		"hook_event_name": hookEvent,
		"goal_id":         goalID,
		"title":           title,
		"domain_id":       domainID,
		"timestamp":       now(),
	}
	if len(params) > 0 {
		p["params"] = params
	}
	switch hookEvent {
	case "goal_finished":
		p["status"] = stringOf(m["status"])
		p["summary"] = stringOf(m["summary"])
		p["created_by_type"] = createdByType
	case "goal_delivered", "goal_deliver_failed":
		p["note"] = stringOf(m["note"])
		p["commits"] = stringSlice(m["commits"])
	case "goal_reviewing":
		p["reason"] = stringOf(m["reason"])
		p["auto_approve"] = boolOf(m["auto_approve"])
	case "goal_review_resolved":
		p["decision"] = stringOf(m["decision"])
	}
	return p
}

func stringOf(v any) string {
	s, _ := v.(string)
	return s
}

func boolOf(v any) bool {
	b, _ := v.(bool)
	return b
}

// stringSlice normalizes []string and []any (defensive: the bus payload is an
// in-memory map, but tolerate a JSON-decoded shape too).
func stringSlice(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			out = append(out, stringOf(x))
		}
		return out
	}
	return []string{}
}

// runRecord is the internal accumulator for one execution.
type runRecord struct {
	ID         string
	Hook       *Hook
	BindingID  string
	GoalID     string
	Event      string
	Status     string
	ExitCode   *int
	StartedAt  string
	FinishedAt string
	Output     string
}

func (s *HookService) insertRunningRun(ctx context.Context, r *runRecord) error {
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO hook_run (id,hook_id,binding_id,goal_id,event,content_hash,status,exit_code,started_at,finished_at,output)
		VALUES (?,?,?,?,?,?, 'running', NULL, ?, '', '')`,
		r.ID, r.Hook.ID, r.BindingID, r.GoalID, r.Event, r.Hook.ContentHash, r.StartedAt)
	return err
}

// finishRun stamps the terminal status and publishes hook:completed. Used for
// real executions and skips alike — a skip is a terminal record too, so this
// UPSERTs: real runs already carry a running row (the UPDATE arm fires);
// skips never inserted one (the INSERT arm fires).
func (s *HookService) finishRun(ctx context.Context, r *runRecord) {
	r.FinishedAt = now()
	if r.StartedAt == "" {
		r.StartedAt = r.FinishedAt
	}
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO hook_run (id,hook_id,binding_id,goal_id,event,content_hash,status,exit_code,started_at,finished_at,output)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
			status=excluded.status, exit_code=excluded.exit_code,
			finished_at=excluded.finished_at, output=excluded.output`,
		r.ID, r.Hook.ID, r.BindingID, r.GoalID, r.Event, r.Hook.ContentHash,
		r.Status, r.ExitCode, r.StartedAt, r.FinishedAt, r.Output)
	if err != nil {
		logging.Errorf("hooks: finish run %s: %v", r.ID, err)
	}
	s.publishEvent("hook:completed", map[string]any{
		"hook_run_id": r.ID, "hook_id": r.Hook.ID, "hook_name": r.Hook.Name,
		"binding_id": r.BindingID, "goal_id": r.GoalID, "event": r.Event, "status": r.Status,
	})
}

// executeScript stages the script and runs it with the contract environment
// (spec §3): cwd = $OUROFLOW_HOME/runs/hooks/<run_id>, stdin = payload JSON,
// process-group SIGTERM → 2s → SIGKILL on timeout.
func (s *HookService) executeScript(ctx context.Context, hook *Hook, runID, hookEvent string, payload map[string]any, params map[string]string) *runRecord {
	rec := &runRecord{ID: runID, Hook: hook, Event: hookEvent, StartedAt: now()}

	dir := paths.HookRunDir(runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		rec.Status = "failed"
		rec.Output = fmt.Sprintf("hooks: mkdir: %v", err)
		return rec
	}
	scriptPath := filepath.Join(dir, "script")
	if err := os.WriteFile(scriptPath, []byte(hook.Content), 0o700); err != nil {
		rec.Status = "failed"
		rec.Output = fmt.Sprintf("hooks: stage script: %v", err)
		return rec
	}
	defer os.Remove(scriptPath) // DB is the truth; no script copy survives.

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		rec.Status = "failed"
		rec.Output = fmt.Sprintf("hooks: marshal payload: %v", err)
		return rec
	}

	timeout := time.Duration(hook.TimeoutSec) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var argv []string
	if hook.Interpreter == "" {
		argv = []string{scriptPath}
	} else {
		argv = []string{hook.Interpreter, scriptPath}
	}
	cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = hookEnv(hookEvent, runID, hook, payload["goal_id"], payload["domain_id"], params)
	cmd.Stdin = bytes.NewReader(payloadJSON)
	out := newTailWriter(hookMaxOutput)
	cmd.Stdout = out
	cmd.Stderr = out
	// Own process group so the timeout SIGTERM reaches children too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		return nil
	}
	cmd.WaitDelay = 2 * time.Second // SIGTERM grace, then exec SIGKILLs.

	runErr := cmd.Run()
	rec.Output = out.String()
	if runCtx.Err() == context.DeadlineExceeded || errors.Is(runErr, context.DeadlineExceeded) {
		rec.Status = "timeout"
		if rec.Output == "" {
			rec.Output = fmt.Sprintf("[hooks] timeout after %s", timeout)
		} else {
			rec.Output += fmt.Sprintf("\n[hooks] timeout after %s", timeout)
		}
		return rec
	}
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		code := ee.ExitCode()
		rec.Status = "failed"
		rec.ExitCode = &code
		return rec
	}
	if runErr != nil {
		// e.g. interpreter not found — launch failed before the script ran.
		rec.Status = "failed"
		rec.Output = strings.TrimSpace(rec.Output + "\n" + runErr.Error())
		return rec
	}
	rec.Status = "completed"
	zero := 0
	rec.ExitCode = &zero
	return rec
}

// hookEnv builds the minimal environment (spec §3.1): only the whitelist
// passes through from the daemon plus OUROFLOW_* contract vars. OUROFLOW_TOKEN
// / worker_token are deliberately NOT passed — hooks are owner-side actions
// and must not impersonate a run identity. Params reach the script three
// ways: the whole merged set as JSON (OUROFLOW_HOOK_PARAMS), each key
// flattened (OUROFLOW_PARAM_<KEY>), and inside the stdin payload.
func hookEnv(hookEvent, runID string, hook *Hook, goalID, domainID any, params map[string]string) []string {
	env := []string{
		"OUROFLOW_HOOK_EVENT=" + hookEvent,
		"OUROFLOW_HOOK_RUN_ID=" + runID,
		"OUROFLOW_HOOK_NAME=" + hook.Name,
		"OUROFLOW_HOOK_GOAL_ID=" + stringOf(goalID),
		"OUROFLOW_DOMAIN_ID=" + stringOf(domainID),
	}
	if len(params) > 0 {
		env = append(env, "OUROFLOW_HOOK_PARAMS="+encodeParams(params))
		for k, v := range params {
			envKey := "OUROFLOW_PARAM_" + strings.ToUpper(strings.ReplaceAll(k, "-", "_"))
			env = append(env, envKey+"="+v)
		}
	}
	serverURL := os.Getenv("OUROFLOW_SERVER_URL")
	if serverURL == "" {
		serverURL = hookDefaultServerURL
	}
	env = append(env, "OUROFLOW_SERVER_URL="+serverURL)
	for _, k := range []string{"PATH", "HOME", "LANG", "TZ", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// tailWriter keeps the LAST max bytes of everything written to it — the run
// output contract is a tail capture, so we never buffer unbounded output.
type tailWriter struct {
	mu        sync.Mutex
	buf       []byte
	max       int
	truncated bool
}

func newTailWriter(max int) *tailWriter {
	return &tailWriter{max: max}
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*t.max {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-t.max:]...)
		t.truncated = true
	}
	return len(p), nil
}

func (t *tailWriter) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.buf
	if len(out) > t.max {
		out = out[len(out)-t.max:]
		t.truncated = true
	}
	s := string(out)
	if t.truncated {
		s = "…[truncated]\n" + s
	}
	return s
}

// ── test run ──

// TestHook executes a hook once with a synthetic payload — the practical
// review tool (spec §4.2): run it and read the result instead of eyeballing
// the script. Records a hook_run like a real fire (binding_id empty).
func (s *HookService) TestHook(ctx context.Context, hookID, event string) (*HookRun, error) {
	// The executor must not inherit the HTTP request lifetime. A test run that
	// outlives a disconnected client would otherwise get its script killed
	// mid-flight AND its finishRun write dropped (canceled ctx) — leaving a
	// ghost `running` row until the next orphan sweep. Same NOTE as
	// onLifecycleEvent: hooks use their own context, like notify.go.
	ctx = context.Background()
	hook, err := s.Get(ctx, hookID)
	if err != nil {
		return nil, err
	}
	if event == "" {
		event = "goal_finished"
	}
	if !validHookEvent(event) {
		return nil, NewValidationError("unknown hook event " + event)
	}
	payload := map[string]any{
		"platform":        "ouroflow",
		"hook_schema":     1,
		"hook_event_name": event,
		"goal_id":         "test-goal",
		"title":           "Hook test run (synthetic payload)",
		"domain_id":       "",
		"status":          "done",
		"summary":         "synthetic payload generated by POST /hooks/{id}/test",
		"created_by_type": "human",
		"timestamp":       now(),
	}
	if len(hook.Params) > 0 {
		payload["params"] = hook.Params // test runs use hook-level defaults only (no binding)
	}
	runID := newID()
	rec := &runRecord{ID: runID, Hook: hook, Event: event, StartedAt: now()}
	if err := s.insertRunningRun(ctx, rec); err != nil {
		return nil, err
	}
	s.publishEvent("hook:started", map[string]any{
		"hook_run_id": runID, "hook_id": hook.ID, "hook_name": hook.Name,
		"binding_id": "", "goal_id": "", "event": event, "status": "running",
	})
	lock := s.lockFor(hook.ID)
	lock.Lock()
	rec2 := s.executeScript(ctx, hook, runID, event, payload, hook.Params)
	lock.Unlock()
	s.finishRun(ctx, rec2)
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	run.HookName = hook.Name
	return run, nil
}

func (s *HookService) getRun(ctx context.Context, runID string) (*HookRun, error) {
	var r HookRun
	err := s.st.DB().QueryRowContext(ctx, `SELECT id,hook_id,binding_id,goal_id,event,content_hash,status,exit_code,started_at,finished_at,output
		FROM hook_run WHERE id=?`, runID).
		Scan(&r.ID, &r.HookID, &r.BindingID, &r.GoalID, &r.Event, &r.ContentHash, &r.Status, &r.ExitCode, &r.StartedAt, &r.FinishedAt, &r.Output)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
