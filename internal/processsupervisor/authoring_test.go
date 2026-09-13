package processsupervisor

import (
	"context"
	"errors"
	"fmt"
	"github.com/nysa-company/sf/internal/authoring"
	"github.com/nysa-company/sf/internal/contracts"
	"github.com/nysa-company/sf/internal/domain"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuthoringFixedNoToolsAndCombinedBound(t *testing.T) {
	argv := authoringArgv("opus")
	flags := map[string]string{}
	for i := 0; i+1 < len(argv); i++ {
		if strings.HasPrefix(argv[i], "--") {
			flags[argv[i]] = argv[i+1]
		}
	}
	for _, flag := range []string{"--tools", "--allowedTools"} {
		if value, ok := flags[flag]; !ok || value != "" {
			t.Fatalf("%s=%q", flag, value)
		}
	}
	if flags["--mcp-config"] != `{"mcpServers":{}}` || flags["--max-turns"] != "3" {
		t.Fatal("unbounded authoring options")
	}
	if _, err := authoringStdin(contracts.AuthoringInput{Purpose: "unknown", Prompt: "write"}); err == nil {
		t.Fatal("unknown purpose accepted")
	}
	if _, err := authoringStdin(contracts.AuthoringInput{Purpose: "ticket_draft", Prompt: "draft", Context: strings.Repeat("\t", 40<<10)}); err == nil {
		t.Fatal("expanded combined input accepted")
	}
	if _, err := authoringStdin(contracts.AuthoringInput{Purpose: "ticket_draft", Prompt: "draft"}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthoringSubscriptionArgvAndPolicyBinding(t *testing.T) {
	for _, purpose := range []string{"ticket_draft", "home_intent"} {
		schema, _, err := authoring.SchemaForPurpose(purpose)
		if err != nil {
			t.Fatal(err)
		}
		argv, err := authoringPurposeArgv("claude-sonnet-4-6", purpose)
		want := []string{"--print", "--output-format", "json", "--json-schema", schema, "--model", "claude-sonnet-4-6", "--restricted", "--safe-mode", "--no-session-persistence", "--permission-mode", "dontAsk", "--tools", "", "--allowedTools", "", "--disallowedTools", "mcp__*", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--max-turns", "3"}
		if err != nil || !reflect.DeepEqual(argv, want) {
			t.Fatal("subscription authoring argv changed")
		}
		for _, argument := range argv {
			if argument == "--bare" {
				t.Fatal("bare mode excludes subscription authentication")
			}
		}
	}
	const oldPolicy = "sf.authoring.claude/v1:2.1.263:private-cwd:empty-tools:empty-mcp:bare:restricted:safe-mode:json-schema:no-persistence:max-turns3:structured-retries1:90s:64KiB:private-filesystem"
	schemas := "\x00" + authoring.Schema + "\x00" + authoring.Instruction + "\x00" + authoring.HomeSchema + "\x00" + authoring.HomeInstruction
	oldDigest := contracts.AuthoringDigest([]byte(oldPolicy + schemas))
	v2Policy := strings.Replace(strings.Replace(oldPolicy, "/v1:", "/v2:", 1), ":bare:", ":subscription-oauth:", 1)
	v2Digest := contracts.AuthoringDigest([]byte(v2Policy + schemas))
	v3Policy := strings.Replace(v2Policy, "/v2:", "/v3:", 1) + ":private-internal-tmp"
	v3Digest := contracts.AuthoringDigest([]byte(v3Policy + schemas))
	newPolicy := strings.Replace(v3Policy, "/v3:", "/v4:", 1) + ":system-timezone-read"
	if authoringPolicyDigest() == oldDigest || authoringPolicyDigest() != contracts.AuthoringDigest([]byte(newPolicy+schemas)) {
		t.Fatal("subscription policy was not immutably versioned")
	}
	digest := strings.Repeat("a", 64)
	s := &Supervisor{authoringStages: map[string]trustedExecutable{"claude-sonnet-4-6": {digest: digest, authDigest: digest, policyDigest: authoringPolicyDigest()}}}
	claim := contracts.AuthoringClaim{Identity: domain.ProviderIdentity{Provider: "claude", Model: "claude-sonnet-4-6", Family: "anthropic-claude", Version: "2.1.263"}, BinaryDigest: digest, AuthDigest: digest, PolicyDigest: oldDigest}
	if _, _, err := s.acquireAuthoring(claim); !errors.Is(err, ErrUnclear) {
		t.Fatal("old bare policy claim admitted under new subscription policy")
	}
	claim.PolicyDigest = v2Digest
	if _, _, err := s.acquireAuthoring(claim); !errors.Is(err, ErrUnclear) {
		t.Fatal("old policy without private internal temp admitted")
	}
	stale := s.authoringStages[claim.Identity.Model]
	stale.policyDigest = v2Digest
	s.authoringStages[claim.Identity.Model] = stale
	if _, _, err := s.acquireAuthoring(claim); !errors.Is(err, ErrUnclear) {
		t.Fatal("matching old capability and claim bypassed current private temp policy")
	}
	claim.PolicyDigest = v3Digest
	stale.policyDigest = v3Digest
	s.authoringStages[claim.Identity.Model] = stale
	if _, _, err := s.acquireAuthoring(claim); !errors.Is(err, ErrUnclear) {
		t.Fatal("matching v3 capability and claim bypassed current timezone policy")
	}
}

func TestAuthoringPrivateInternalTemporaryOverridesHost(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal("canonical fixture root unavailable")
	}
	t.Setenv("TMPDIR", root)
	t.Setenv("CLAUDE_CODE_TMPDIR", "/outside/host-value")
	s, claim, input := gatedAuthoringFixture(t, `cat >/dev/null
test -n "$CLAUDE_CODE_TMPDIR" && test "$CLAUDE_CODE_TMPDIR" = "$TMPDIR" && test "$CLAUDE_CODE_TMPDIR" = "$(pwd -P)" || exit 9
printf private-temp-ok`)
	_, proof, runErr := s.RunAuthoring(context.Background(), claim, input, func(context.Context, contracts.ProviderLaunch) error { return nil })
	d := AuthoringRunDiagnostics(runErr)
	if !errors.Is(runErr, authoring.ErrContent) || d.Stage != "output_json" || !d.ExitObserved || d.ExitCode != 0 || !d.StdoutPresent || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, claim.LeaderEpoch, proof) {
		t.Fatal("authoring internal temporary environment did not override host or drain")
	}
}

// This fixture compiles the real sf gate when tests are later authorized.
// It never invokes a real provider or credential source.
func gatedAuthoringFixture(t *testing.T, body string) (*Supervisor, contracts.AuthoringClaim, contracts.AuthoringInput) {
	t.Helper()
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	s.Executable = filepath.Join(t.TempDir(), "sf")
	build := exec.Command("go", "build", "-o", s.Executable, "./cmd/sf")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real gate: %v\n%s", err, output)
	}
	program := []byte("#!/bin/sh\n" + body + "\n")
	path := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(path, program, 0700); err != nil {
		t.Fatal(err)
	}
	digest := contracts.AuthoringDigest(program)
	auth := strings.Repeat("b", 64)
	trusted := trustedExecutable{path: path, digest: digest, authDigest: auth, policyDigest: authoringPolicyDigest()}
	if err := trusted.stage(); err != nil {
		t.Fatal(err)
	}
	s.authoringStages = map[string]trustedExecutable{"claude-opus-4-6": trusted}
	s.authoringEnvironment = func(context.Context, string, string, cliSecretLookup) ([]string, string, func(), error) {
		return vettedEnvironment("")
	}
	s.authoringCommand = func(value trustedExecutable, _, _ string) ([]string, error) {
		return []string{value.stagedPath, value.stagedPath}, nil
	}
	s.SoftDrain, s.HardDrain = 200*time.Millisecond, 500*time.Millisecond
	input := contracts.AuthoringInput{Purpose: "ticket_draft", Prompt: "Draft a count ticket"}
	claim := contracts.AuthoringClaim{Purpose: "ticket_draft", Channel: domain.ChannelDev, Session: "session", TurnKey: "turn", Turn: 1, LeaderEpoch: 1, Identity: domain.ProviderIdentity{Provider: "claude", Model: "claude-opus-4-6", Family: "anthropic-claude", Version: "2.1.263"}, BinaryDigest: digest, AuthDigest: auth, PolicyDigest: authoringPolicyDigest(), ContextDigest: contracts.AuthoringDigest(nil), RequestDigest: contracts.AuthoringInputDigest(input)}
	return s, claim, input
}

func TestGatedAuthoringRecordsBeforeLaunchAndCancelsWithDrain(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "started")
	s, claim, input := gatedAuthoringFixture(t, fmt.Sprintf("cat >/dev/null\nprintf started > %q\nprintf partial\nsleep 30", marker))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		proof contracts.AuthoringProof
		err   error
	}
	done := make(chan outcome, 1)
	go func() {
		_, proof, err := s.RunAuthoring(ctx, claim, input, func(context.Context, contracts.ProviderLaunch) error {
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Error("provider escaped durable launch gate")
			}
			return nil
		})
		done <- outcome{proof, err}
	}()
	awaitFile(t, marker, 3*time.Second)
	select {
	case <-done:
		t.Fatal("silent process returned before cancellation")
	default:
	}
	cancel()
	select {
	case result := <-done:
		if result.err == nil || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, 1, result.proof) {
			t.Fatal("cancel did not prove drain")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel hung")
	}
	s.mu.Lock()
	remaining := len(s.authoringRuns)
	s.mu.Unlock()
	if remaining != 0 {
		t.Fatal("drained authoring process retained")
	}
}

