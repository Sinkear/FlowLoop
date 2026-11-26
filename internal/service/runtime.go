package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Sinkear/flowloop/internal/link"
	"github.com/Sinkear/flowloop/internal/store"
)

// Runtime is the launch spec of one probed agent CLI on one registered
// machine (CLI 分支): the machine spawns it with args (acp_spawn) when a
// run dispatches. The local-transport concepts (transport/provider/
// executable/endpoint) are retired — the daemon never opens a transport.
// The wire protocol is the machine's implementation detail (ACP today; a
// future a2a backend would live in the machine's executor).
type Runtime struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Args []string          `json:"args"`
	Env  map[string]string `json:"env"`
	// MachineID is the registered machine that executes this runtime's
	// runs (transport='flowloop'; '' = local/legacy transports).
	MachineID string `json:"machine_id,omitempty"`
	// Status: active | absent | inactive — absent = the machine's latest
	// probe no longer sees this CLI (uninstalled); the claim gate rejects
	// it. inactive = the machine is offline (heartbeat lost); List derives
	// this at read time via a LEFT JOIN on machine — the stored value is
	// active/absent only, so a reconnect auto-restores without a write.
	Status    string `json:"status"`
	CreatedAt string `json:"created_at"`
	// Models is the CLI's detected model list (probe-time detection, cached
	// on the row; [] = never detected or detection unavailable). ModelsAt is
	// the RFC3339 time of the last successful detection ('' = never).
	Models   []link.ModelInfo `json:"models"`
	ModelsAt string           `json:"models_at"`
	// ProbeStatus/ProbeError/ProbeAt record the connectivity outcome of the
	// last models refresh: '' = never probed, 'ok' = the default model
	// answered a minimal prompt, 'failed' = the provider endpoint was
	// unreachable (ProbeError carries the cause).
	ProbeStatus string `json:"probe_status,omitempty"`
	ProbeError  string `json:"probe_error,omitempty"`
	ProbeAt     string `json:"probe_at,omitempty"`
}

type RuntimeService struct{ st *store.Store }

func NewRuntimeService(st *store.Store) *RuntimeService { return &RuntimeService{st: st} }

func (s *RuntimeService) Create(ctx context.Context, r Runtime) (*Runtime, error) {
	if r.Name == "" {
		return nil, NewFieldRequiredError("name")
	}
	if r.MachineID == "" {
		return nil, NewFieldRequiredError("machine_id")
	}
	if r.Args == nil {
		r.Args = []string{}
	}
	if r.Env == nil {
		r.Env = map[string]string{}
	}
	// Models are PROBE-DETECTED, never caller-supplied: a manually created
	// runtime starts with an empty list and gets one at the first probe.
	r.Models = []link.ModelInfo{}
	r.ModelsAt = ""
	r.ID = newID()
	r.CreatedAt = now()
	argsJSON, _ := json.Marshal(r.Args)
	envJSON, _ := json.Marshal(r.Env)
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT INTO runtime (id,name,machine_id,args,env,models,models_at,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		r.ID, r.Name, r.MachineID, string(argsJSON), string(envJSON), "[]", "", r.CreatedAt)
	if err != nil {
		if ve := dupNameCodedError(err, CodeRuntimeNameExists, "runtime", r.Name); ve != nil {
			return nil, ve
		}
		return nil, fmt.Errorf("insert runtime: %w", err)
	}
	return &r, nil
}

