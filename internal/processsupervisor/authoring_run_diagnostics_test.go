package processsupervisor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nysa-company/sf/internal/authoring"
	"github.com/nysa-company/sf/internal/contracts"
)

func TestAuthoringRunDiagnosticsAreClosedAndPreserveErrors(t *testing.T) {
	cause := errors.New("secret-token\x1b[31m provider transcript")
	for _, stage := range []string{"claim", "input", "binding", "auth", "sandbox", "gate_setup", "gate_start", "identity", "record", "gate_release", "cancel", "drain", "proof", "process_exit", "output_limit", "output_json", "result_envelope", "structured_output"} {
		err := &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: stage}, cause: cause}
		if !errors.Is(err, cause) || AuthoringRunDiagnostics(fmt.Errorf("outer: %w", err)).Stage != stage {
			t.Fatal("lost stage or error identity")
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "transcript") || strings.Contains(err.Error(), "\x1b") {
			t.Fatal("unsafe error rendering")
		}
	}
	for _, err := range []error{nil, cause, &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: "secret-token", ExitCode: 99}, cause: cause}} {
		if AuthoringRunDiagnostics(err) != (AuthoringRunDiagnostic{Stage: "unknown"}) {
			t.Fatal("unknown error exposed metadata")
		}
	}
}

func TestAuthoringPrelaunchDiagnosticsDoNotInventObservations(t *testing.T) {
	s := &Supervisor{}
	recorded := false
	_, proof, err := s.RunAuthoring(context.Background(), contracts.AuthoringClaim{}, contracts.AuthoringInput{}, func(context.Context, contracts.ProviderLaunch) error {
		recorded = true
		return nil
	})
	if !errors.Is(err, ErrUnclear) || AuthoringRunDiagnostics(err) != (AuthoringRunDiagnostic{Stage: "claim"}) {
		t.Fatal("prelaunch failure must have no exit or capture observations")
	}
	if recorded || proof.Claim != (contracts.AuthoringClaim{}) || proof.Epoch != 0 || len(proof.Signature) != 0 {
		t.Fatal("invalid claim gained launch or drain authority")
	}
}

func TestAuthoringStderrHintIsConservativeAndAdvisory(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"error: unknown option '--safe-mode'\n", "unknown_option"},
		{"unknown option '--max-turns'", "unknown_option"},
		{"error: option '--permission-mode <mode>' argument 'dontAsk' is invalid.", "invalid_option_value"},
		{"option '--max-turns' argument 'three' is invalid.\n", "invalid_option_value"},
		{"error: unknown option '--not-authored'", "unclassified"},
		{"error: unknown option '--bare'", "unclassified"},
		{"error: unknown option '--safe-mode' secret-token", "unclassified"},
		{"error: option '--permission-mode <secret-label>' argument 'dontAsk' is invalid.", "unclassified"},
		{"error: option '--permission-mode' argument 'dontAsk' is invalid. Allowed choices are secret-token.", "unclassified"},
		{"secret-token", "unclassified"},
		{"\x1b[31merror: unknown option '--safe-mode'", "unclassified"},
		{"error: unknown option '--safe-mode'\r\n", "unclassified"},
		{"error:\tunknown option '--safe-mode'", "unclassified"},
		{"error: unknown option '--safe-mode'\nerror: unknown option '--model'", "unclassified"},
		{"error: unknown option '--safe-mode'\n\n", "unclassified"},
		{"é", "unclassified"},
		{strings.Repeat("x", 513), "unclassified"},
		{"", "unclassified"},
	} {
		if got := classifyAuthoringStderr([]byte(test.raw), false); got != test.want {
			t.Fatal("unexpected fixed hint category")
		}
		if classifyAuthoringStderr([]byte(test.raw), true) != "unclassified" {
			t.Fatal("truncated capture classified")
		}
	}
	err := &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: "process_exit", CaptureKnown: true, ProcessReportedHint: "secret-token"}, cause: ErrUnclear}
	if AuthoringRunDiagnostics(err).ProcessReportedHint != "unclassified" || strings.Contains(err.Error(), "secret-token") {
		t.Fatal("arbitrary advisory hint exposed")
	}
	err.diagnostic.CaptureKnown = false
	if AuthoringRunDiagnostics(err).ProcessReportedHint != "" {
		t.Fatal("unobserved capture invented a hint")
	}
}

