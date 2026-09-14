package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nysa-company/sf/internal/api"
	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
	"github.com/nysa-company/sf/internal/store"
	"github.com/spf13/cobra"
)

const projectSchema = "sf.projects/v1"
const projectRetained = "Repository files, .sf/config.toml, worktrees, ticket history, branches and GitHub PRs are retained. Other projects and the factory keep running."

type ProjectRequest struct {
	Channel            domain.Channel
	Project            string
	Home               string
	Paths              config.ChannelPaths
	Operation          string // list, preview, remove
	All                bool
	ExpectedGeneration uint64
}

type projectView struct {
	Project    string `json:"project"`
	Repository string `json:"repository"`
	State      string `json:"state"`
	Generation uint64 `json:"registration_generation"`
}

type projectReport struct {
	Schema           string         `json:"schema"`
	Channel          domain.Channel `json:"channel"`
	Operation        string         `json:"operation"`
	Projects         []projectView  `json:"projects"`
	CanRemove        bool           `json:"can_remove,omitempty"`
	GlobalQuarantine bool           `json:"global_quarantine,omitempty"`
	Blockers         map[string]int `json:"blockers,omitempty"`
	Retained         string         `json:"retained,omitempty"`
}

// RunProject is local setup maintenance, like init/config apply. Preview and
// inventory open read-only; only an explicitly confirmed removal opens a
// writer. Store rechecks registration generation and safety in its transaction.
func RunProject(ctx context.Context, request ProjectRequest) api.Response {
	binary := binaryForChannel(request.Channel)
	help := []string{binary, "project", "--help"}
	if !request.Channel.Valid() || (request.Operation != "list" && request.Operation != "preview" && request.Operation != "remove") || (request.Operation != "list" && strings.TrimSpace(request.Project) == "") || (request.Operation == "remove" && request.ExpectedGeneration == 0) {
		return failure("invalid_argument", "select a project and a valid maintenance operation", help)
	}
	paths := request.Paths
	if paths.Root == "" {
		home := request.Home
		var err error
		if home == "" {
			home, err = os.UserHomeDir()
		}
		if err != nil {
			return failure("project_unavailable", "current-user home is unavailable", help)
		}
		paths, err = config.PathsFor(home, request.Channel)
		if err != nil {
			return failure("invalid_argument", "channel paths are unavailable", help)
		}
	}
	report := projectReport{Schema: projectSchema, Channel: request.Channel, Operation: request.Operation, Projects: []projectView{}}
	info, err := os.Lstat(paths.Database)
	if errors.Is(err, os.ErrNotExist) && request.Operation == "list" {
		return projectResponse(report, false, false)
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return failure("not_configured", "no regular channel authority database is available", []string{binary, "init", "--help"})
	}
	var db *store.Store
	if request.Operation == "remove" {
		db, err = store.OpenChannel(ctx, paths.Database, paths.Backups, request.Channel)
	} else {
		db, err = store.OpenReadOnly(ctx, paths.Database)
	}
	if err != nil {
		return failure("project_unavailable", "channel authority could not be opened; inspect doctor and use a compatible factory version", []string{binary, "doctor"})
	}
	defer db.Close()
	view := func(p store.Project) projectView {
		return projectView{Project: string(p.ID), Repository: p.Path, State: string(p.Lifecycle), Generation: p.RegistrationGeneration}
	}
	if request.Operation == "list" {
		projects, err := db.Projects(ctx, request.Channel)
		if err != nil {
			return projectStoreFailure(err, binary, request.Project)
		}
		for _, p := range projects {
			if request.All || string(p.Lifecycle) != "removed" {
				report.Projects = append(report.Projects, view(p))
			}
		}
		return projectResponse(report, false, false)
	}
	var preview store.ProjectRemovalPreview
	changed := false
	if request.Operation == "remove" {
		preview, changed, err = db.RemoveProject(ctx, request.Channel, domain.ProjectID(request.Project), request.ExpectedGeneration, time.Now().UTC())
	} else {
		preview, err = db.ProjectRemovalPreview(ctx, request.Channel, domain.ProjectID(request.Project))
	}
	if err != nil && !errors.Is(err, store.ErrProjectRemovalBlocked) {
		return projectStoreFailure(err, binary, request.Project)
	}
	report.Projects = append(report.Projects, view(preview.Project))
	report.CanRemove = preview.CanRemove || preview.Project.Lifecycle == store.ProjectRemoved
	report.GlobalQuarantine = preview.GlobalQuarantine
	report.Retained = projectRetained
	report.Blockers = map[string]int{"unfinished_tickets": preview.UnfinishedTickets, "workflow_owners": preview.ActiveWorkflowOwners, "leases": preview.ActiveLeases, "provider_attempts": preview.ActiveProviderAttempts, "phase_runs": preview.ActivePhaseRuns, "repository_commands": preview.ActiveRepositoryCommands, "git_mutations": preview.ActiveGitMutations, "unresolved_effects": preview.UnresolvedEffects, "authoring_turns": preview.ActiveAuthoringTurns}
	response := projectResponse(report, request.Operation == "remove" && err == nil, !changed)
	if !report.CanRemove || err != nil {
		response.OK = false
		response.Error = &api.Error{Code: "project_removal_blocked", Message: "project has unfinished work or unresolved execution; finish/cancel tickets and resolve safety blockers before removing it"}
		response.NextAction = &domain.NextAction{Code: "resolve_project_work", Argv: []string{binary, "ticket", "list", "--project", request.Project}}
		if preview.GlobalQuarantine || preview.UnfinishedTickets == 0 {
			response.NextAction = &domain.NextAction{Code: "resolve_project_safety", Argv: []string{binary, "doctor"}}
		}
	}
	return response
}

