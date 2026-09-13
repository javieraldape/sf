package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/api"
	"github.com/nysa-company/sf/internal/authoring"
	"github.com/nysa-company/sf/internal/contracts"
	"github.com/nysa-company/sf/internal/domain"
)

// This is a connected CLI journey against a synthetic shared-state service.
// It exercises no model, database, provider runtime, or live daemon.
func TestTicketConnectedAuthoringSaveStartViewWatchJourney(t *testing.T) {
	path := filepath.Join(t.TempDir(), "draft.md")
	draft := contracts.AuthoringResult{Kind: "draft", Title: "Count jobs", Problem: "Expose an in-memory job count.", Scope: []string{}, Acceptance: []string{"Empty returns zero"}, Assumptions: []string{}}
	source, err := authoring.Markdown(draft)
	if err != nil {
		t.Fatal(err)
	}
	a, prompts := entryApp(t, "Count jobs\nsend\nsave\nleave\n")
	output := a.out.(*bytes.Buffer)
	item := selectableTicket{ID: runTestID, Title: draft.Title, Project: "app", Channel: domain.ChannelStable, State: domain.StateQueued}
	created, started, sessionCreated := false, false, false
	mutations, selections, views, polls := 0, 0, 0, 0
	var saved []byte
	var turnKey string
	base := time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC)
	var quietStartedEvents int
	client := fakeClient(func(_ context.Context, request api.Request) (api.Response, error) {
		var parameters map[string]any
		if json.Unmarshal(request.Parameters, &parameters) != nil {
			t.Fatal("invalid request parameters")
		}
		response := api.Response{Version: api.Version, RequestID: request.RequestID, OK: true}
		var data any
		switch request.Method {
		case "authoring.create":
			if sessionCreated || created || parameters["project"] != "app" {
				t.Fatal("invalid session creation order")
			}
			sessionCreated = true
			data = map[string]any{"authoring_session": authoringSessionView{ID: "journey-session", Channel: "stable", Project: "app", Purpose: "ticket_draft", Capability: contracts.AuthoringCapability{Identity: domain.ProviderIdentity{Provider: "claude", Model: "opus"}}, ContextDigest: contracts.AuthoringDigest(nil)}}
		case "authoring.turn":
			key, ok := parameters["key"].(string)
			if !ok || key == "" || !sessionCreated || turnKey != "" || parameters["session"] != "journey-session" || !strings.Contains(prompts.String(), "Send this authoring turn to Claude? Type send, or cancel:") || !strings.Contains(prompts.String(), "ticket new --status journey-session --turn "+key) {
				t.Fatal("turn lacked known recovery identity or consent ordering")
			}
			turnKey = key
			data = map[string]any{"authoring_turn": authoringTurnView{Channel: "stable", Project: "app", Purpose: "ticket_draft", Session: "journey-session", Key: key, State: "completed", Outcome: "success", Result: &draft}}
		case "ticket.submit":
			if created || len(saved) == 0 || parameters["project"] != "app" || parameters["source"] != string(saved) || string(saved) != source {
				t.Fatal("submission was not the saved reviewed draft")
			}
			created = true
			mutations++
			response = runTestResponse(item.State, "ticket_submit", false)
		case "ticket.start":
			if !created || started || request.Ticket != item.ID || parameters["accept_cost_estimates"] != true {
				t.Fatal("start missing exact submitted identity or explicit cost acceptance")
			}
			started = true
			item.State = domain.StatePlanning
			mutations++
			response = runTestResponse(item.State, "ticket_start", false)
		case "ticket.status":
			if !started || parameters["project"] != item.Project || request.Ticket != "" {
				t.Fatal("selection escaped shared project inventory")
			}
			selections++
			if selections > 2 {
				t.Fatal("unexpected extra inventory read")
			}
			data = map[string]any{"channel": item.Channel, "tickets": []selectableTicket{item}}
		case "ticket.show":
			if !started || request.Ticket != item.ID || parameters["section"] != "all" || selections != 1 {
				t.Fatal("view did not resolve submitted identity")
			}
			views++
			data = map[string]any{"ticket": item.ID, "title": item.Title, "project": item.Project, "channel": item.Channel, "state": item.State, "artifacts": map[string]any{"draft": map[string]any{"available": true, "text": string(saved), "provenance": "submitted ticket"}}}
		case "ticket.activity":
			if !started || views != 1 || selections != 2 || request.Ticket != item.ID || mutations != 2 {
				t.Fatal("watch escaped the selected started ticket")
			}
			polls++
			if polls > 5 {
				t.Fatal("watch failed to stop on terminal snapshot")
			}
			wantEpoch, wantSequence := float64(7), float64(1)
			if polls == 1 {
				wantEpoch, wantSequence = 0, 0
			}
			if parameters["after_epoch"] != wantEpoch || parameters["after_sequence"] != wantSequence {
				t.Fatal("watch did not preserve its cursor through quiet/disconnect")
			}
			if polls == 2 {
				quietStartedEvents = strings.Count(output.String(), activityEventLabel("sf", "process_started"))
			}
			if polls == 3 {
				if quietStartedEvents != 1 || strings.Count(output.String(), activityEventLabel("sf", "process_started")) != quietStartedEvents || mutations != 2 {
					t.Fatal("quiet poll fabricated a process event or mutation")
				}
				return failure("daemon_unavailable", "fixture disconnected", []string{"sf", "factory", "run"}), nil
			}
			activity := liveTicketActivity{Available: true, Current: true, Running: true, DaemonEpoch: 7, Sequence: 1, MonitorAt: base.Add(time.Duration(polls) * time.Minute), Phase: "planning", Attempt: 1, Provider: "claude", Model: "opus", Observation: contracts.ActivitySnapshot{Available: true, Sequence: 1, StartedAt: base, LastOutputAt: base, LastFrameAt: base, LastEventAt: base, StdoutBytes: 32, DetailAvailable: true}}
			if polls == 1 {
				activity.Observation.Events = []contracts.ActivityEvent{{Sequence: 1, At: base, Source: "sf", Kind: "process_started"}}
			}
			if polls == 5 {
				item.State = domain.StateDone
				activity.Current, activity.Running, activity.Historical = false, false, true
				activity.Observation.ExitedAt = base.Add(4 * time.Minute)
			}
			data = map[string]any{"ticket": item, "activity": activity}
		default:
			t.Fatalf("unexpected operation in connected journey: %s", request.Method)
		}
		if data != nil {
			response.Data, _ = json.Marshal(data)
		}
		return response, nil
	})
	a.client = client
	executeEntry(t, a, "ticket", "new", path, "--project", "app", "--model", "opus")
	saved, err = os.ReadFile(path)
	if err != nil || string(saved) != source || !strings.Contains(prompts.String(), source) || turnKey == "" || created || mutations != 0 {
		t.Fatal("preview/save implicitly submitted or changed reviewed bytes")
	}
	runCommand := func(args ...string) {
		// Public commands get fresh app/flag/response state, but retain the same
		// synthetic service and reviewed file throughout this journey.
		a = newApp(client, output, prompts)
		a.interactive = func() bool { return true }
		a.input = strings.NewReader("")
		executeEntry(t, a, args...)
	}
	runCommand("ticket", "start", "--file", path, "--project", "app", "--accept-cost-estimates")
	if a.last == nil || !a.last.OK || !started || mutations != 2 {
		t.Fatal("explicit submit/start composition failed")
	}
	runCommand("ticket", "view", "012345", "--project", "app")
	if a.last == nil || !a.last.OK || views != 1 {
		t.Fatal("scoped view failed")
	}
	output.Reset()
	prompts.Reset()
	runCommand("ticket", "watch", "012345", "--project", "app")
	if a.last == nil || !a.last.OK || polls != 5 || mutations != 2 || item.State != domain.StateDone {
		t.Fatal("watch did not finish read-only on actual terminal state")
	}
	if !strings.Contains(prompts.String(), "Monitor disconnected") || strings.Count(output.String(), "last output") < 2 || !strings.Contains(output.String(), "last validated frame") || strings.Contains(output.String(), "stuck") {
		t.Fatal("watch failed to distinguish quiet connected output from disconnection")
	}
}
