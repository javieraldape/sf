package processsupervisor

import (
	"errors"
	"strings"
)

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
	// ProcessReportedHint is an untrusted advisory category, not proof of
	// origin, root cause, or whether any provider API request occurred.
	ProcessReportedHint string
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
		if !d.CaptureKnown {
			d.ProcessReportedHint = ""
		} else {
			switch d.ProcessReportedHint {
			case "unknown_option", "invalid_option_value":
			default:
				d.ProcessReportedHint = "unclassified"
			}
		}
		switch d.Stage {
		case "claim", "input", "binding", "auth", "sandbox", "gate_setup", "gate_start", "identity", "record", "gate_release", "cancel", "drain", "proof", "process_exit", "output_limit", "output_json", "result_envelope", "structured_output":
			return d
		}
	}
	return AuthoringRunDiagnostic{Stage: "unknown"}
}

// classifyAuthoringStderr recognizes only conventional synthetic option-error
// forms. These forms are not a claim about the installed Claude's formatter.
// No captured fragment is retained or returned. Unfamiliar output stays unknown.
func classifyAuthoringStderr(raw []byte, truncated bool) string {
	if truncated || len(raw) == 0 || len(raw) > 512 {
		return "unclassified"
	}
	line := strings.TrimSuffix(string(raw), "\n")
	if line == "" {
		return "unclassified"
	}
	for _, value := range []byte(line) {
		if value < 0x20 || value > 0x7e {
			return "unclassified"
		}
	}
	line = strings.TrimPrefix(line, "error: ")
	for _, option := range []string{"--print", "--output-format", "--json-schema", "--model", "--bare", "--restricted", "--safe-mode", "--no-session-persistence", "--permission-mode", "--tools", "--allowedTools", "--disallowedTools", "--strict-mcp-config", "--mcp-config", "--max-turns"} {
		if line == "unknown option '"+option+"'" {
			return "unknown_option"
		}
		for _, descriptor := range []string{option, authoringOptionDescriptor(option)} {
			if descriptor == "" {
				continue
			}
			prefix := "option '" + descriptor + "' argument '"
			const suffix = "' is invalid."
			if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
				continue
			}
			value := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
			if len(value) > 0 && len(value) <= 128 && !strings.ContainsAny(value, "'\\") {
				return "invalid_option_value"
			}
		}
	}
	return "unclassified"
}

func authoringOptionDescriptor(option string) string {
	// Only exact code-owned descriptors; no parsed/reflected placeholder names.
	switch option {
	case "--permission-mode":
		return "--permission-mode <mode>"
	case "--max-turns":
		return "--max-turns <turns>"
	default:
		return ""
	}
}
