package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/api"
)

// This acceptance is intentionally offline. It proves the packaged CLI's
// clean-state contract and refusal guidance; provider qualification and live
// GitHub readiness remain separate native/live gates.
func TestCompiledCleanStateReadinessRefusalsAndOfflineStacks(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS clean-state acceptance")
	}
	binary := buildDevRuntimeBundle(t)
	runnerHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	runnerHome, err = filepath.Abs(runnerHome)
	if err != nil {
		t.Fatal(err)
	}
	runnerHome, err = filepath.EvalSymlinks(runnerHome)
	if err != nil || runnerHome == string(filepath.Separator) {
		t.Fatalf("invalid runner home for protected executable fixture: %v", err)
	}
	for _, fixture := range []struct {
		name       string
		files      map[string]string
		accepted   bool
		reasonCode string
	}{
		{name: "go", files: map[string]string{"go.mod": "module example.test/clean-go\n\ngo 1.25\n"}, accepted: true},
		{name: "node", files: map[string]string{"package.json": `{"name":"clean-node","private":true,"type":"module"}`, "smoke.test.js": "import test from 'node:test'; test('baseline', () => {});\n"}, accepted: true},
		{name: "unsupported", files: map[string]string{"Gemfile": "raise 'must not execute'\n"}, reasonCode: "invalid_configuration"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := shortOnboardingRoot(t)
			home, repository := filepath.Join(root, "home"), filepath.Join(root, "repo")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(repository, 0700); err != nil {
				t.Fatal(err)
			}
			for name, contents := range fixture.files {
				if err := os.WriteFile(filepath.Join(repository, name), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			binDir, err := os.MkdirTemp(runnerHome, ".sf-clean-state-gh-")
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(binDir) != runnerHome || !strings.HasPrefix(filepath.Base(binDir), ".sf-clean-state-gh-") {
				t.Fatal("fake gh directory escaped runner home")
			}
			t.Cleanup(func() {
				if err := os.RemoveAll(binDir); err != nil {
					t.Errorf("remove fake gh fixture: %v", err)
				}
			})
			if err := os.Chmod(binDir, 0700); err != nil {
				t.Fatal(err)
			}
			gh := filepath.Join(binDir, "gh")
			if err := os.WriteFile(gh, []byte("#!/bin/sh\ncase \"$1 $2\" in\n  \"--version \"*) echo 'gh version 2.0.0'; exit 0 ;;\n  \"auth status\"*) echo 'not logged in' >&2; exit 1 ;;\n  *) exit 1 ;;\nesac\n"), 0700); err != nil {
				t.Fatal(err)
			}
			env := []string{"HOME=" + home, "PATH=" + binDir + ":/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + root, "LANG=C", "CODEX_HOME=" + filepath.Join(root, "codex"), "GH_CONFIG_DIR=" + filepath.Join(root, "gh"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
			run := func(args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.Env = repository, env
				return cmd.CombinedOutput()
			}
			if output, err := run("--help"); err != nil ||
				!bytes.Contains(output, []byte("Delegate a Markdown ticket to a local, operator-controlled software factory.")) ||
				!bytes.Contains(output, []byte("Ticket workflow:")) ||
				!bytes.Contains(output, []byte("Draft, start, inspect, and control tickets")) ||
				!bytes.Contains(output, []byte("Setup and diagnostics:")) {
				t.Fatalf("help exit=%v output=%s", err, output)
			}
			versionOutput, err := run("version", "--json")
			var versionResponse api.Response
			if err != nil || json.Unmarshal(versionOutput, &versionResponse) != nil || !versionResponse.OK || versionResponse.Mutation.Attempted {
				t.Fatalf("version exit=%v response=%+v output=%s", err, versionResponse, versionOutput)
			}
			git := func(args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, "/usr/bin/git", args...)
				cmd.Dir, cmd.Env = repository, env
				return cmd.CombinedOutput()
			}
			if output, err := git("init", "-b", "main"); err != nil {
				t.Fatalf("git init: %v %s", err, output)
			}
			if output, err := git("-c", "user.name=SF Test", "-c", "user.email=sf@example.invalid", "add", "."); err != nil {
				t.Fatalf("git add: %v %s", err, output)
			}
			if output, err := git("-c", "user.name=SF Test", "-c", "user.email=sf@example.invalid", "commit", "-m", "baseline"); err != nil {
				t.Fatalf("git commit: %v %s", err, output)
			}
			secretEnv := append(append([]string(nil), env...), "GH_TOKEN=seeded-clean-state-secret")
			check := exec.Command(binary, "init", "--check", "--json")
			check.Dir, check.Env = repository, secretEnv
			output, err := check.CombinedOutput()
			var response api.Response
			if json.Unmarshal(output, &response) != nil || response.OK != fixture.accepted || (err == nil) != response.OK || response.Mutation.Attempted {
				t.Fatalf("init check exit=%v response=%+v output=%s", err, response, output)
			}
			if !fixture.accepted && (response.Error == nil || response.Error.Code != fixture.reasonCode) {
				t.Fatalf("unsupported response=%+v", response.Error)
			}
			if strings.Contains(string(output), "seeded-clean-state-secret") {
				t.Fatal("init check exposed seeded credential")
			}
			if _, statErr := os.Lstat(filepath.Join(repository, ".sf")); !os.IsNotExist(statErr) {
				t.Fatalf("init check wrote config: %v", statErr)
			}
			if !fixture.accepted {
				return
			}
			// Registration is the first mutating setup step; it must not imply
			// provider qualification, daemon startup, or ticket submission.
			if output, err := run("init", "--json"); err != nil || !bytes.Contains(output, []byte(`"ok":true`)) {
				t.Fatalf("registration exit=%v output=%s", err, output)
			}
			draft := filepath.Join(repository, "offline-ticket.md")
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			command := exec.CommandContext(ctx, "/usr/bin/script", "-q", "/dev/null", binary, "ticket", "new", "--no-ai", draft)
			command.Dir, command.Env = repository, env
			input, pipeErr := command.StdinPipe()
			if pipeErr != nil {
				cancel()
				t.Fatal(pipeErr)
			}
			var transcript bytes.Buffer
			command.Stdout, command.Stderr = &transcript, &transcript
			if err := command.Start(); err != nil {
				input.Close()
				cancel()
				t.Fatal(err)
			}
			_, writeErr := input.Write([]byte("Offline clean-state ticket\nExercise selection and view.\nThe baseline must remain green.\n\nyes\n"))
			waitErr := command.Wait()
			input.Close()
			cancel()
			if writeErr != nil || waitErr != nil {
				t.Fatalf("offline draft exit=%v write=%v transcript=%s", waitErr, writeErr, transcript.String())
			}
			if _, err := os.Stat(draft); err != nil {
				t.Fatalf("offline draft was not saved: %v", err)
			}
			if strings.Contains(transcript.String(), "seeded-clean-state-secret") {
				t.Fatal("draft flow exposed seeded credential")
			}
			for _, command := range [][]string{{"factory", "status", "--json"}, {"auth", "status", "--json"}} {
				output, err := run(command...)
				if strings.Contains(string(output), "seeded-clean-state-secret") {
					t.Fatalf("%v exposed seeded credential", command)
				}
				if command[0] == "factory" && (err == nil || !strings.Contains(string(output), "daemon_unavailable")) {
					t.Fatalf("missing daemon was not actionable: %v %s", err, output)
				}
				if command[0] == "auth" {
					var report struct {
						Providers []struct {
							Authenticated bool `json:"authenticated"`
							NextAction    struct {
								Argv []string `json:"argv"`
							} `json:"next_action"`
						} `json:"providers"`
					}
					var envelope api.Response
					if json.Unmarshal(output, &envelope) != nil || !envelope.OK || json.Unmarshal(envelope.Data, &report) != nil {
						t.Fatalf("auth status exit=%v output=%s", err, output)
					}
					if len(report.Providers) == 0 {
						t.Fatalf("clean auth returned no provider readiness records: %s", envelope.Data)
					}
					for _, provider := range report.Providers {
						if provider.Authenticated || len(provider.NextAction.Argv) == 0 {
							t.Fatalf("clean auth unexpectedly ready: %+v", provider)
						}
					}
				}
			}
			doctorOutput, doctorErr := run("doctor", "--json")
			var doctorResponse api.Response
			var doctor struct {
				Authentication []struct {
					Provider      string `json:"provider"`
					Installed     bool   `json:"installed"`
					Authenticated bool   `json:"authenticated"`
					State         string `json:"state"`
				} `json:"authentication"`
			}
			if json.Unmarshal(doctorOutput, &doctorResponse) != nil || json.Unmarshal(doctorResponse.Data, &doctor) != nil {
				t.Fatalf("missing gh login produced invalid doctor JSON: err=%v output=%s", doctorErr, doctorOutput)
			}
			githubRecord := false
			for _, record := range doctor.Authentication {
				if record.Provider == "github" && record.Installed && !record.Authenticated && record.State == "unauthenticated" {
					githubRecord = true
				}
			}
			if doctorErr == nil || doctorResponse.OK || doctorResponse.Mutation.Attempted || !strings.Contains(string(doctorOutput), `"code":"doctor_failed"`) ||
				!strings.Contains(string(doctorOutput), `"id":"gh_executable"`) ||
				!strings.Contains(string(doctorOutput), `"id":"github_auth"`) ||
				!strings.Contains(string(doctorOutput), `"auth","login","github"`) || !githubRecord {
				t.Fatalf("missing gh login was not an actionable refusal: err=%v output=%s", doctorErr, doctorOutput)
			}
		})
	}
}
