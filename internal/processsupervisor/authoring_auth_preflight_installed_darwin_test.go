package processsupervisor

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run only from an externally verified CI-built artifact. Preparation performs
// version/help/auth observation outside the profile, never inference. Its 25s
// context and staging's separate 30s bound are not a combined wall-clock bound.
// Snapshot checks have their own bounds; expired probe context prevents launch.
// This single authenticated sandbox probe has no model, prompt, or print argv.
func TestInstalledClaudeAuthoringAuthenticatedSandboxPreflight(t *testing.T) {
	if os.Getenv("SF_TEST_CLAUDE_AUTHORING_AUTH_PREFLIGHT") != "1" {
		t.Skip("explicit authenticated no-model sandbox preflight only")
	}
	s, err := New(nil)
	if err != nil {
		t.Fatal("auth preflight stage=supervisor")
	}
	safe := true
	cleanup := func() {}
	release := func() {}
	defer func() {
		if safe {
			cleanup()
			release()
		}
		if s.Close() != nil {
			t.Error("auth preflight stage=close; ambiguous resources retained")
		}
	}()
	const model = "claude-sonnet-4-6"
	prepareCtx, stopPrepare := context.WithTimeout(context.Background(), 60*time.Second)
	capability, err := s.PrepareAuthoring(prepareCtx, model)
	stopPrepare()
	if err != nil {
		t.Fatalf("auth preflight stage=preparation category=%s", AuthoringPreparationCategory(err))
	}
	s.mu.Lock()
	trusted, ok := s.authoringStages[model]
	bound := ok && !s.closed && !s.closing && trusted.snapshot != nil &&
		trusted.digest == capability.BinaryDigest && trusted.authDigest == capability.AuthDigest &&
		trusted.policyDigest == capability.PolicyDigest && capability.PolicyDigest == authoringPolicyDigest() &&
		capability.Identity.Provider == "claude" && capability.Identity.Model == model && capability.Identity.Version == "2.1.263"
	if bound {
		trusted.snapshot.refs++
		release = func() { s.releaseSnapshot(trusted.snapshot) }
	}
	s.mu.Unlock()
	if !bound || !stagedRuntimeMatches(trusted.snapshot, trusted.digest) {
		t.Fatal("auth preflight stage=binding")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	env, tmp, clean, err := vettedCLIEnvironment(ctx, "claude", capability.AuthDigest, lookupCLISecret)
	if err != nil {
		t.Fatal("auth preflight stage=environment")
	}
	cleanup = clean
	env = append(env, "MAX_STRUCTURED_OUTPUT_RETRIES=1")
	home := ""
	for _, value := range env {
		if strings.HasPrefix(value, "HOME=") {
			home = strings.TrimPrefix(value, "HOME=")
		}
	}
	prefix, err := authoringSandboxCommand(trusted, home, tmp)
	if err != nil {
		t.Fatal("auth preflight stage=profile")
	}
	facts, output := authoringSandboxProbe(ctx, prefix, "auth_status", tmp, env, 5*time.Second)
	safe = !facts.Started || facts.Waited && facts.GroupAbsent
	matched := facts.CaptureKnown && !facts.Truncated && validObservedClaudeAuth(output)
	t.Logf("auth preflight started=%t waited=%t observed_group_absent=%t capture_known=%t exit_code=%d stdout_present=%t stderr_present=%t truncated=%t auth_shape=%t process_reported_error_family=%s", facts.Started, facts.Waited, facts.GroupAbsent, facts.CaptureKnown, facts.ExitCode, facts.StdoutPresent, facts.StderrPresent, facts.Truncated, matched, facts.ProcessReportedErrorFamily)
	if !safe || !facts.Started || !facts.CaptureKnown || facts.ExitCode != 0 || facts.Truncated || !matched {
		t.Fatal("auth preflight failed; no model calls; ambiguous resources retained")
	}
	if !stagedRuntimeMatches(trusted.snapshot, trusted.digest) {
		t.Fatal("auth preflight stage=binding")
	}
}
