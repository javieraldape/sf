package processsupervisor

import "errors"

// AuthoringRunDiagnostic contains observations only, not process authority or
// provider content. ExitCode/Signal are meaningful only when ExitObserved.
type AuthoringRunDiagnostic struct {
	Stage           string
	ExitObserved    bool
	ExitCode        int
	Signal          int
	CaptureKnown    bool
	StdoutPresent   bool
	StderrPresent   bool
	StdoutTruncated bool
	StderrTruncated bool
}

type authoringRunError struct {
	diagnostic AuthoringRunDiagnostic
	cause      error
}

func (e *authoringRunError) Error() string {
	return "authoring run: " + AuthoringRunDiagnostics(e).Stage
}
func (e *authoringRunError) Unwrap() error { return e.cause }

// AuthoringRunDiagnostics never renders the wrapped error or provider output.
// A nil or unrecognized error does not imply success.
func AuthoringRunDiagnostics(err error) AuthoringRunDiagnostic {
	var failure *authoringRunError
	if errors.As(err, &failure) && failure != nil {
		d := failure.diagnostic
		switch d.Stage {
		case "claim", "input", "binding", "auth", "sandbox", "gate_setup", "gate_start", "identity", "record", "gate_release", "cancel", "drain", "proof", "process_exit", "output_limit", "output_json", "result_envelope", "structured_output":
			return d
		}
	}
	return AuthoringRunDiagnostic{Stage: "unknown"}
}
