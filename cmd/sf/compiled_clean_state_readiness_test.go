package main

import (
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
			env := []string{"HOME=" + home, "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TMPDIR=" + root, "LANG=C", "CODEX_HOME=" + filepath.Join(root, "codex"), "GH_CONFIG_DIR=" + filepath.Join(root, "gh"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GH_TOKEN=seeded-clean-state-secret"}
			run := func(args ...string) ([]byte, error) {
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, args...)
				cmd.Dir, cmd.Env = repository, env
				return cmd.CombinedOutput()
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
			output, err := run("init", "--check", "--json")
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
			for _, command := range [][]string{{"factory", "status", "--json"}, {"auth", "status", "--json"}} {
				output, err := run(command...)
				if strings.Contains(string(output), "seeded-clean-state-secret") {
					t.Fatalf("%v exposed seeded credential", command)
				}
				if command[0] == "factory" && (err == nil || !strings.Contains(string(output), "daemon_unavailable")) {
					t.Fatalf("missing daemon was not actionable: %v %s", err, output)
				}
			}
		})
	}
}