func TestGatedAuthoringRecorderRefusalNeverReleasesProvider(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "forbidden")
	s, claim, input := gatedAuthoringFixture(t, fmt.Sprintf("printf forbidden > %q", marker))
	_, proof, err := s.RunAuthoring(context.Background(), claim, input, func(context.Context, contracts.ProviderLaunch) error { return errors.New("durable recorder refused") })
	if err == nil || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, 1, proof) {
		t.Fatal("refused gate was not safely drained")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unrecorded authoring provider ran")
	}
}

func TestGatedAuthoringIdentityFailureRetainsOwnershipUntilBoundedExit(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "forbidden")
	s, claim, input := gatedAuthoringFixture(t, fmt.Sprintf("printf forbidden > %q", marker))
	s.authoringIdentity = func(int) (string, error) { return "", errors.New("identity unavailable") }
	_, proof, err := s.RunAuthoring(context.Background(), claim, input, func(context.Context, contracts.ProviderLaunch) error {
		t.Fatal("identity failure reached recorder")
		return nil
	})
	if err == nil || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, 1, proof) {
		t.Fatal("unreleased gate did not retire safely")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("identity failure released provider")
	}
	if err := s.Close(); err != nil {
		t.Fatal("provisional identity failure leaked stage reference", err)
	}
}

