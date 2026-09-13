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
	files, err := StagePure(context.Background(), root, argv, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write("helpers.go", "package helper\nimport \"os/exec\"\n")
	data, err := os.ReadFile(files[0])
	if err != nil || strings.Contains(string(data), "os/exec") {
		t.Fatalf("snapshot changed: %v", err)
	}
	if _, err := ValidateCommand(root, argv, true); err == nil {
		t.Fatal("process import accepted")
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
