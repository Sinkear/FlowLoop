package service

import (
	"context"
	"testing"
)

func TestProjectSquadMembershipIndependentOfConnection(t *testing.T) {
	ctx := context.Background()
	conn, _, domainID, squadID := connectionFixture(t)
	ps := NewProjectSquadService(conn.st)

	// Joining a project needs no repo/events/connection.
	m, err := ps.Add(ctx, domainID, squadID)
	if err != nil {
		t.Fatalf("add membership: %v", err)
	}
	if m.DomainID != domainID || m.SquadID != squadID {
		t.Fatalf("membership = %+v", m)
	}
	list, err := ps.List(ctx, domainID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list membership = %v, %v", list, err)
	}

	// Idempotent: adding twice does not duplicate.
	if _, err := ps.Add(ctx, domainID, squadID); err != nil {
		t.Fatalf("add membership twice: %v", err)
	}
	if list, _ := ps.List(ctx, domainID); len(list) != 1 {
		t.Fatalf("membership duplicated: %v", list)
	}

	// A repo connection implies membership and cannot be removed while it
	// exists — otherwise webhooks would keep firing for an unlisted squad.
	if _, err := conn.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID}); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := ps.Remove(ctx, domainID, squadID); err == nil {
		t.Fatal("remove must be refused while a connection exists")
	}
	var connectionID string
	if err := conn.st.DB().QueryRowContext(ctx,
		`SELECT id FROM connection WHERE domain_id=? AND squad_id=?`, domainID, squadID).Scan(&connectionID); err != nil {
		t.Fatalf("read connection id: %v", err)
	}
	if err := conn.Delete(ctx, connectionID); err != nil {
		t.Fatalf("delete connection: %v", err)
	}
	if err := ps.Remove(ctx, domainID, squadID); err != nil {
		t.Fatalf("remove membership after connection deletion: %v", err)
	}
	if list, _ := ps.List(ctx, domainID); len(list) != 0 {
		t.Fatalf("membership should be gone: %v", list)
	}
}
