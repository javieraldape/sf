package store

import (
	"errors"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/domain"
)

func TestProviderThreeCapacityRetainsFourthAdmissionExclusion(t *testing.T) {
	db, ctx := openTestStore(t)
	digest := setupProviderProject(t, db, ctx)
	leader, err := db.AcquireLeader(ctx, domain.ChannelDev, "three-capacity")
	if err != nil {
		t.Fatal(err)
	}
	builder, _ := setupProviderPair(t, db, ctx)
	var tickets []Ticket
	for _, id := range []string{"SF-three-a", "SF-three-b", "SF-three-c", "SF-three-d"} {
		tickets = append(tickets, providerState(t, db, ctx, setupProviderTicket(t, db, ctx, id, leader), leader, domain.StateBuilding))
	}
	begin := func(ticket Ticket) (ProviderAttemptClaim, error) {
		return db.BeginProviderAttempt(ctx, supervised(t, ProviderAttemptRequest{Ref: ticket.Ref, ExpectedVersion: ticket.Version, Fence: domain.Fence{LeaderEpoch: leader, RunnerEpoch: ticket.RunnerEpoch}, Phase: domain.PhaseBuild, Role: "builder", Binding: runtime(builder), ConfigDigest: digest, Capacity: 3, At: time.Now().UTC()}))
	}
	var claims []ProviderAttemptClaim
	for _, ticket := range tickets[:3] {
		claim, err := begin(ticket)
		if err != nil {
			t.Fatal(err)
		}
		claims = append(claims, claim)
	}
	if active, err := db.ActiveProviderAttempts(ctx, domain.ChannelDev); err != nil || len(active) != 3 {
		t.Fatalf("active=%v err=%v; want three", active, err)
	}
	if _, err := begin(tickets[3]); !errors.Is(err, ErrProviderCapacity) {
		t.Fatalf("fourth admission=%v; want capacity refusal", err)
	}
	second := tickets[1]
	if err := db.FinishProviderAttempt(ctx, claims[1], proof(t, claims[1]), second.Version, domain.Fence{LeaderEpoch: leader, RunnerEpoch: second.RunnerEpoch}, "failed", "failed", 1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := begin(tickets[3]); err != nil {
		t.Fatal(err)
	}
	if active, err := db.ActiveProviderAttempts(ctx, domain.ChannelDev); err != nil || len(active) != 3 {
		t.Fatalf("replacement active=%v err=%v; want three", active, err)
	}
}
