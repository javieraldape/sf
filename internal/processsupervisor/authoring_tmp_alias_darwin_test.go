package processsupervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuthoringSandboxProductionTemporaryAlias(t *testing.T) {
	if os.Getenv("SF_AUTHORING_TMP_ALIAS_HELPER") == "1" {
		authoringTemporaryAliasHelper(t)
		return
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal("fixture executable unavailable")
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal("fixture executable identity unavailable")
	}
	for _, alias := range []bool{false, true} {
		name := "canonical_control"
		if alias {
			name = "raw_alias"
		}
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal("fixture root unavailable")
			}
			physical := filepath.Join(root, "physical")
			if os.Mkdir(physical, 0700) != nil {
				t.Fatal("fixture physical temp root unavailable")
			}
			selected := physical
			if alias {
				selected = filepath.Join(root, "alias")
				if os.Symlink(physical, selected) != nil {
					t.Fatal("fixture alias unavailable")
				}
			}
			resolvedSelected, err := filepath.EvalSymlinks(selected)
			if err != nil || resolvedSelected != physical || (selected != resolvedSelected) != alias {
				t.Fatal("fixture did not exercise the selected input alias")
			}
			outside := filepath.Join(root, "outside.txt")
			if os.WriteFile(outside, []byte("outside-fixed-sentinel"), 0600) != nil {
				t.Fatal("fixture sentinel unavailable")
			}
			t.Setenv("TMPDIR", selected)
			lookup := func(_ context.Context, service, account string) ([]byte, error) {
				if service != "Claude Code-credentials" || account != "" {
					t.Fatal("unexpected synthetic credential lookup")
				}
				return json.Marshal(map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fixture-only", "expiresAt": time.Now().Add(time.Hour).UnixMilli()}})
			}
			sum := sha256.Sum256([]byte("sf-cli-browser-auth/v1\x00claude\x00fixture-only"))
			ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			env, temporary, cleanup, err := vettedCLIEnvironment(ctx, "claude", hex.EncodeToString(sum[:]), lookup)
			if err != nil {
				t.Fatal("synthetic production environment refused")
			}
			defer cleanup()
			home, envTemporary := "", ""
			for _, value := range env {
				if strings.HasPrefix(value, "HOME=") {
					home = strings.TrimPrefix(value, "HOME=")
				}
				if strings.HasPrefix(value, "TMPDIR=") {
					envTemporary = strings.TrimPrefix(value, "TMPDIR=")
				}
			}
			resolvedTemporary, resolveErr := filepath.EvalSymlinks(temporary)
			if home == "" || envTemporary != temporary || resolveErr != nil || !strings.HasPrefix(resolvedTemporary, physical+string(os.PathSeparator)) {
				t.Fatal("fixture environment escaped its selected temporary root")
			}
			t.Logf("fixture alias_parent=%t returned_alias_spelling=%t", alias, temporary != resolvedTemporary)
			if temporary != resolvedTemporary {
				t.Fatal("production temporary directory was not canonicalized")
			}
			// Use the production return unchanged; normalization belongs to the
			// production environment boundary, not this native fixture.
			prefix, err := authoringSandboxCommand(trustedExecutable{stagedDir: filepath.Dir(self), stagedPath: self}, home, temporary)
			if err != nil {
				t.Fatal("production authoring profile refused")
			}
			cmd := exec.CommandContext(ctx, prefix[0], append(prefix[2:], "-test.run", "^TestAuthoringSandboxProductionTemporaryAlias$")...)
			cmd.Args[0] = prefix[1]
			cmd.Dir = temporary
			cmd.Env = append(env, "SF_AUTHORING_TMP_ALIAS_HELPER=1", "SF_AUTHORING_TMP_ALIAS_OUTSIDE="+outside)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			cmd.WaitDelay = time.Second
			cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
			var stdout, stderr limitedBuffer
			stdout.limit, stderr.limit = 16<<10, 16<<10
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			runErr := cmd.Run()
			if runErr != nil || stdout.truncated || stderr.truncated || !bytes.Contains(stdout.Bytes(), []byte("authoring-tmp-private-ok")) {
				t.Fatalf("native temporary boundary failed: alias=%t exit_ok=%t timed_out=%t stdout_present=%t stderr_present=%t truncated=%t home_ok=%t tmp_ok=%t outside_denied=%t child_denied=%t", alias, runErr == nil, ctx.Err() != nil, stdout.Len() > 0, stderr.Len() > 0, stdout.truncated || stderr.truncated, bytes.Contains(stdout.Bytes(), []byte("authoring-home-operations-ok")), bytes.Contains(stdout.Bytes(), []byte("authoring-tmp-operations-ok")), bytes.Contains(stdout.Bytes(), []byte("authoring-outside-denied")), bytes.Contains(stdout.Bytes(), []byte("authoring-child-denied")))
			}
		})
	}
}

func authoringTemporaryAliasHelper(t *testing.T) {
	t.Helper()
	for index, directory := range []string{os.Getenv("HOME"), os.Getenv("TMPDIR")} {
		file, err := os.CreateTemp(directory, "private-probe-")
		if err != nil {
			t.Fatal("private create denied")
		}
		path := file.Name()
		_, writeErr := file.WriteString("fixed-private-payload")
		closeErr := file.Close()
		contents, readErr := os.ReadFile(path)
		removeErr := os.Remove(path)
		if writeErr != nil || closeErr != nil || readErr != nil || removeErr != nil || string(contents) != "fixed-private-payload" {
			t.Fatal("private read/write/delete failed")
		}
		if index == 0 {
			fmt.Fprintln(os.Stdout, "authoring-home-operations-ok")
		} else {
			fmt.Fprintln(os.Stdout, "authoring-tmp-operations-ok")
		}
	}
	if _, err := os.ReadFile(os.Getenv("SF_AUTHORING_TMP_ALIAS_OUTSIDE")); err == nil {
		t.Fatal("outside read permitted")
	}
	fmt.Fprintln(os.Stdout, "authoring-outside-denied")
	if err := exec.Command("/bin/echo", "forbidden-child").Run(); err == nil {
		t.Fatal("child execution permitted")
	}
	fmt.Fprintln(os.Stdout, "authoring-child-denied")
	fmt.Fprintln(os.Stdout, "authoring-tmp-private-ok")
}