func projectStoreFailure(err error, binary, project string) api.Response {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return failure("unknown_project", "project is not registered in this channel", []string{binary, "project", "list", "--all"})
	case errors.Is(err, store.ErrProjectLifecycleChanged):
		return failure("project_changed", "registration changed after preview; inspect it and retry", []string{binary, "project", "remove", project, "--dry-run"})
	case errors.Is(err, store.ErrBusy):
		return failure("store_busy", "local authority is busy; no removal was confirmed", []string{binary, "project", "remove", project, "--dry-run"})
	default:
		return failure("project_unavailable", "project authority could not be verified; no removal was confirmed", []string{binary, "doctor"})
	}
}

func projectResponse(report projectReport, attempted, observed bool) api.Response {
	data, _ := json.Marshal(report)
	response := api.Response{Version: api.Version, RequestID: requestID(), OK: true, Data: data}
	if attempted {
		response.Mutation = api.Mutation{Attempted: true, Kind: "project.remove", Identity: string(report.Channel) + "/" + report.Projects[0].Project, Observed: observed}
	}
	return response
}

func (a *app) projectCall(request ProjectRequest) api.Response {
	request.Channel = a.channel
	if a.runProject != nil {
		return a.runProject(a.ctx, request)
	}
	return RunProject(a.ctx, request)
}