func TestAuthoringErrorFamilyBoundedTokens(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"EPERM", "permission"}, {"EACCES", "permission"},
		{"ENOENT", "path"}, {"ENOTDIR", "path"}, {"EROFS", "read_only"},
		{"ENOSPC", "storage"}, {"EDQUOT", "storage"},
		{"EMFILE", "resource"}, {"ENFILE", "resource"}, {"ENOMEM", "resource"}, {"EAGAIN", "resource"},
		{"EPERM EACCES\nEPERM", "permission"}, {"EPERM:ENOENT", "ambiguous"},
		{"EPERM ENOSPC EROFS", "ambiguous"},
		{strings.Repeat(" ", (16<<10)-5) + "EPERM", "permission"},
		{strings.Repeat(" ", (16<<10)-4) + "EPERM", "unclassified"},
		{"", "unclassified"}, {"ECONNREFUSED ETIMEDOUT EAUTH", "unclassified"},
		{"secret-EPERM-token", "unclassified"}, {"/private/ENOENT/file", "unclassified"},
		{"token=EPERM", "unclassified"}, {"EPERM=token", "unclassified"},
		{"EPERM.extra", "unclassified"}, {".EPERM", "unclassified"},
		{"EPERM\\file", "unclassified"}, {"\\EPERM", "unclassified"},
		{"EPERM_suffix", "unclassified"}, {"prefixEPERM", "unclassified"},
		{"EPERM1", "unclassified"}, {"eperm Eperm", "unclassified"},
		{"[EPERM]", "unclassified"}, {"{EPERM}", "unclassified"},
		{"EPERM\x00", "unclassified"}, {"EPERM\x7f", "unclassified"},
		{"EPERM\x1b[31m", "unclassified"}, {"EPERM é", "unclassified"},
		{"EPERM\xff", "unclassified"}, {"EPERM ENOENT\x00", "unclassified"},
	} {
		if got := classifyAuthoringErrorFamily([]byte(test.raw), false); got != test.want {
			t.Fatalf("unexpected closed family: got %s want %s", got, test.want)
		}
		if classifyAuthoringErrorFamily([]byte(test.raw), true) != "unclassified" {
			t.Fatal("truncated stderr produced an error family")
		}
	}
	for _, delimiter := range []string{" ", "\n", "\r", "\t", ":", ",", ";", "(", ")", "'", "\""} {
		if classifyAuthoringErrorFamily([]byte(delimiter+"EPERM"+delimiter), false) != "permission" {
			t.Fatal("approved delimiter rejected")
		}
	}
	for control := byte(0); control < 0x20; control++ {
		if control == '\n' || control == '\r' || control == '\t' {
			continue
		}
		if classifyAuthoringErrorFamily(append([]byte("EPERM ENOENT"), control), false) != "unclassified" {
			t.Fatal("control-bearing capture classified before full validation")
		}
	}
}

