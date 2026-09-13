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

func TestAuthoringGatedFailureDiagnostics(t *testing.T) {
	for _, test := range []struct {
		name, body, stage              string
		exit                           int
		stdout, stderr, outCap, errCap bool
	}{
		{"nonzero", "printf secret-token >&2; exit 7", "process_exit", 7, false, true, false, false},
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
