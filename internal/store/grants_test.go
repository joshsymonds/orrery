package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func grantRowOf(id string, granted time.Time, members ...GrantMember) GrantRow {
	return GrantRow{
		ID: id, Project: "/proj/" + id, ClientPID: 4242, ClientStart: 987654, Pools: `{"build":["a"]}`,
		Members: members, Granted: granted,
	}
}

func countRows(t *testing.T, s *Store, query string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestAddGrantStoresGrantAndMembersInAddOrder(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	row := grantRowOf("g1", engineNowAt(),
		GrantMember{Pool: "build", Member: "m2"}, GrantMember{Pool: "lint", Member: "m1"})
	if err := s.AddGrant(ctx, row); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if want := []GrantRow{row}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants: got %+v, want %+v", got, want)
	}
}

func TestGrantsOrdersByGrantedThenID(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	t1 := t0.Add(time.Minute)
	added := []GrantRow{
		grantRowOf("b", t1, GrantMember{Pool: "p", Member: "mb"}),
		grantRowOf("a", t1, GrantMember{Pool: "p", Member: "ma"}),
		grantRowOf("c", t0, GrantMember{Pool: "p", Member: "mc"}),
	}
	for _, row := range added {
		if err := s.AddGrant(ctx, row); err != nil {
			t.Fatalf("AddGrant %s: %v", row.ID, err)
		}
	}
	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if want := []GrantRow{added[2], added[1], added[0]}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants order: got %+v, want %+v", got, want)
	}
}

func TestAddGrantRefusesMemberAlreadyGrantedInAnyPool(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	first := grantRowOf("g1", t0,
		GrantMember{Pool: "build", Member: "m1"}, GrantMember{Pool: "build", Member: "m2"})
	if err := s.AddGrant(ctx, first); err != nil {
		t.Fatalf("AddGrant first: %v", err)
	}

	// m2 is held under pool build, so a second grant asking for it under pool lint is refused. Its other
	// member m3 is free, and must not be written by the refused grant.
	second := grantRowOf("g2", t0.Add(time.Second),
		GrantMember{Pool: "lint", Member: "m3"}, GrantMember{Pool: "lint", Member: "m2"})
	err := s.AddGrant(ctx, second)
	if !errors.Is(err, ErrMemberGranted) {
		t.Fatalf("AddGrant of held member: got %v, want ErrMemberGranted", err)
	}

	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if want := []GrantRow{first}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants after refusal: got %+v, want only the first grant %+v", got, want)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM grant_members"); n != 2 {
		t.Fatalf("grant_members after refusal: got %d rows, want 2", n)
	}
}

func TestEndGrantRemovesGrantAndFreesItsMembers(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	t0 := engineNowAt()
	first := grantRowOf("g1", t0,
		GrantMember{Pool: "build", Member: "m1"}, GrantMember{Pool: "build", Member: "m2"})
	if err := s.AddGrant(ctx, first); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	if err := s.EndGrant(ctx, "g1"); err != nil {
		t.Fatalf("EndGrant: %v", err)
	}

	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Grants after EndGrant: got %+v, want none", got)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM grants"); n != 0 {
		t.Fatalf("grants after EndGrant: got %d rows, want 0", n)
	}
	if n := countRows(t, s, "SELECT COUNT(*) FROM grant_members"); n != 0 {
		t.Fatalf("grant_members after EndGrant: got %d rows, want 0", n)
	}
	if err := s.AddGrant(ctx, grantRowOf("g2", t0, GrantMember{Pool: "build", Member: "m1"})); err != nil {
		t.Fatalf("AddGrant of a member the ended grant held: %v", err)
	}
}

func TestEndGrantUnknownIDReturnsNilAndKeepsOtherGrants(t *testing.T) {
	ctx := context.Background()
	s := openSyncStore(t)
	live := grantRowOf("g1", engineNowAt(), GrantMember{Pool: "build", Member: "m1"})
	if err := s.AddGrant(ctx, live); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	if err := s.EndGrant(ctx, "missing"); err != nil {
		t.Fatalf("EndGrant of unknown id: got %v, want nil", err)
	}
	got, err := s.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants: %v", err)
	}
	if want := []GrantRow{live}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants: got %+v, want %+v", got, want)
	}
}

func TestGrantsSurviveReopeningTheStore(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "orrery.db")
	row := grantRowOf("g1", engineNowAt(), GrantMember{Pool: "build", Member: "m1"})

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddGrant(ctx, row); err != nil {
		t.Fatalf("AddGrant: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open after close: %v", err)
	}
	t.Cleanup(func() {
		if err := reopened.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	got, err := reopened.Grants(ctx)
	if err != nil {
		t.Fatalf("Grants after reopen: %v", err)
	}
	if want := []GrantRow{row}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Grants after reopen: got %+v, want %+v", got, want)
	}
	err = reopened.AddGrant(ctx, grantRowOf("g2", engineNowAt(), GrantMember{Pool: "lint", Member: "m1"}))
	if !errors.Is(err, ErrMemberGranted) {
		t.Fatalf("AddGrant of member granted before reopen: got %v, want ErrMemberGranted", err)
	}
}
