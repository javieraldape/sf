package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nysa-company/sf/internal/api"
	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
	"github.com/nysa-company/sf/internal/store"
)

func decodeProjectReport(t *testing.T, response api.Response) projectReport {
	t.Helper()
	var report projectReport
	if err := json.Unmarshal(response.Data, &report); err != nil {
		t.Fatalf("report=%+v: %v", response, err)
	}
	return report
}

func TestProjectRemoveInitRoundTripRetainsFilesAndOtherChannel(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	repo := initializedRepository(t)
	for _, channel := range []domain.Channel{domain.ChannelDev, domain.ChannelStable} {
		if response := RunInit(ctx, InitRequest{Channel: channel, Project: "sample", Repo: repo, Home: home}); !response.OK {
			t.Fatalf("init=%+v", response)
		}
	}
	dirty := filepath.Join(repo, "retained-work.txt")
	if err := os.WriteFile(dirty, []byte("uncommitted user work\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(op string, all bool, generation uint64) api.Response {
		return RunProject(ctx, ProjectRequest{Channel: domain.ChannelDev, Project: "sample", Home: home, Operation: op, All: all, ExpectedGeneration: generation})
	}
	preview := call("preview", false, 0)
	if !preview.OK || preview.Mutation.Attempted {
		t.Fatalf("preview=%+v", preview)
	}
	p := decodeProjectReport(t, preview)
	if !p.CanRemove || len(p.Projects) != 1 {
		t.Fatalf("preview=%+v", p)
	}
	removed := call("remove", false, p.Projects[0].Generation)
	if !removed.OK || !removed.Mutation.Attempted || removed.Mutation.Observed {
		t.Fatalf("remove=%+v", removed)
	}
	if got := decodeProjectReport(t, call("list", false, 0)); len(got.Projects) != 0 {
		t.Fatalf("active=%+v", got)
	}
	all := decodeProjectReport(t, call("list", true, 0))
	if len(all.Projects) != 1 || all.Projects[0].State != "removed" {
		t.Fatalf("all=%+v", all)
	}
	if replay := call("remove", false, all.Projects[0].Generation); !replay.OK || !replay.Mutation.Observed {
		t.Fatalf("replay=%+v", replay)
	}
	if response := RunProject(ctx, ProjectRequest{Channel: domain.ChannelStable, Operation: "list", Home: home}); !response.OK || len(decodeProjectReport(t, response).Projects) != 1 {
		t.Fatalf("stable=%+v", response)
	}
	if response := RunInit(ctx, InitRequest{Channel: domain.ChannelDev, Project: "sample", Repo: repo, Home: home}); !response.OK {
		t.Fatalf("reactivate=%+v", response)
	}
	active := decodeProjectReport(t, call("list", false, 0))
	if len(active.Projects) != 1 || active.Projects[0].State != "active" || active.Projects[0].Generation <= all.Projects[0].Generation {
		t.Fatalf("reactivated=%+v", active)
	}
	if got, err := os.ReadFile(dirty); err != nil || string(got) != "uncommitted user work\n" {
		t.Fatalf("retained=%q %v", got, err)
	}
	if stale := call("remove", false, p.Projects[0].Generation); stale.OK || stale.Error.Code != "project_changed" {
		t.Fatalf("stale=%+v", stale)
	}
}

func TestProjectListMissingAuthorityDoesNotCreateState(t *testing.T) {
	home := t.TempDir()
	response := RunProject(context.Background(), ProjectRequest{Channel: domain.ChannelDev, Operation: "list", Home: home})
	if !response.OK || len(decodeProjectReport(t, response).Projects) != 0 {
		t.Fatalf("response=%+v", response)
	}
	paths, _ := config.PathsFor(home, domain.ChannelDev)
	if _, err := os.Lstat(paths.Root); !os.IsNotExist(err) {
		t.Fatalf("list created root: %v", err)
	}
}

func TestProjectCommandsConfirmAndBindPreview(t *testing.T) {
	for _, tc := range []struct {
		name        string
		args        []string
		input       string
		interactive bool
		wantRemove  bool
	}{
		{"dry", []string{"project", "remove", "sample", "--dry-run"}, "", false, false},
		{"jsonNoYes", []string{"project", "remove", "sample", "--json"}, "yes\n", true, false},
		{"jsonYes", []string{"project", "remove", "sample", "--json", "--yes"}, "", false, true},
		{"cancel", []string{"project", "remove", "sample"}, "no\n", true, false},
		{"confirm", []string{"project", "remove", "sample"}, "yes\n", true, true},
		{"picker", []string{"project", "remove"}, "1\nyes\n", true, true},
		{"pickerCancel", []string{"project", "remove"}, "q\n", true, false},
		{"noTerminal", []string{"project", "remove", "--yes"}, "", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			a := newApp(nil, &out, &errOut)
			a.channel = domain.ChannelDev
			a.input = strings.NewReader(tc.input)
			a.interactive = func() bool { return tc.interactive }
			removed := false
			a.runProject = func(ctx context.Context, r ProjectRequest) api.Response {
				if r.Channel != domain.ChannelDev {
					t.Fatal("wrong channel")
				}
				report := projectReport{Schema: projectSchema, Channel: r.Channel, Operation: r.Operation, CanRemove: true, Projects: []projectView{{Project: "sample", Repository: "/example/project", State: "active", Generation: 7}}, Retained: projectRetained}
				if r.Operation == "remove" {
					removed = true
					if r.Project != "sample" || r.ExpectedGeneration != 7 {
						t.Fatalf("unbound request=%+v", r)
					}
					report.Projects[0].State = "removed"
				}
				return projectResponse(report, r.Operation == "remove", false)
			}
			cmd := a.command()
			cmd.SetArgs(tc.args)
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatal(err)
			}
			if removed != tc.wantRemove {
				t.Fatalf("removed=%t output=%s error=%s", removed, out.String(), errOut.String())
			}
			if a.last == nil {
				t.Fatal("missing response")
			}
			if tc.name == "jsonNoYes" && exitCode(*a.last) != ExitAction {
				t.Fatalf("exit=%v", exitCode(*a.last))
			}
		})
	}
}

