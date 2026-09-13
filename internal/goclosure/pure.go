package goclosure

import (
	"context"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// PureFlag selects a code-owned two-file recipe, never arbitrary Go flags.
const PureFlag = "--sf-go-pure-files-v1"
const pureFileLimit = 1 << 20

var purePath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*\.go$`)

// PurePaths requires one implementation and its matching test in one directory.
func PurePaths(argv []string) (string, string, error) {
	if len(argv) != 4 || argv[0] != "go" || argv[1] != PureFlag {
		return "", "", ErrInvalid
	}
	a, b := argv[2], argv[3]
	for _, p := range []string{a, b} {
		if !purePath.MatchString(p) || path.Clean(p) != p || strings.HasPrefix(p, "/") {
			return "", "", ErrInvalid
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." || strings.HasPrefix(part, ".") {
				return "", "", ErrInvalid
			}
		}
	}
	if strings.HasSuffix(a, "_test.go") || b != strings.TrimSuffix(a, ".go")+"_test.go" {
		return "", "", ErrInvalid
	}
	return a, b, nil
}

// ValidateCommand preserves the module recipe and separately checks explicit
// pure source. Only readiness may allow the not-yet-authored test to be absent.
func ValidateCommand(root string, argv []string, allowMissingTest bool) (bool, error) {
	if len(argv) == 3 && argv[0] == "go" && argv[1] == "test" && argv[2] == "./..." {
		return Validate(root)
	}
	_, err := readPure(context.Background(), root, argv, allowMissingTest)
	return false, err
}

// readPure uses an os.Root to prevent escape even if a path component changes.
// Every existing component must also be nonsymlinked. The returned exact bytes
// are parsed and later staged; no compiler rereads the mutable source checkout.
func readPure(ctx context.Context, root string, argv []string, allowMissingTest bool) ([][]byte, error) {
	a, b, err := PurePaths(argv)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, ErrInvalid
	}
	defer r.Close()
	var result [][]byte
	var packageName string
	for i, name := range []string{a, b} {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parts := strings.Split(name, "/")
		missing := false
		for j := range parts {
			info, err := r.Lstat(strings.Join(parts[:j+1], "/"))
			if os.IsNotExist(err) && i == 1 && j == len(parts)-1 && allowMissingTest {
				missing = true
				break
			}
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				return nil, ErrInvalid
			}
			if j < len(parts)-1 && !info.IsDir() {
				return nil, ErrInvalid
			}
			if j == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > pureFileLimit) {
				return nil, ErrInvalid
			}
		}
		if missing {
			result = append(result, nil)
			continue
		}
		// Nonblocking open prevents a raced FIFO/device from hanging readiness;
		// fstat below still requires a regular file before any bytes are read.
		f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			return nil, ErrInvalid
		}
		info, statErr := f.Stat()
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > pureFileLimit {
			f.Close()
			return nil, ErrInvalid
		}
		data, readErr := io.ReadAll(io.LimitReader(f, pureFileLimit+1))
		f.Close()
		if readErr != nil || len(data) > pureFileLimit || int64(len(data)) != info.Size() {
			return nil, ErrInvalid
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), name, data, parser.ParseComments|parser.AllErrors)
		if err != nil {
			return nil, ErrInvalid
		}
		if i == 0 {
			packageName = parsed.Name.Name
		}
		if parsed.Name.Name != packageName || packageName == "main" {
			return nil, ErrInvalid
		}
		for _, group := range parsed.Comments {
			for _, c := range group.List {
				if strings.HasPrefix(c.Text, "//go:") || strings.HasPrefix(c.Text, "// +build") {
					return nil, ErrInvalid
				}
			}
		}
		for _, imp := range parsed.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !pureImport(p) {
				return nil, ErrInvalid
			}
		}
		result = append(result, data)
	}
	return result, nil
}

func pureImport(p string) bool {
	switch p {
	case "bytes", "cmp", "encoding/hex", "encoding/json", "errors", "fmt", "math", "path", "path/filepath", "reflect", "regexp", "slices", "sort", "strconv", "strings", "testing", "unicode", "unicode/utf8":
		return true
	}
	return false
}

// StagePure writes only validated bytes into a new private directory under
// supervisor-owned scratch. The caller owns cleanup and must retain it on an
// ambiguous live process, just like the toolchain and command scratch.
func StagePure(ctx context.Context, root string, argv []string, scratch string) ([]string, error) {
	sources, err := readPure(ctx, root, argv, false)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(scratch, "pure-go-")
	if err != nil {
		return nil, err
	}
	var files []string
	for i, name := range []string{"source.go", "source_test.go"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, sources[i], 0400); err != nil {
			return nil, err
		}
		files = append(files, p)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		return nil, err
	}
	return files, ctx.Err()
}