func (a *app) projectCommand() *cobra.Command {
	root := &cobra.Command{Use: "project", Short: "List or disconnect projects without deleting files or history", Args: cobra.NoArgs}
	var all, dryRun, yes bool
	list := &cobra.Command{Use: "list", Short: "List active project registrations", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		return a.emit(a.projectCall(ProjectRequest{Operation: "list", All: all}))
	}}
	list.Flags().BoolVar(&all, "all", false, "include removed registrations")
	remove := &cobra.Command{Use: "remove [project]", Short: "Disconnect an idle project; retain its files and history", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		project := ""
		if len(args) == 1 {
			project = args[0]
		} else {
			if !a.canSelectInteractively() {
				return a.emit(failure("invalid_argument", "supply an exact project name; terminal selection is unavailable", commandHelpAction(cmd)))
			}
			response := a.projectCall(ProjectRequest{Operation: "list"})
			if !response.OK {
				return a.emit(response)
			}
			var inventory projectReport
			if json.Unmarshal(response.Data, &inventory) != nil || inventory.Schema != projectSchema || inventory.Channel != a.channel || len(inventory.Projects) == 0 || len(inventory.Projects) > 1000 {
				return a.emit(failure("invalid_argument", "no selectable project inventory; use project list", []string{binaryForChannel(a.channel), "project", "list"}))
			}
			fmt.Fprintln(a.errOut, "Select a project to remove (number; q cancels):")
			for i, p := range inventory.Projects {
				fmt.Fprintf(a.errOut, "%d) %s [%s] %s\n", i+1, safeSelectionLabel(p.Project), safeSelectionLabel(string(a.channel)), safeSelectionLabel(p.Repository))
			}
			line, err := readSelectionAnswer(a.projectInput())
			i, parseErr := strconv.Atoi(strings.TrimSpace(line))
			if err != nil || parseErr != nil || i < 1 || i > len(inventory.Projects) {
				return a.emit(failure("operator_action_required", "project selection cancelled; nothing changed", commandHelpAction(cmd)))
			}
			project = inventory.Projects[i-1].Project
		}
		preview := a.projectCall(ProjectRequest{Operation: "preview", Project: project})
		if !preview.OK || dryRun {
			return a.emit(preview)
		}
		var report projectReport
		if json.Unmarshal(preview.Data, &report) != nil || report.Schema != projectSchema || report.Channel != a.channel || len(report.Projects) != 1 || report.Projects[0].Project != project || report.Projects[0].Generation == 0 || !report.CanRemove {
			return a.emit(failure("invalid_response", "project preview is inconsistent; nothing changed", commandHelpAction(cmd)))
		}
		if !yes {
			if !a.canSelectInteractively() {
				return a.emit(failure("operator_action_required", "inspect --dry-run, then supply --yes to remove this registration without deleting files", []string{binaryForChannel(a.channel), "project", "remove", project, "--dry-run"}))
			}
			if err := Render(a.errOut, preview, false); err != nil {
				return err
			}
			fmt.Fprintf(a.errOut, "Remove %s from SF management? Type yes to confirm: ", safeSelectionLabel(project))
			answer, err := readSelectionAnswer(a.projectInput())
			if err != nil || strings.TrimSpace(answer) != "yes" {
				return a.emit(failure("operator_action_required", "removal cancelled; nothing changed", commandHelpAction(cmd)))
			}
		}
		if cmd.Context().Err() != nil {
			return a.emit(failure("operator_action_required", "removal cancelled; nothing changed", commandHelpAction(cmd)))
		}
		return a.emit(a.projectCall(ProjectRequest{Operation: "remove", Project: project, ExpectedGeneration: report.Projects[0].Generation}))
	}}
	remove.Flags().BoolVar(&dryRun, "dry-run", false, "preview retention and blockers without making changes")
	remove.Flags().BoolVar(&yes, "yes", false, "confirm unregistering; never deletes data or bypasses blockers")
	root.AddCommand(list, remove)
	return root
}

func (a *app) projectInput() io.Reader {
	if a.input != nil {
		return a.input
	}
	return os.Stdin
}

func renderProjects(writer io.Writer, report map[string]any) error {
	if _, err := fmt.Fprintf(writer, "Projects (%s)\n", safeSelectionLabel(stringField(report, "channel"))); err != nil {
		return err
	}
	projects, _ := report["projects"].([]any)
	if len(projects) == 0 {
		_, err := fmt.Fprintln(writer, "No projects. Use sf init inside a repository to register it.")
		return err
	}
	for _, raw := range projects {
		p, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(writer, "- %s [%s]\n  %s\n", safeSelectionLabel(stringField(p, "project")), safeSelectionLabel(stringField(p, "state")), safeSelectionLabel(stringField(p, "repository"))); err != nil {
			return err
		}
	}
	if retained := stringField(report, "retained"); retained != "" {
		if _, err := fmt.Fprintln(writer, retained); err != nil {
			return err
		}
	}
	if blockers, ok := report["blockers"].(map[string]any); ok {
		keys := make([]string, 0, len(blockers))
		for key := range blockers {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if count, ok := blockers[key].(float64); ok && count > 0 {
				if _, err := fmt.Fprintf(writer, "Blocker: %s (%g)\n", key, count); err != nil {
					return err
				}
			}
		}
	}
	if boolField(report, "global_quarantine") {
		if _, err := fmt.Fprintln(writer, "Blocker: this channel has an unresolved external-process quarantine."); err != nil {
			return err
		}
	}
	if stringField(report, "operation") == "preview" {
		_, err := fmt.Fprintf(writer, "Preview only. Eligible for removal: %t\n", boolField(report, "can_remove"))
		return err
	}
	if stringField(report, "operation") == "remove" {
		removed := len(projects) == 1
		for _, raw := range projects {
			p, ok := raw.(map[string]any)
			removed = removed && ok && stringField(p, "state") == "removed"
		}
		message := "Removal was not completed. Resolve the reported blockers and retry."
		if removed {
			message = "Removed from SF management. Run sf init in the same repository to reactivate it."
		}
		_, err := fmt.Fprintln(writer, message)
		return err
	}
	return nil
}
