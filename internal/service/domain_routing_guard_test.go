package service

import (
	"context"
	"testing"

	"github.com/Sinkear/flowloop/internal/events"
)

// Deleting a project must remove its association rows too. They have no FK to
// domain, and a leftover project_squad row would make the squad undeletable
// forever (SquadService.Delete refuses while the row exists, and the project
// page that could remove it is gone).
func TestDomainDeleteCleansProjectLinks(t *testing.T) {
	ctx := context.Background()
	conn, ds, domainID, squadID := connectionFixture(t)
	if _, err := conn.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID}); err != nil {
		t.Fatalf("create connection: %v", err)
	}

	if err := ds.Delete(ctx, domainID); err != nil {
		t.Fatalf("delete project: %v", err)
	}
	for _, q := range []string{
		`SELECT COUNT(*) FROM project_squad WHERE domain_id=?`,
		`SELECT COUNT(*) FROM connection WHERE domain_id=?`,
	} {
		var n int
		if err := conn.st.DB().QueryRowContext(ctx, q, domainID).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", q, err)
		}
		if n != 0 {
			t.Fatalf("%q left %d orphan row(s)", q, n)
		}
	}

	// With the project gone, the squad has no dangling project reference and
	// must be deletable again.
	if err := NewSquadService(conn.st, events.NewBus()).Delete(ctx, squadID); err != nil {
		t.Fatalf("squad must be deletable after project delete: %v", err)
	}
}

// Squads are public resources: a project may route its issues/PRs to any
// existing squad without first "joining" it to the project.
func TestProjectSquadRoutingWithoutMembership(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	ds := NewDomainService(st, bus)
	leader := seedAgent(t, st, "routing-leader")
	sq, err := NewSquadService(st, bus).Create(ctx, Squad{Name: "routing-squad", LeaderID: leader})
	if err != nil {
		t.Fatalf("create squad: %v", err)
	}
	d, err := ds.Create(ctx, Domain{
		Name:   "routing-project",
		GitURL: "https://github.com/acme/routing.git",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	if _, err := ds.SetAssignees(ctx, d.ID, sq.ID, "squad", "", ""); err != nil {
		t.Fatalf("public squad routing must succeed without membership: %v", err)
	}

	// A later project edit must keep the routing and must not require the
	// polling token just because routing exists (webhook-only projects).
	old, err := ds.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	old.Name = "routing-project-renamed"
	old.GitCredentials = ""
	updated, err := ds.Update(ctx, d.ID, *old)
	if err != nil {
		t.Fatalf("tokenless project update must succeed: %v", err)
	}
	if updated.IssueAssignee != sq.ID || updated.IssueAssigneeType != "squad" {
		t.Fatalf("update dropped routing: %+v", updated)
	}
}