func TestAuthoringPersistedLiveIdentityDrainAndWrongStartRefusal(t *testing.T) {
	s, claim, _ := gatedAuthoringFixture(t, "exit 0")
	cmd := exec.Command("/bin/sh", "-c", "sleep 30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	start, err := processStartIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	boot, err := hostBootIdentity()
	if err != nil {
		t.Fatal(err)
	}
	launch := contracts.ProviderLaunch{PID: cmd.Process.Pid, PGID: cmd.Process.Pid, ProcessStartIdentity: start, BootIdentity: boot, Worktree: t.TempDir()}
	wrong := launch
	wrong.ProcessStartIdentity = "wrong"
	if _, err := s.RecoverAuthoring(context.Background(), claim, wrong, 2); err == nil {
		t.Fatal("wrong process start accepted")
	}
	if syscall.Kill(cmd.Process.Pid, 0) != nil {
		t.Fatal("wrong identity signalled live process")
	}
	proof, err := s.RecoverAuthoring(context.Background(), claim, launch, 2)
	if err != nil || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, 2, proof) {
		t.Fatalf("live persisted drain: %v", err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery did not reap fixture")
	}
}
func TestAuthoringUnavailableRuntimeProducesNoLaunchProof(t *testing.T) {
	s, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := contracts.AuthoringInput{Purpose: "ticket_draft", Prompt: "draft"}
	digest := strings.Repeat("a", 64)
	claim := contracts.AuthoringClaim{Purpose: "ticket_draft", Channel: domain.ChannelDev, Session: "session", TurnKey: "turn", Turn: 1, LeaderEpoch: 1, Identity: domain.ProviderIdentity{Provider: "claude", Model: "opus", Family: "claude", Version: "2.1.263"}, BinaryDigest: digest, AuthDigest: digest, PolicyDigest: authoringPolicyDigest(), ContextDigest: contracts.AuthoringDigest(nil), RequestDigest: contracts.AuthoringInputDigest(input)}
	_, proof, err := s.RunAuthoring(context.Background(), claim, input, func(context.Context, contracts.ProviderLaunch) error {
		t.Fatal("unprepared runtime launched")
		return nil
	})
	if err == nil || !contracts.VerifyAuthoringProof(s.PublicKey(), claim, 1, proof) {
		t.Fatal("no-launch failure did not have exact drain proof")
	}
}
