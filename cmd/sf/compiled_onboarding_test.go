package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/api"
	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
	"github.com/nysa-company/sf/internal/store"
	"github.com/nysa-company/sf/internal/ticket"
)

// This acceptance fixture builds the complete helper bundle, but deliberately
// does not start a provider or daemon. Those are separate execution gates.
func TestCompiledDevOnboardingUsesPrivateHomeAndLocalCommands(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS onboarding acceptance")
	}
	binary := buildDevRuntimeBundle(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, repository := filepath.Join(root, "home"), filepath.Join(root, "My-App")
	for _, path := range []string{home, repository} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + root, "LANG=C", "CODEX_HOME=" + filepath.Join(root, "codex"), "GH_CONFIG_DIR=" + filepath.Join(root, "gh"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	run := func(executable string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir, command.Env = repository, environment
		return command.CombinedOutput()
	}
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=SF Test", "-c", "user.email=sf@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		if output, err := run("/usr/bin/git", args...); err != nil {
			t.Fatalf("git: %v %s", err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.test/onboarding\n\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	request := func(args ...string) api.Response {
		output, err := run(binary, append(args, "--json")...)
		var response api.Response
		if json.Unmarshal(output, &response) != nil {
			t.Fatalf("command %v err=%v output=%s", args, err, output)
		}
		if (err == nil) != response.OK {
			t.Fatalf("exit/envelope disagree: %v %+v", err, response)
		}
		return response
	}
	preview := request("init", "--check")
	if !preview.OK || preview.Mutation.Attempted {
		t.Fatalf("preview=%+v", preview)
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatalf("preview touched HOME: %v %v", entries, err)
	}
	if _, err := os.Lstat(filepath.Join(repository, ".sf")); !os.IsNotExist(err) {
		t.Fatalf("preview wrote config: %v", err)
	}
	for i := 0; i < 2; i++ {
		registered := request("init")
		if !registered.OK || registered.Mutation.Observed != (i == 1) {
			t.Fatalf("registration=%+v", registered)
		}
	}
	paths, _ := config.PathsFor(home, domain.ChannelDev)
	database, err := store.OpenReadOnly(context.Background(), paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	project, err := database.Project(context.Background(), domain.ChannelDev, "my-app")
	if err != nil || project.Path != repository || project.ConfigGeneration != 1 {
		t.Fatalf("project=%+v err=%v", project, err)
	}
	stable, _ := config.PathsFor(home, domain.ChannelStable)
	if _, err := os.Lstat(stable.Root); !os.IsNotExist(err) {
		t.Fatalf("dev init touched stable: %v", err)
	}
	if _, err := os.Lstat(paths.Socket); !os.IsNotExist(err) {
		t.Fatalf("local setup created daemon socket: %v", err)
	}
	template, err := run(binary, "ticket", "template")
	if err != nil {
		t.Fatalf("template: %v %s", err, template)
	}
	parsed, err := ticket.Parse(bytes.NewReader(template))
	if err != nil || len(parsed.Acceptance) == 0 {
		t.Fatalf("template parse=%v", err)
	}
	draft := filepath.Join(repository, "ticket.md")
	if err := os.WriteFile(draft, template, 0600); err != nil {
		t.Fatal(err)
	}
	validated := request("ticket", "validate", draft)
	if !validated.OK || validated.Mutation.Attempted {
		t.Fatalf("validate=%+v", validated)
	}
	refused := request("ticket", "new", filepath.Join(repository, "piped.md"))
	if refused.OK || refused.Mutation.Attempted {
		t.Fatalf("noninteractive create=%+v", refused)
	}
	if _, err := os.Lstat(filepath.Join(repository, "piped.md")); !os.IsNotExist(err) {
		t.Fatalf("noninteractive file created: %v", err)
	}
	for _, answer := range []string{"no", "yes"} {
		path := filepath.Join(repository, "interactive-"+answer+".md")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		command := exec.CommandContext(ctx, "/usr/bin/script", "-q", "/dev/null", binary, "ticket", "new", "--no-ai", path)
		command.Dir, command.Env = repository, environment
		// Keep stdin open until the child exits: BSD script otherwise sends
		// terminal EOF before the child has consumed the supplied answers.
		input, err := command.StdinPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		var transcript bytes.Buffer
		command.Stdout, command.Stderr = &transcript, &transcript
		if err := command.Start(); err != nil {
			input.Close()
			cancel()
			t.Fatal(err)
		}
		_, writeErr := input.Write([]byte("Count jobs\nImplement a read-only count.\nEmpty returns zero.\n\n" + answer + "\n"))
		err = command.Wait()
		input.Close()
		cancel()
		output := transcript.Bytes()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if !bytes.Contains(output, []byte("Review the complete draft:")) {
			t.Fatalf("interactive preview missing: %v %s", err, output)
		}
		data, readErr := os.ReadFile(path)
		if answer == "no" {
			if !os.IsNotExist(readErr) {
				t.Fatal("cancelled interactive draft created a file")
			}
		} else {
			if err != nil || readErr != nil {
				t.Fatalf("interactive save: %v %v %s", err, readErr, output)
			}
			parsed, err := ticket.Parse(bytes.NewReader(data))
			if err != nil || parsed.Title != "Count jobs" || len(parsed.Acceptance) != 1 {
				t.Fatalf("interactive draft=%+v err=%v", parsed, err)
			}
		}
	}
	tickets, err := database.Tickets(context.Background(), domain.ChannelDev, "my-app", 10)
	if err != nil || len(tickets) != 0 {
		t.Fatalf("local commands submitted tickets: %v %v", tickets, err)
	}
	// Exercise the actual packaged CLI repair, not a fake repair callback.
	// Missing provider tools remain guided failures in this isolated HOME.
	if err := os.Chmod(paths.Logs, 0755); err != nil {
		t.Fatal(err)
	}
	fixPreview := request("doctor", "fix", "--dry-run")
	if fixPreview.Mutation.Attempted {
		t.Fatalf("preview mutated: %+v", fixPreview)
	}
	if info, err := os.Stat(paths.Logs); err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("preview changed permissions: %v", err)
	}
	fix := request("doctor", "fix", "--yes")
	if !fix.Mutation.Attempted {
		t.Fatalf("repair not applied: %+v", fix)
	}
	if info, err := os.Stat(paths.Logs); err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("repair did not restore permissions: %v", err)
	}
	if again := request("doctor", "fix", "--yes"); again.Mutation.Attempted {
		t.Fatalf("repair not idempotent: %+v", again)
	}
	previewRemoval := request("project", "remove", "my-app", "--dry-run")
	if !previewRemoval.OK || previewRemoval.Mutation.Attempted {
		t.Fatalf("removal preview=%+v", previewRemoval)
	}
	if removed := request("project", "remove", "my-app", "--yes"); !removed.OK {
		t.Fatalf("remove=%+v", removed)
	}
	var projectList struct {
		Projects []struct {
			State string `json:"state"`
		} `json:"projects"`
	}
	listed := request("project", "list")
	if !listed.OK || json.Unmarshal(listed.Data, &projectList) != nil || len(projectList.Projects) != 0 {
		t.Fatalf("active projects=%+v", listed)
	}
	listed = request("project", "list", "--all")
	if !listed.OK || json.Unmarshal(listed.Data, &projectList) != nil || len(projectList.Projects) != 1 || projectList.Projects[0].State != "removed" {
		t.Fatalf("retained registration=%+v", listed)
	}
	if data, err := os.ReadFile(draft); err != nil || !bytes.Equal(data, template) {
		t.Fatalf("removal changed draft: %v", err)
	}
	if registered := request("init"); !registered.OK {
		t.Fatalf("reactivation=%+v", registered)
	}
	if project, err := database.Project(context.Background(), domain.ChannelDev, "my-app"); err != nil || project.Lifecycle != store.ProjectActive {
		t.Fatalf("reactivated=%+v %v", project, err)
	}
}
