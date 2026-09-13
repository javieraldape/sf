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
	// ProcessReportedErrorFamily classifies only reported OS-code tokens. It
	// does not establish an actual syscall, sandbox denial, origin, or API call.
	ProcessReportedErrorFamily string
	// ProcessReportedOperation is a spoofable reported frame, not syscall or origin proof.
	ProcessReportedOperation string
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
			d.ProcessReportedErrorFamily = ""
			d.ProcessReportedOperation = ""
		} else {
			switch d.ProcessReportedHint {
			case "unknown_option", "invalid_option_value":
			default:
				d.ProcessReportedHint = "unclassified"
			}
			switch d.ProcessReportedErrorFamily {
			case "permission", "path", "read_only", "storage", "resource", "ambiguous", "unclassified":
			default:
				d.ProcessReportedErrorFamily = "unclassified"
			}
			switch d.ProcessReportedOperation {
			case "spawn", "thread", "open", "read", "write", "mkdir", "cwd", "temp", "socket", "connect", "priority", "signal", "metadata_write", "metadata_read", "file_change", "home", "ambiguous", "unclassified":
			default:
				d.ProcessReportedOperation = "unclassified"
			}
		}
		switch d.Stage {
		case "claim", "input", "binding", "auth", "sandbox", "gate_setup", "gate_start", "identity", "record", "gate_release", "cancel", "drain", "proof", "process_exit", "output_limit", "output_json", "result_envelope", "structured_output":
			return d
		}
	}
	return AuthoringRunDiagnostic{Stage: "unknown"}
}

// classifyAuthoringErrorFamily scans bounded stderr without retaining any
// fragments. Even a recognized token is only an untrusted process report.
// Colons are token delimiters, not authenticated error-frame markers: arbitrary
// text such as "label:EPERM" can report a family without proving an OS error.
func classifyAuthoringErrorFamily(raw []byte, truncated bool) string {
	if truncated || len(raw) == 0 || len(raw) > 16<<10 {
		return "unclassified"
	}
	for _, value := range raw {
		if (value < 0x20 && value != '\n' && value != '\r' && value != '\t') || value > 0x7e {
			return "unclassified"
		}
	}
	boundary := func(value byte) bool {
		switch value {
		case ' ', '\n', '\r', '\t', ':', ',', ';', '(', ')', '\'', '"':
			return true
		default:
			return false
		}
	}
	family := "unclassified"
	for start := 0; start < len(raw); {
		if boundary(raw[start]) {
			start++
			continue
		}
		end := start
		for end < len(raw) && !boundary(raw[end]) {
			end++
		}
		candidate := ""
		switch string(raw[start:end]) {
		case "EPERM", "EACCES":
			candidate = "permission"
		case "ENOENT", "ENOTDIR":
			candidate = "path"
		case "EROFS":
			candidate = "read_only"
		case "ENOSPC", "EDQUOT":
			candidate = "storage"
		case "EMFILE", "ENFILE", "ENOMEM", "EAGAIN":
			candidate = "resource"
		}
		if candidate != "" {
			if family != "unclassified" && family != candidate {
				return "ambiguous"
			}
			family = candidate
		}
		start = end
	}
	return family
}

// classifyAuthoringOperation accepts only anchored synthetic conventional frames.
// An exact syscall metadata line alone can be spoofed; no origin is established.
// Optional quoted suffixes are discarded without interpreting their contents.
func classifyAuthoringOperation(raw []byte, truncated bool) string {
	if truncated || len(raw) == 0 || len(raw) > 16<<10 {
		return "unclassified"
	}
	for _, b := range raw {
		if (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b > 0x7e {
			return "unclassified"
		}
	}
	operations := []struct{ name, category string }{
		{"spawn", "spawn"}, {"posix_spawn", "spawn"}, {"pthread_create", "thread"},
		{"open", "open"}, {"openat", "open"}, {"read", "read"}, {"write", "write"},
		{"mkdir", "mkdir"}, {"mkdirat", "mkdir"}, {"chdir", "cwd"}, {"getcwd", "cwd"},
		{"mkdtemp", "temp"}, {"mkstemp", "temp"}, {"socket", "socket"},
		{"connect", "connect"}, {"setpriority", "priority"},
		{"kill", "signal"}, {"chmod", "metadata_write"}, {"fchmod", "metadata_write"}, {"chown", "metadata_write"},
		{"stat", "metadata_read"}, {"lstat", "metadata_read"}, {"access", "metadata_read"}, {"realpath", "metadata_read"}, {"readlink", "metadata_read"},
		{"rename", "file_change"}, {"unlink", "file_change"}, {"symlink", "file_change"}, {"uv_cwd", "cwd"}, {"uv_os_homedir", "home"},
	}
	codes := []struct{ code, message string }{
		{"EPERM", "operation not permitted"}, {"EACCES", "permission denied"},
		{"ENOENT", "no such file or directory"}, {"ENOTDIR", "not a directory"},
		{"EROFS", "read-only file system"}, {"ENOSPC", "no space left on device"},
		{"EDQUOT", "disk quota exceeded"}, {"EMFILE", "too many open files"},
		{"ENFILE", "file table overflow"}, {"ENOMEM", "not enough memory"},
		{"EAGAIN", "resource temporarily unavailable"},
	}
	frame := func(line, prefix string) bool {
		if line == prefix {
			return true
		}
		if len(line) < len(prefix)+4 || !strings.HasPrefix(line, prefix+" '") || !strings.HasSuffix(line, "'") {
			return false
		}
		suffix := line[len(prefix)+2 : len(line)-1]
		return len(suffix) > 0 && len(suffix) <= 1024 && !strings.ContainsAny(suffix, "'\\\r\t")
	}
	result := "unclassified"
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.Trim(rawLine, " \t\r")
		for _, operation := range operations {
			matched := false
			for _, quote := range []string{"'", "\""} {
				metadata := "syscall: " + quote + operation.name + quote
				matched = matched || line == metadata || line == metadata+","
			}
			errorLine := strings.TrimPrefix(line, "Error: ")
			for _, code := range codes {
				matched = matched || frame(errorLine, operation.name+" "+code.code) || frame(errorLine, code.code+": "+code.message+", "+operation.name)
			}
			if matched {
				if result != "unclassified" && result != operation.category {
					return "ambiguous"
				}
				result = operation.category
			}
		}
	}
	return result
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
	for _, option := range []string{"--print", "--output-format", "--json-schema", "--model", "--restricted", "--safe-mode", "--no-session-persistence", "--permission-mode", "--tools", "--allowedTools", "--disallowedTools", "--strict-mcp-config", "--mcp-config", "--max-turns"} {
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
