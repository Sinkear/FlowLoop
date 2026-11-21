package main

// autopermit is the executor's ClientRequestHandler: the machine runs
// autonomously — nobody sits at the terminal to click "allow". The
// platform's verdicts (verification + gates + human checkpoints) judge
// the run's OUTCOME, not its individual tool calls, so every permission
// request is answered with the strongest allow option the CLI offers, and
// the delegated fs/terminal RPCs are implemented natively (CLIs that lack
// their own file/terminal tools delegate them to the client).

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/eushing/agentwork/internal/acp"
)

type autoTerm struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	out  bytes.Buffer
	done chan struct{}
}

type autopermit struct {
	mu    sync.Mutex
	terms map[string]*autoTerm
	cwd   string
	// readOnly enforces the consult role at the TOOL layer: the platform's
	// contract says a consulted agent answers with an opinion and produces no
	// work, so write-permissioned tools (fs write / terminal / edit|execute
	// permission requests) are denied here rather than trusted to the prompt.
	readOnly bool
}

func newAutopermit(cwd string, readOnly bool) *autopermit {
	return &autopermit{terms: map[string]*autoTerm{}, cwd: cwd, readOnly: readOnly}
}

// consultReadOnlyKind reports whether a tool-call kind writes (files, system,
// or external state). read/search/think are allowed for a consult; fetch is
// NOT: an outbound request can carry data out even without touching the file
// system, and a consult is an opinion, not an action.
func consultReadOnlyKind(kind string) bool {
	switch kind {
	case "edit", "delete", "move", "execute", "fetch":
		return true
	}
	return false
}

// HandleRequestPermission approves with the narrowest allow option the
// CLI offered (allow_once > allow_always); only-reject menus cancel the
// request so the tool call fails visibly instead of hanging.
func (a *autopermit) HandleRequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (*acp.RequestPermissionResponse, error) {
	var opts []string
	for _, o := range req.Options {
		opts = append(opts, string(o.Kind)+":"+o.OptionID)
	}
	cliLogf("autopermit: permission request tool=%q title=%q kind=%s options=%v", req.ToolCall.Title, req.ToolCall.Kind, req.ToolCall.Status, opts)
	if a.readOnly && consultReadOnlyKind(req.ToolCall.Kind) {
		cliLogf("autopermit: policy_violation consult read-only denied tool=%q kind=%s", req.ToolCall.Title, req.ToolCall.Kind)
		for _, o := range req.Options {
			if o.Kind == acp.PermissionRejectOnce || o.Kind == acp.PermissionRejectAlways {
				id := o.OptionID
				return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
					Outcome:  acp.PermissionOutcomeSelected,
					OptionID: &id,
				}}, nil
			}
		}
		return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome: acp.PermissionOutcomeCancelled,
		}}, nil
	}
	var allowAlways string
	for _, o := range req.Options {
		switch o.Kind {
		case acp.PermissionAllowOnce:
			id := o.OptionID
			cliLogf("autopermit: approved allow_once (%s)", id)
			return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
				Outcome:  acp.PermissionOutcomeSelected,
				OptionID: &id,
			}}, nil
		case acp.PermissionAllowAlways:
			allowAlways = o.OptionID
		}
	}
	if allowAlways != "" {
		cliLogf("autopermit: approved allow_always (%s)", allowAlways)
		return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
			Outcome:  acp.PermissionOutcomeSelected,
			OptionID: &allowAlways,
		}}, nil
	}
	cliLogf("autopermit: no allow option offered — cancelling")
	return &acp.RequestPermissionResponse{Outcome: acp.RequestPermissionOutcome{
		Outcome: acp.PermissionOutcomeCancelled,
	}}, nil
}

func (a *autopermit) HandleReadTextFile(ctx context.Context, req acp.ReadTextFileRequest) (*acp.ReadTextFileResponse, error) {
	cliLogf("autopermit: fs read %s", req.Path)
	b, err := os.ReadFile(req.Path)
	if err != nil {
		cliLogf("autopermit: fs read %s failed: %v", req.Path, err)
		return nil, err
	}
	return &acp.ReadTextFileResponse{Content: string(b)}, nil
}

