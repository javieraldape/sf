package workflowruntime

import (
	"context"
	"testing"

	"github.com/nysa-company/sf/internal/domain"
	"github.com/nysa-company/sf/internal/store"
)

// An unchanged external-wait ticket remains eligible, but must not monopolize
// every subsequent tick while later tickets have work to do.
func TestSchedulerRotatesPastUnchangedWaitingTicket(t *testing.T) {
	waiting := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "a-wait"}, domain.StateWaitingManualMerge)
	second := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "b-build"}, domain.StateBuilding)
	third := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "c-plan"}, domain.StatePlanning)
	worker := &fakeWorker{}
	scheduler := NewScheduler(domain.ChannelDev, fakeTickets{tickets: []store.Ticket{third, waiting, second}}, &fakeEnsure{}, worker)
	for _, want := range []domain.TicketRef{waiting.Ref, second.Ref, third.Ref, waiting.Ref} {
		result := scheduler.Tick(context.Background(), domain.Fence{LeaderEpoch: 9})
		if result.Outcome != OutcomeInvoked || result.Ref != want {
			t.Fatalf("fair scheduling: got %s (%s), want %s", result.Ref.Ticket, result.Outcome, want.Ticket)
		}
	}
}
