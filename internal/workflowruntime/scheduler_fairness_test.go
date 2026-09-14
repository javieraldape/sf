package workflowruntime

import (
	"context"
	"errors"
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

func TestSchedulerFairnessRetainsQueuedAndStoppedExclusion(t *testing.T) {
	first := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "a"}, domain.StatePlanning)
	stopped := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "b"}, domain.StateBuilding)
	queued := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "c"}, domain.StateQueued)
	last := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "b", Ticket: "d"}, domain.StateVerifying)
	source := []store.Ticket{last, queued, stopped, first}
	worker := &fakeWorker{}
	scheduler := NewScheduler(domain.ChannelDev, fakeTickets{tickets: source}, &fakeEnsure{}, worker)
	if err := scheduler.admission.Stop(context.Background(), stopped.Ref); err != nil {
		t.Fatal(err)
	}
	for _, want := range []domain.TicketRef{first.Ref, last.Ref, first.Ref, last.Ref} {
		got := scheduler.Tick(context.Background(), domain.Fence{LeaderEpoch: 9})
		if got.Outcome != OutcomeInvoked || got.Ref != want {
			t.Fatalf("got %v/%s, want %v", got.Ref, got.Outcome, want)
		}
	}
	if source[0].Ref != last.Ref || source[3].Ref != first.Ref {
		t.Fatal("scheduler mutated source ordering")
	}
}

func TestSchedulerRotatesAfterReadinessFailure(t *testing.T) {
	first := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "a"}, domain.StatePlanning)
	second := ticket(domain.TicketRef{Channel: domain.ChannelDev, Project: "a", Ticket: "b"}, domain.StatePlanning)
	worker := &fakeWorker{}
	scheduler := NewScheduler(domain.ChannelDev, fakeTickets{tickets: []store.Ticket{first, second}}, &fakeEnsure{err: errors.New("unavailable checkout")}, worker)
	for _, want := range []domain.TicketRef{first.Ref, second.Ref, first.Ref} {
		got := scheduler.Tick(context.Background(), domain.Fence{LeaderEpoch: 9})
		if got.Ref != want || got.Err == nil {
			t.Fatalf("readiness rotation got %v/%v, want %v/error", got.Ref, got.Err, want)
		}
	}
	if len(worker.calls) != 0 {
		t.Fatal("readiness failure invoked worker")
	}
}