func (a *autopermit) HandleWriteTextFile(ctx context.Context, req acp.WriteTextFileRequest) (*acp.WriteTextFileResponse, error) {
	cliLogf("autopermit: fs write %s (%d bytes)", req.Path, len(req.Content))
	if a.readOnly {
		cliLogf("autopermit: policy_violation consult read-only denied fs write %s", req.Path)
		return nil, fmt.Errorf("consult runs are read-only: file writes are denied by the platform")
	}
	if err := os.MkdirAll(filepath.Dir(req.Path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(req.Path, []byte(req.Content), 0o644); err != nil {
		cliLogf("autopermit: fs write %s failed: %v", req.Path, err)
		return nil, err
	}
	return &acp.WriteTextFileResponse{}, nil
}

// Terminal delegation: a minimal native shell per terminal id — spawn,
// poll (the buffer resets after each read; the CLI polls incrementally),
// wait, kill, release.

func (a *autopermit) HandleCreateTerminal(ctx context.Context, req acp.CreateTerminalRequest) (*acp.CreateTerminalResponse, error) {
	if a.readOnly {
		cliLogf("autopermit: policy_violation consult read-only denied terminal %q %v", req.Command, req.Args)
		return nil, fmt.Errorf("consult runs are read-only: terminal execution is denied by the platform")
	}
	cmd := exec.Command(req.Command, req.Args...)
	cmd.Dir = a.cwd
	env := os.Environ()
	for _, e := range req.Env {
		env = append(env, e.Name+"="+e.Value)
	}
	cmd.Env = env
	t := &autoTerm{cmd: cmd, done: make(chan struct{})}
	cmd.Stdout = &t.out
	cmd.Stderr = &t.out
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait(); close(t.done) }()
	a.mu.Lock()
	tid := fmt.Sprintf("term-%d", len(a.terms)+1)
	a.terms[tid] = t
	a.mu.Unlock()
	return &acp.CreateTerminalResponse{TerminalID: tid}, nil
}

func (a *autopermit) term(tid string) *autoTerm {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.terms[tid]
}

func (a *autopermit) HandleTerminalOutput(ctx context.Context, req acp.TerminalOutputRequest) (*acp.TerminalOutputResponse, error) {
	t := a.term(req.TerminalID)
	if t == nil {
		return nil, fmt.Errorf("terminal %s not found", req.TerminalID)
	}
	t.mu.Lock()
	out := t.out.String()
	t.out.Reset()
	exited := false
	select {
	case <-t.done:
		exited = true
	default:
	}
	t.mu.Unlock()
	resp := &acp.TerminalOutputResponse{Output: out, Truncated: false}
	if exited {
		code := 0
		resp.ExitStatus = &acp.TerminalExitStatus{ExitCode: &code}
	}
	return resp, nil
}

func (a *autopermit) HandleWaitForTerminalExit(ctx context.Context, req acp.WaitForTerminalExitRequest) (*acp.WaitForTerminalExitResponse, error) {
	t := a.term(req.TerminalID)
	if t == nil {
		return nil, fmt.Errorf("terminal %s not found", req.TerminalID)
	}
	<-t.done
	return &acp.WaitForTerminalExitResponse{}, nil
}

func (a *autopermit) HandleKillTerminal(ctx context.Context, req acp.KillTerminalRequest) (*acp.KillTerminalResponse, error) {
	t := a.term(req.TerminalID)
	if t == nil {
		return nil, fmt.Errorf("terminal %s not found", req.TerminalID)
	}
	_ = t.cmd.Process.Kill()
	return &acp.KillTerminalResponse{}, nil
}

func (a *autopermit) HandleReleaseTerminal(ctx context.Context, req acp.ReleaseTerminalRequest) (*acp.ReleaseTerminalResponse, error) {
	t := a.term(req.TerminalID)
	if t != nil {
		_ = t.cmd.Process.Kill()
	}
	a.mu.Lock()
	delete(a.terms, req.TerminalID)
	a.mu.Unlock()
	return &acp.ReleaseTerminalResponse{}, nil
}
