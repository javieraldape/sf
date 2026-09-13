package goclosure

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPureGoRecipeAndSnapshot(t *testing.T) {
	root := t.TempDir()
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.test/deps\ngo 1.25\nrequire example.test/unavailable v1.0.0\n")
	write("helpers.go", "package helper\nimport \"path/filepath\"\nfunc Extension(p string) string { return filepath.Ext(p) }\n")
	argv := []string{"go", PureFlag, "helpers.go", "helpers_test.go"}
	if _, err := ValidateCommand(root, argv, true); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateCommand(root, argv, false); err == nil {
		t.Fatal("missing test accepted for execution")
	}
	write("helpers_test.go", "package helper\nimport \"testing\"\nfunc TestExtension(t *testing.T) { if Extension(\"a.go\") != \".go\" { t.Fatal(\"extension\") } }\n")
	authenticated, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	files, cleanup, err := StagePure(context.Background(), root, authenticated, argv, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	write("helpers.go", "package helper\nimport \"os/exec\"\n")
	data, err := os.ReadFile(files[0])
	if err != nil || strings.Contains(string(data), "os/exec") {
		t.Fatalf("snapshot changed: %v", err)
	}
	if _, err := ValidateCommand(root, argv, true); err == nil {
		t.Fatal("process import accepted")
	}
	cleanup()
	if _, err := os.Stat(filepath.Dir(files[0])); !os.IsNotExist(err) {
		t.Fatalf("drained snapshot not removed: %v", err)
	}
}

func TestPureGoRejectsReplacedAuthenticatedRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "worktree")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	fd, err := os.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	authenticated, err := fd.Stat()
	if err != nil {
		t.Fatal(err)
	}
	// Deterministic replacement after authentication but before staging.
	if err := os.Rename(root, filepath.Join(parent, "original")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"x.go", "x_test.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package p\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, cleanup, err := StagePure(context.Background(), root, authenticated, []string{"go", PureFlag, "x.go", "x_test.go"}, t.TempDir())
	if cleanup != nil {
		defer cleanup()
	}
	if err == nil || len(files) != 0 {
		t.Fatal("replacement root accepted")
	}
}

func TestPureGoRejectsInvalidInputs(t *testing.T) {
	for _, argv := range [][]string{
		{"go", PureFlag, "../x.go", "../x_test.go"},
		{"go", PureFlag, "/x.go", "/x_test.go"},
		{"go", PureFlag, "x.go", "other_test.go"},
		{"go", PureFlag, "x.go", "x_test.go", "-exec=evil"},
		{"go", PureFlag, "x_test.go", "x_test_test.go"},
		{"go", PureFlag, "x//y.go", "x//y_test.go"},
	} {
		if _, _, err := PurePaths(argv); err == nil {
			t.Fatalf("accepted %q", argv)
		}
	}
	for name, source := range map[string]string{
		"external":  "package p\nimport \"example.com/pkg\"",
		"network":   "package p\nimport \"net/http\"",
		"unsafe":    "package p\nimport \"unsafe\"",
		"cgo":       "package p\nimport \"C\"",
		"directive": "package p\n//go:linkname x y\nvar x int",
		"embed":     "package p\nimport \"embed\"",
		"syntax":    "not go",
		"oversized": strings.Repeat(" ", pureFileLimit+1),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "x.go"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := ValidateCommand(root, []string{"go", PureFlag, "x.go", "x_test.go"}, true); err == nil {
				t.Fatal("accepted forbidden source")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		target := t.TempDir()
		if err := os.WriteFile(filepath.Join(target, "x.go"), []byte("package p"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
			t.Fatal(err)
		}
		if _, err := ValidateCommand(root, []string{"go", PureFlag, "linked/x.go", "linked/x_test.go"}, true); err == nil {
			t.Fatal("symlink accepted")
		}
	})
}
