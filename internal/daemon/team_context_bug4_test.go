package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
	"github.com/Sinkear/flowloop/internal/service"
	"github.com/Sinkear/flowloop/internal/store"
)

// TestOwnerContractTellsTheTruthAboutMaterialization: a pin alone must not
// tell the owner "the plan is COMPILED" (the incident's contract lie).
func TestOwnerContractTellsTheTruthAboutMaterialization(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "aw.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	d := &Daemon{st: st, bus: events.NewBus(), goalSvc: service.NewGoalService(st, events.NewBus())}

	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO domain (id,type,name,created_at) VALUES ('d1','scratch','d','`+ts+`')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO blueprint (id,name,created_at) VALUES ('bp1','bp','`+ts+`')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO blueprint_version (id,blueprint_id,version,content,content_hash,status,created_at)
		 VALUES ('bv1','bp1',1,'{}','h','published','`+ts+`')`); err != nil {
		t.Fatal(err)
	}
	insertGoal := func(id, pin string, withNode bool) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx,
			`INSERT INTO goal (id,title,domain_id,assignee_type,assignee_id,status,blueprint_version_id,created_at)
			 VALUES (?,?,?,'agent','a1','active',?,?)`, id, id, "d1", pin, ts); err != nil {
			t.Fatal(err)
		}
		if withNode {
			if _, err := st.DB().ExecContext(ctx,
				`INSERT INTO blueprint_node (id,goal_id,node_key,node_type,state,created_at)
				 VALUES (?,?, 'n1','task','running',?)`, "node-"+id, id, ts); err != nil {
				t.Fatal(err)
			}
		}
	}
	insertGoal("g-compiled", "bv1", true)
	insertGoal("g-empty", "bv1", false)

	compiled := d.buildFixedBlock(ctx, "g-compiled", "a1", "owner", "owner", "t", "", "scratch", "d")
	if !strings.Contains(compiled, "The work plan is COMPILED") {
		t.Fatalf("materialized plan must keep the compiled contract")
	}
	if strings.Contains(compiled, "PLAN NOT MATERIALIZED") {
		t.Fatalf("materialized plan must not claim otherwise")
	}
	empty := d.buildFixedBlock(ctx, "g-empty", "a1", "owner", "owner", "t", "", "scratch", "d")
	if !strings.Contains(empty, "PLAN NOT MATERIALIZED") {
		t.Fatalf("empty ledger must be stated, got:\n%s", empty)
	}
	if strings.Contains(empty, "The work plan is COMPILED") {
		t.Fatalf("empty ledger must not claim a compiled plan")
	}
	if !strings.Contains(empty, "do NOT route the work through consults") {
		t.Fatalf("empty ledger must forbid the consult detour")
	}
}