func TestAuthoringErrorFamilyAccessorIsClosed(t *testing.T) {
	cause := errors.New("secret-token provider content")
	for _, stage := range []string{"claim", "auth", "identity", "record", "gate_release", "drain"} {
		err := &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: stage, ProcessReportedErrorFamily: "permission"}, cause: cause}
		if AuthoringRunDiagnostics(err).ProcessReportedErrorFamily != "" {
			t.Fatal("pre-capture stage exposed reported error family")
		}
	}
	for _, family := range []string{"permission", "path", "read_only", "storage", "resource", "ambiguous", "unclassified"} {
		err := &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: "process_exit", CaptureKnown: true, ProcessReportedErrorFamily: family}, cause: cause}
		if AuthoringRunDiagnostics(fmt.Errorf("outer: %w", err)).ProcessReportedErrorFamily != family || !errors.Is(err, cause) || err.Error() != "authoring run: process_exit" {
			t.Fatal("advisory metadata changed error privacy or identity")
		}
		err.diagnostic.CaptureKnown = false
		if AuthoringRunDiagnostics(err).ProcessReportedErrorFamily != "" {
			t.Fatal("unobserved capture exposed a family")
		}
	}
	err := &authoringRunError{diagnostic: AuthoringRunDiagnostic{Stage: "process_exit", CaptureKnown: true, ProcessReportedErrorFamily: "secret-token"}, cause: cause}
	if AuthoringRunDiagnostics(err).ProcessReportedErrorFamily != "unclassified" || strings.Contains(err.Error(), "secret") {
		t.Fatal("arbitrary family escaped the closed accessor")
	}
	err.diagnostic.Stage = "secret-token"
	if AuthoringRunDiagnostics(err) != (AuthoringRunDiagnostic{Stage: "unknown"}) {
		t.Fatal("unknown stage exposed family metadata")
	}
}

func TestAuthoringGatedFailureDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, body, stage              string
		exit                           int
		stdout, stderr, outCap, errCap bool
	}{
		{"nonzero", "printf secret-token >&2; exit 7", "process_exit", 7, false, true, false, false},
		{"option hint", `printf '%s\n' "error: unknown option '--safe-mode'" >&2; exit 1`, "process_exit", 1, false, true, false, false},
		{"multiline permission", "printf '%s\\n' '" + strings.Repeat("x", 600) + "' 'EPERM' >&2; exit 1", "process_exit", 1, false, true, false, false},
		{"malformed", "printf secret-token", "output_json", 0, true, false, false, false},
		{"envelope", `printf '%s' '{"type":"result","subtype":"error","is_error":true}'`, "result_envelope", 0, true, false, false, false},
		{"structured", `printf '%s' '{"type":"result","subtype":"success","is_error":false,"structured_output":{}}'`, "structured_output", 0, true, false, false, false},
		{"stdout cap", "head -c 65537 /dev/zero", "output_limit", 0, true, false, true, false},
		{"stderr cap", "head -c 16385 /dev/zero >&2", "output_limit", 0, false, true, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, claim, input := gatedAuthoringFixture(t, "cat >/dev/null\n"+test.body)
			_, proof, err := s.RunAuthoring(context.Background(), claim, input, func(context.Context, contracts.ProviderLaunch) error { return nil })
			d := AuthoringRunDiagnostics(err)
			wantHint := "unclassified"
			if test.name == "option hint" {
				wantHint = "unknown_option"
			}
			if d.ProcessReportedHint != wantHint {
				t.Fatal("post-wait advisory hint did not match bounded capture")
			}
			wantFamily := "unclassified"
			if test.name == "multiline permission" {
				wantFamily = "permission"
			}
			if d.ProcessReportedErrorFamily != wantFamily {
				t.Fatal("post-wait error family did not match bounded capture")
			}
			if err == nil || d.Stage != test.stage || !d.ExitObserved || d.ExitCode != test.exit || d.Signal != 0 || !d.CaptureKnown || d.StdoutPresent != test.stdout || d.StderrPresent != test.stderr || d.StdoutTruncated != test.outCap || d.StderrTruncated != test.errCap {
				t.Fatalf("incorrect bounded metadata: %+v", d)
			}
			if !contracts.VerifyAuthoringProof(s.PublicKey(), claim, claim.LeaderEpoch, proof) {
				t.Fatal("diagnostics changed drain proof")
			}
			if strings.Contains(err.Error(), "secret-token") {
				t.Fatal("provider content escaped")
			}
			if (test.stage == "output_json" || test.stage == "result_envelope" || test.stage == "structured_output") && !errors.Is(err, authoring.ErrContent) {
				t.Fatal("parser error identity changed")
			}
		})
	}
}
