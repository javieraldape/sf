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
	// Path categories are lexical reports, not physical location or causal proof.
	ProcessReportedPathRoot string
	ProcessReportedPathName string
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
			d.ProcessReportedPathRoot = ""
			d.ProcessReportedPathName = ""
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
			switch d.ProcessReportedPathRoot {
			case "private_home", "private_tmp", "runtime", "system", "dev", "outside", "ambiguous", "unclassified":
			default:
				d.ProcessReportedPathRoot = "unclassified"
			}
			switch d.ProcessReportedPathName {
			case "null", "zero", "random", "urandom", "stdin", "stdout", "stderr", "tty", "claude_config", "settings", "managed_settings", "instructions", "other", "ambiguous", "unclassified":
			default:
				d.ProcessReportedPathName = "unclassified"
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

// classifyAuthoringPath reports lexical categories only, without filesystem
// inspection. Standalone path metadata is gated by whole-capture permission/open
// reports; that does not associate frames or prove the path caused any failure.
// Canonical known roots come from the launch, never from reported content.
func classifyAuthoringPath(raw []byte, truncated bool, home, temporary, runtimeRoot string) (string, string) {
	unknown := func() (string, string) { return "unclassified", "unclassified" }
	if truncated || len(raw) == 0 || len(raw) > 16<<10 {
		return unknown()
	}
	for _, b := range raw {
		if (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b > 0x7e {
			return unknown()
		}
	}
	clean := func(path string) bool {
		if len(path) == 0 || len(path) > 1024 || path[0] != '/' {
			return false
		}
		for _, b := range []byte(path) {
			if b < 0x20 || b > 0x7e || b == '\\' || b == '\'' || b == '"' {
				return false
			}
		}
		if path == "/" {
			return true
		}
		for _, component := range strings.Split(path[1:], "/") {
			if component == "" || component == "." || component == ".." {
				return false
			}
		}
		return true
	}
	roots := []struct{ path, category string }{{home, "private_home"}, {temporary, "private_tmp"}, {runtimeRoot, "runtime"}}
	for _, root := range roots {
		if !clean(root.path) || root.path == "/" {
			return unknown()
		}
	}
	if home == temporary || home == runtimeRoot || temporary == runtimeRoot {
		return unknown()
	}
	metadataAllowed := classifyAuthoringErrorFamily(raw, false) == "permission" && classifyAuthoringOperation(raw, false) == "open"
	quoted := func(value string) (string, bool) {
		if len(value) < 3 || (value[0] != '\'' && value[0] != '"') || value[len(value)-1] != value[0] {
			return "", false
		}
		path := value[1 : len(value)-1]
		return path, clean(path)
	}
	selected := ""
	conflicting := false
	for _, rawLine := range strings.Split(string(raw), "\n") {
		line := strings.Trim(rawLine, " \t\r")
		value, recognized := "", false
		if strings.HasPrefix(line, "path:") {
			if !metadataAllowed {
				return unknown()
			}
			recognized = true
			if !strings.HasPrefix(line, "path: ") {
				return unknown()
			}
			value = strings.TrimSuffix(strings.TrimPrefix(line, "path: "), ",")
		} else {
			line = strings.TrimPrefix(line, "Error: ")
			for _, prefix := range []string{"EPERM: operation not permitted, open ", "EPERM: operation not permitted, openat ", "EACCES: permission denied, open ", "EACCES: permission denied, openat "} {
				if strings.HasPrefix(line, prefix) {
					value, recognized = strings.TrimPrefix(line, prefix), true
					break
				}
			}
		}
		if !recognized {
			continue
		}
		path, ok := quoted(value)
		if !ok {
			return unknown()
		}
		if selected != "" && selected != path {
			conflicting = true
		}
		selected = path
	}
	if conflicting {
		return "ambiguous", "ambiguous"
	}
	if selected == "" {
		return unknown()
	}
	rootCategory, longest := "outside", 0
	for _, root := range roots {
		if (selected == root.path || strings.HasPrefix(selected, root.path+"/")) && len(root.path) > longest {
			rootCategory, longest = root.category, len(root.path)
		}
	}
	if longest == 0 {
		for _, root := range []string{"/System", "/usr/lib", "/usr/share", "/Library/Apple", "/private/etc"} {
			if selected == root || strings.HasPrefix(selected, root+"/") {
				rootCategory = "system"
			}
		}
		if selected == "/dev" || strings.HasPrefix(selected, "/dev/") {
			rootCategory = "dev"
		}
	}
	name := "other"
	switch selected {
	case "/dev/null":
		name = "null"
	case "/dev/zero":
		name = "zero"
	case "/dev/random":
		name = "random"
	case "/dev/urandom":
		name = "urandom"
	case "/dev/stdin", "/dev/fd/0":
		name = "stdin"
	case "/dev/stdout", "/dev/fd/1":
		name = "stdout"
	case "/dev/stderr", "/dev/fd/2":
		name = "stderr"
	case "/dev/tty":
		name = "tty"
	default:
		switch selected[strings.LastIndex(selected, "/")+1:] {
		case ".claude.json":
			name = "claude_config"
		case "settings.json":
			name = "settings"
		case "managed-settings.json":
			name = "managed_settings"
		case "CLAUDE.md":
			name = "instructions"
		}
	}
	return rootCategory, name
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