func TestProjectRemoveRefusesUnfinishedRealStoreTicket(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	repo := initializedRepository(t)
	if response := RunInit(ctx, InitRequest{Channel: domain.ChannelDev, Project: "sample", Repo: repo, Home: home}); !response.OK {
		t.Fatalf("init=%+v", response)
	}
	paths, _ := config.PathsFor(home, domain.ChannelDev)
	db, err := store.OpenChannel(ctx, paths.Database, paths.Backups, domain.ChannelDev)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The Store suite covers writer/effect races; this asserts the real CLI
	// preview exposes an unfinished registration blocker without mutating it.
	project, err := db.Project(ctx, domain.ChannelDev, "sample")
	if err != nil {
		t.Fatal(err)
	}
	if project.Lifecycle != store.ProjectActive {
		t.Fatalf("project=%+v", project)
	}
	if err := db.CreateTicket(ctx, store.Ticket{Ref: domain.TicketRef{Channel: domain.ChannelDev, Project: "sample", Ticket: "SF-unfinished"}, SourceDigest: "unfinished", Type: domain.TicketBug, MergeMode: domain.MergeGuarded}); err != nil {
		t.Fatal(err)
	}
	blocked := RunProject(ctx, ProjectRequest{Channel: domain.ChannelDev, Project: "sample", Home: home, Operation: "preview"})
	if blocked.OK || blocked.Error.Code != "project_removal_blocked" || blocked.Mutation.Attempted || decodeProjectReport(t, blocked).Blockers["unfinished_tickets"] != 1 {
		t.Fatalf("blocked=%+v", blocked)
	}
	response := RunProject(ctx, ProjectRequest{Channel: domain.ChannelDev, Project: "missing", Home: home, Operation: "preview"})
	if response.OK || response.Error.Code != "unknown_project" {
		t.Fatalf("missing=%+v", response)
	}
}
