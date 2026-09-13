package processsupervisor

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuthoringSandboxPreflightClosedArgv(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	facts, _ := authoringSandboxProbe(cancelled, []string{"/must-not-execute", "fixture"}, "help", "/", nil, time.Second)
	if facts.Started || !facts.GroupAbsent {
		t.Fatal("expired context attempted probe")
	}
	for _, kind := range []string{"print", "status", "--help", "version --print", "", "help\n--print"} {
		if _, ok := authoringProbeFlag(kind); ok {
			t.Fatal("non-probe command admitted")
		}
		facts, _ := authoringSandboxProbe(context.Background(), []string{"/must-not-execute", "fixture"}, kind, "/", nil, time.Second)
		if facts.Started {
			t.Fatal("invalid probe attempted launch")
		}
	}
	for kind, expected := range map[string]string{"version": "--version", "help": "--help"} {
		if flag, ok := authoringProbeFlag(kind); !ok || flag != expected {
			t.Fatal("probe argv changed")
		}
	}
	file, err := parser.ParseFile(token.NewFileSet(), "authoring_sandbox_installed_darwin_test.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		if identifier, ok := node.(*ast.Ident); ok {
			switch identifier.Name {
			case "lookupCLISecret", "prepareCLICredentials", "vettedCLIEnvironment", "authoringArgv", "authoringPurposeArgv", "authoringStdin":
				t.Fatal("preflight gained direct authentication or inference helper")
			}
		}
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch selector.Sel.Name {
		case "RunAuthoring", "PrepareAuthoring", "ObserveClaudeRuntime", "CombinedOutput":
			t.Fatal("preflight gained inference/auth or raw-output execution surface")
		}
		return true
	})
	var native *ast.FuncDecl
	for _, declaration := range file.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == "TestInstalledClaudeAuthoringSandboxPreflight" {
			native = fn
		}
	}
	if native == nil {
		t.Fatal("missing native opt-in entrypoint")
	}
	printed := func(node ast.Node) string {
		var text bytes.Buffer
		if format.Node(&text, token.NewFileSet(), node) != nil {
			t.Fatal("invalid preflight syntax")
		}
		return text.String()
	}
	gate, ok := native.Body.List[0].(*ast.IfStmt)
	if !ok || printed(gate.Cond) != `os.Getenv("SF_TEST_CLAUDE_AUTHORING_SANDBOX_PREFLIGHT") != "1"` {
		t.Fatal("native entrypoint must begin with its dedicated exact opt-in")
	}
	getenv, sequence, probes, profiles := 0, 0, 0, 0
	ast.Inspect(native.Body, func(node ast.Node) bool {
		if loop, ok := node.(*ast.RangeStmt); ok && printed(loop.X) == `[]string{"version", "help"}` {
			sequence++
		}
		if call, ok := node.(*ast.CallExpr); ok {
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "Getenv":
					getenv++
				case "Command", "CommandContext", "StartProcess", "Exec":
					t.Fatal("native entrypoint bypassed the bounded probe helper")
				}
			}
			if name, ok := call.Fun.(*ast.Ident); ok {
				switch name.Name {
				case "authoringSandboxProbe":
					probes++
					if printed(call) != `authoringSandboxProbe(ctx, prefix, kind, tmp, env, 5*time.Second)` {
						t.Fatal("native probe invocation changed")
					}
				case "authoringSandboxCommand":
					profiles++
					if printed(call) != `authoringSandboxCommand(trusted, home, tmp)` {
						t.Fatal("native profile construction changed")
					}
				}
			}
		}
		return true
	})
	if getenv != 1 || sequence != 1 || probes != 1 || profiles != 1 {
		t.Fatal("native opt-in must use only the fixed version/help sandbox sequence")
	}
}

func TestAuthoringSandboxPreflightSyntheticCaptureAndDrain(t *testing.T) {
	for _, test := range []struct {
		name, body string
		truncated  bool
		exit       int
	}{
		{"exact argument", `test "$#" = 1 && test "$1" = --version || exit 4; printf fixture`, false, 0},
		{"exact help", `test "$#" = 1 && test "$1" = --help || exit 4; printf fixture`, false, 0},
		{"capture cap", "head -c 65537 /dev/zero", true, 0},
		{"nonzero", "exit 7", false, 7},
		{"bounded stop", "exec /bin/sleep 30", false, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "fixture.sh")
			if os.WriteFile(path, []byte(test.body), 0600) != nil {
				t.Fatal("fixture creation failed")
			}
			ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
			defer stop()
			kind := "version"
			if test.name == "exact help" {
				kind = "help"
			}
			facts, output := authoringSandboxProbe(ctx, []string{"/bin/sh", "/bin/sh", path}, kind, dir, []string{"PATH=/usr/bin:/bin"}, 2*time.Second)
			if !facts.Started || !facts.Waited || !facts.GroupAbsent || !facts.CaptureKnown || facts.ExitCode != test.exit || facts.Truncated != test.truncated || len(output) > 64<<10 {
				t.Fatal("synthetic probe violated bounded capture/drain")
			}
		})
	}
}
