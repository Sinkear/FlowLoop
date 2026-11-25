package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Sinkear/flowloop/internal/link"
)

// fakeRefreshPeer registers a machine peer that answers machine.models_refresh
// with the given result and records the requested CLI.
func fakeRefreshPeer(t *testing.T, d *Daemon, machineID string, result link.ModelsRefreshResult, rpcErr *link.RPCError) <-chan string {
	t.Helper()
	asked := make(chan string, 4)
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	registered := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		peer := link.NewPeer(conn)
		peer.Handle(link.MethodModelsRefresh, func(_ context.Context, raw json.RawMessage) (any, *link.RPCError) {
			var p link.ModelsRefreshParams
			if err := json.Unmarshal(raw, &p); err != nil {
				return nil, &link.RPCError{Code: link.CodeInvalidParams, Message: err.Error()}
			}
			asked <- p.CLI
			return result, rpcErr
		})
		close(registered)
	}))
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		srv.Close()
		t.Fatalf("dial fake machine: %v", err)
	}
	<-registered
	d.RegisterMachinePeer(machineID, link.NewPeer(conn))
	t.Cleanup(func() {
		if p := d.MachinePeer(machineID); p != nil {
			p.Close()
		}
		conn.Close()
		srv.CloseClientConnections()
		srv.Close()
	})
	return asked
}

// TestRefreshRuntimeModelsOffline: no live machine peer → ErrMachineOffline,
// which the HTTP layer turns into a 409 instead of a generic probe failure.
func TestRefreshRuntimeModelsOffline(t *testing.T) {
	d, _, _ := newSkillPushDaemon(t)
	_, err := d.RefreshRuntimeModels(context.Background(), "m-offline", "hermes")
	if !errors.Is(err, ErrMachineOffline) {
		t.Fatalf("expected ErrMachineOffline, got %v", err)
	}
}

// TestRefreshRuntimeModelsForwardsAndReturns: the daemon sends the CLI id to
// the machine over machine.models_refresh and hands back its authoritative
// model list + detection timestamp untouched.
func TestRefreshRuntimeModelsForwardsAndReturns(t *testing.T) {
	d, _, _ := newSkillPushDaemon(t)
	want := link.ModelsRefreshResult{
		Models:     []link.ModelInfo{{ID: "m1", Name: "One"}, {ID: "m2"}},
		DetectedAt: "2026-11-23T10:00:00Z",
	}
	asked := fakeRefreshPeer(t, d, "m1", want, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := d.RefreshRuntimeModels(ctx, "m1", "hermes")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if got == nil || len(got.Models) != 2 || got.Models[0].ID != "m1" || got.DetectedAt != want.DetectedAt {
		t.Fatalf("refresh result mismatch: %+v", got)
	}
	select {
	case cli := <-asked:
		if cli != "hermes" {
			t.Fatalf("machine was asked to refresh %q, want hermes", cli)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("machine never received machine.models_refresh")
	}
}

// TestRefreshRuntimeModelsProbeFailurePropagates: a machine-side probe
// failure must surface as an error (HTTP 502) instead of looking like an
// authoritative empty catalog — the caller keeps the previous model list.
func TestRefreshRuntimeModelsProbeFailurePropagates(t *testing.T) {
	d, _, _ := newSkillPushDaemon(t)
	asked := fakeRefreshPeer(t, d, "m1", link.ModelsRefreshResult{}, &link.RPCError{
		Code:    link.CodeInternal,
		Message: "model detection failed for CLI %q",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got, err := d.RefreshRuntimeModels(ctx, "m1", "hermes")
	if err == nil {
		t.Fatalf("expected probe failure to propagate, got %+v", got)
	}
	if !strings.Contains(err.Error(), "machine models refresh") {
		t.Fatalf("error must be wrapped by the daemon, got %v", err)
	}
	select {
	case <-asked:
	case <-time.After(3 * time.Second):
		t.Fatal("machine never received machine.models_refresh")
	}
}