func (s *RuntimeService) List(ctx context.Context) ([]Runtime, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT r.id, r.name, r.machine_id, r.args, r.env,
		        CASE WHEN m.status='offline' THEN 'inactive' ELSE r.status END,
		        r.created_at, r.models, r.models_at,
		        r.probe_status, r.probe_error, r.probe_at
		 FROM runtime r
		 LEFT JOIN machine m ON m.id = r.machine_id
		 ORDER BY r.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runtime{}
	for rows.Next() {
		var r Runtime
		var argsJSON, envJSON, modelsJSON string
		if err := rows.Scan(&r.ID, &r.Name, &r.MachineID, &argsJSON, &envJSON, &r.Status, &r.CreatedAt, &modelsJSON, &r.ModelsAt, &r.ProbeStatus, &r.ProbeError, &r.ProbeAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(argsJSON), &r.Args)
		_ = json.Unmarshal([]byte(envJSON), &r.Env)
		if err := json.Unmarshal([]byte(modelsJSON), &r.Models); err != nil || r.Models == nil {
			r.Models = []link.ModelInfo{}
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *RuntimeService) Get(ctx context.Context, id string) (*Runtime, error) {
	var r Runtime
	var argsJSON, envJSON, modelsJSON string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,name,machine_id,args,env,created_at,models,models_at,probe_status,probe_error,probe_at FROM runtime WHERE id=?`, id).
		Scan(&r.ID, &r.Name, &r.MachineID, &argsJSON, &envJSON, &r.CreatedAt, &modelsJSON, &r.ModelsAt,
			&r.ProbeStatus, &r.ProbeError, &r.ProbeAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(argsJSON), &r.Args)
	_ = json.Unmarshal([]byte(envJSON), &r.Env)
	if err := json.Unmarshal([]byte(modelsJSON), &r.Models); err != nil || r.Models == nil {
		r.Models = []link.ModelInfo{}
	}
	return &r, nil
}

// RuntimeModels is the GET /runtimes/{id}/models response: the runtime's
// identity (name, CLI slug, machine) plus its detected model list and the
// detection timestamp. cli = the name segment before '@' ("claude" for
// "claude@host"). detected_at is empty when detection never succeeded.
type RuntimeModels struct {
	RuntimeID  string           `json:"runtime_id"`
	Name       string           `json:"name"`
	CLI        string           `json:"cli"`
	MachineID  string           `json:"machine_id"`
	Models     []link.ModelInfo `json:"models"`
	DetectedAt string           `json:"detected_at"`
	// Connectivity of the last refresh, see Runtime.ProbeStatus.
	ProbeStatus string `json:"probe_status,omitempty"`
	ProbeError  string `json:"probe_error,omitempty"`
	ProbeAt     string `json:"probe_at,omitempty"`
}

// Models returns the runtime's detected model list (persisted at probe
// time). [] when detection never succeeded — never nil, so the web's model
// picker can render an empty state without nil checks.
func (s *RuntimeService) Models(ctx context.Context, id string) (*RuntimeModels, error) {
	var out RuntimeModels
	var modelsJSON string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,name,machine_id,models,models_at,probe_status,probe_error,probe_at FROM runtime WHERE id=?`, id).
		Scan(&out.RuntimeID, &out.Name, &out.MachineID, &modelsJSON, &out.DetectedAt,
			&out.ProbeStatus, &out.ProbeError, &out.ProbeAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if i := strings.IndexByte(out.Name, '@'); i >= 0 {
		out.CLI = out.Name[:i]
	} else {
		out.CLI = out.Name
	}
	out.Models = []link.ModelInfo{}
	if err := json.Unmarshal([]byte(modelsJSON), &out.Models); err != nil || out.Models == nil {
		out.Models = []link.ModelInfo{}
	}
	return &out, nil
}

// SetModels overwrites a runtime's persisted model list — the authoritative
// write behind POST /runtimes/{id}/models/refresh (the machine re-detected
// the list, so an empty result must clear stale ids rather than be ignored
// like the probe-report path's empty-means-unavailable semantics). Returns
// the fresh RuntimeModels in the same shape as Models.
func (s *RuntimeService) SetModels(ctx context.Context, id string, models []link.ModelInfo, detectedAt string) (*RuntimeModels, error) {
	if models == nil {
		models = []link.ModelInfo{}
	}
	modelsJSON, err := json.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("marshal models: %w", err)
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE runtime SET models=?, models_at=? WHERE id=?`, string(modelsJSON), detectedAt, id)
	if err != nil {
		return nil, fmt.Errorf("update runtime models: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Models(ctx, id)
}

// SetProbe records the connectivity outcome of the last model refresh:
// 'ok' when a minimal prompt got an answer, 'failed' with the provider error
// otherwise. Legacy machines that cannot run the probe never call this, so
// their previous health (if any) stays untouched.
func (s *RuntimeService) SetProbe(ctx context.Context, id string, ok bool, probeError, at string) (*RuntimeModels, error) {
	status := "failed"
	if ok {
		status = "ok"
		probeError = ""
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE runtime SET probe_status=?, probe_error=?, probe_at=? WHERE id=?`, status, probeError, at, id)
	if err != nil {
		return nil, fmt.Errorf("update runtime probe: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Models(ctx, id)
}

func (s *RuntimeService) Delete(ctx context.Context, id string) error {
	// Refuse if agents still reference this runtime. Cascading would silently
	// orphan agents; the caller should delete its agents first.
	var n int
	if err := s.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM agent WHERE runtime_id=?`, id).Scan(&n); err != nil {
		return fmt.Errorf("check agents: %w", err)
	}
	if n > 0 {
		var name string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT name FROM runtime WHERE id=?`, id).Scan(&name)
		if name == "" {
			name = id
		}
		return NewCodedErrorDetail(CodeRuntimeHasAgents,
			fmt.Sprintf("runtime %q has %d agent(s); delete its agents first", name, n),
			map[string]any{"name": name, "count": n})
	}
	_, err := s.st.DB().ExecContext(ctx, `DELETE FROM runtime WHERE id=?`, id)
	return err
}
