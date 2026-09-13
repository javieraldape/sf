package processsupervisor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/cliruntime"
)

type authoringProbeFacts struct {
	Started, Waited, GroupAbsent, CaptureKnown bool
	ExitCode                                   int
	StdoutPresent, StderrPresent, Truncated    bool
	ProcessReportedErrorFamily                 string
}

func authoringProbeArgs(kind string) ([]string, bool) {
	switch kind {
	case "version":
		return []string{"--version"}, true
	case "help":
		return []string{"--help"}, true
	case "auth_status":
		return []string{"--max-turns", "3", "--safe-mode", "--restricted", "auth", "status"}, true
	default:
		return nil, false
	}
}

// Fixed no-input probes only. The caller retains all directories unless Wait
// and group absence are observed. This is not a signed workflow drain proof.
func authoringSandboxProbe(ctx context.Context, prefix []string, kind, cwd string, env []string, limit time.Duration) (authoringProbeFacts, []byte) {
	facts := authoringProbeFacts{ExitCode: -1}
	args, ok := authoringProbeArgs(kind)
	if !ok || len(prefix) < 2 || limit <= 0 || limit > 5*time.Second {
		return facts, nil
	}
	cmd := exec.Command(prefix[0], append(append([]string{}, prefix[2:]...), args...)...)
	cmd.Args[0] = prefix[1]
	cmd.Dir, cmd.Env = cwd, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 64<<10, 16<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if ctx.Err() != nil {
		facts.GroupAbsent = true
		return facts, nil
	}
	if cmd.Start() != nil {
		facts.GroupAbsent = true
		return facts, nil
	}
	facts.Started = true
	identity, identityErr := processStartIdentity(cmd.Process.Pid)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	probeCtx, stop := context.WithTimeout(ctx, limit)
	defer stop()
	select {
	case <-done:
		facts.Waited = true
	case <-probeCtx.Done():
		current, err := processStartIdentity(cmd.Process.Pid)
		group, groupErr := syscall.Getpgid(cmd.Process.Pid)
		if identityErr == nil && err == nil && current == identity && groupErr == nil && group == cmd.Process.Pid {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		} else {
			// Only target the owned child handle when its group is ambiguous.
			_ = cmd.Process.Kill()
		}
	}
	drainCtx, stopDrain := context.WithTimeout(ctx, 5*time.Second)
	defer stopDrain()
	if !facts.Waited {
		select {
		case <-done:
			facts.Waited = true
		case <-drainCtx.Done():
			return facts, nil
		}
	}
	for {
		if syscall.Kill(-cmd.Process.Pid, 0) == syscall.ESRCH {
			facts.GroupAbsent = true
			break
		}
		select {
		case <-drainCtx.Done():
			return facts, nil
		case <-time.After(20 * time.Millisecond):
		}
	}
	facts.CaptureKnown = true
	facts.ExitCode = cmd.ProcessState.ExitCode()
	facts.StdoutPresent, facts.StderrPresent = stdout.Len() > 0, stderr.Len() > 0
	facts.Truncated = stdout.truncated || stderr.truncated
	facts.ProcessReportedErrorFamily = classifyAuthoringErrorFamily(stderr.Bytes(), stderr.truncated)
	return facts, append([]byte(nil), stdout.Bytes()...)
}

// Run only from an externally verified CI-built test artifact. This opt-in is
// independent of every spending flag. No auth lookup, PrepareAuthoring, gate,
// Store, print, status, prompt, or provider turn is used. Existing staging and
// snapshot helpers have separate bounded preparation deadlines. The probe loop
// shares a 30-second wall-clock context; a snapshot check may overrun it under
// its own deadline, but an expired context cannot launch the next probe.
func TestInstalledClaudeAuthoringSandboxPreflight(t *testing.T) {
	if os.Getenv("SF_TEST_CLAUDE_AUTHORING_SANDBOX_PREFLIGHT") != "1" {
		t.Skip("explicit no-model native sandbox preflight only")
	}
	executable, err := exec.LookPath("claude")
	if err != nil {
		t.Fatal("preflight stage=resolve")
	}
	resolveCtx, stopResolve := context.WithTimeout(context.Background(), 30*time.Second)
	bundle, err := cliruntime.Resolve(resolveCtx, "claude", executable)
	stopResolve()
	if err != nil {
		t.Fatal("preflight stage=resolve")
	}
	trusted := trustedExecutable{path: bundle.Executable(), digest: bundle.Digest(), cliBundle: &bundle}
	if trusted.stage() != nil {
		t.Fatal("preflight stage=staging")
	}
	safe := true
	defer func() {
		if safe {
			_ = os.RemoveAll(trusted.stagedDir)
		}
	}()
	env, tmp, cleanup, err := vettedEnvironment("")
	if err != nil {
		t.Fatal("preflight stage=environment")
	}
	defer func() {
		if safe {
			cleanup()
		}
	}()
	home := ""
	for i, value := range env {
		if strings.HasPrefix(value, "HOME=") {
			home, err = filepath.EvalSymlinks(strings.TrimPrefix(value, "HOME="))
			if err != nil {
				t.Fatal("preflight stage=environment")
			}
			env[i] = "HOME=" + home
		}
	}
	tmp, err = filepath.EvalSymlinks(tmp)
	if err != nil {
		t.Fatal("preflight stage=environment")
	}
	for i, value := range env {
		if strings.HasPrefix(value, "TMPDIR=") {
			env[i] = "TMPDIR=" + tmp
		}
	}
	env = append(env, "DISABLE_AUTOUPDATER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1")
	if !stagedRuntimeMatches(trusted.snapshot, trusted.digest) {
		t.Fatal("preflight stage=binding")
	}
	prefix, err := authoringSandboxCommand(trusted, home, tmp)
	if err != nil {
		t.Fatal("preflight stage=profile")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	for _, kind := range []string{"version", "help"} {
		facts, output := authoringSandboxProbe(ctx, prefix, kind, tmp, env, 5*time.Second)
		safe = !facts.Started || facts.Waited && facts.GroupAbsent
		matched := false
		if kind == "version" {
			matched = strings.TrimSpace(string(output)) == "2.1.263 (Claude Code)"
		} else {
			matched = true
			for _, flag := range []string{"--tools", "--restricted", "--safe-mode", "--json-schema", "--no-session-persistence", "--strict-mcp-config", "--permission-mode", "--allowedTools", "--disallowedTools"} {
				matched = matched && strings.Contains(string(output), flag)
			}
		}
		t.Logf("preflight kind=%s started=%t waited=%t observed_group_absent=%t capture_known=%t exit_code=%d stdout_present=%t stderr_present=%t truncated=%t expected_shape=%t", kind, facts.Started, facts.Waited, facts.GroupAbsent, facts.CaptureKnown, facts.ExitCode, facts.StdoutPresent, facts.StderrPresent, facts.Truncated, matched)
		if !safe || !facts.Started || !facts.CaptureKnown || facts.ExitCode != 0 || facts.Truncated || !matched {
			t.Fatal("preflight failed; no model calls; ambiguous resources retained")
		}
		if !stagedRuntimeMatches(trusted.snapshot, trusted.digest) {
			t.Fatal("preflight stage=binding")
		}
	}
}
