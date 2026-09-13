//go:build darwin

package processsupervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAuthoringSandboxNativeHelperBoundary(t *testing.T) {
	if os.Getenv("SF_AUTHORING_SANDBOX_HELPER") == "1" {
		runAuthoringSandboxHelper(t)
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		t.Fatal(err)
	}
	stage := filepath.Dir(self)
	canonical := func(path string) string {
		path, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}
	home := canonical(t.TempDir())
	temporary := canonical(t.TempDir())
	outside := canonical(t.TempDir())
	privateFile := filepath.Join(home, "approved.txt")
	repositoryFile := filepath.Join(outside, "repository.txt")
	if err := os.WriteFile(privateFile, []byte("authoring-private-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(repositoryFile, []byte("must-not-read"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err := authoringSandbox(stage, self, home, temporary)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(profile, `(allow file-read* (literal "/"))`) || strings.Contains(profile, `(subpath "/")`) || strings.Contains(profile, "(allow process-fork)") {
		t.Fatal("native-loader baseline must grant only literal root, never descendants or child creation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-p", profile, self, "-test.run", "^TestAuthoringSandboxNativeHelperBoundary$")
	cmd.Dir = "/"
	cmd.Stdin = strings.NewReader("fixed-native-descriptor-probe")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Env = []string{
		"HOME=" + home,
		"TMPDIR=" + temporary,
		"SF_AUTHORING_SANDBOX_HELPER=1",
		"SF_AUTHORING_SANDBOX_PRIVATE=" + privateFile,
		"SF_AUTHORING_SANDBOX_REPOSITORY=" + repositoryFile,
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native authoring sandbox helper failed: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "authoring-sandbox-ok") {
		t.Fatalf("native authoring sandbox helper did not prove all boundaries: %q", out)
	}
	// Observational, no-model evidence only; these facts do not establish which
	// descriptor operation the installed provider performs during drafting.
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "authoring-descriptor-probe ") {
			t.Log(line)
		}
	}
}

func runAuthoringSandboxHelper(t *testing.T) {
	privateFile := os.Getenv("SF_AUTHORING_SANDBOX_PRIVATE")
	repositoryFile := os.Getenv("SF_AUTHORING_SANDBOX_REPOSITORY")
	contents, err := os.ReadFile(privateFile)
	if err != nil || string(contents) != "authoring-private-ok" {
		t.Fatalf("approved private read failed: %v %q", err, contents)
	}
	if contents, err := os.ReadFile(repositoryFile); err == nil || string(contents) == "must-not-read" {
		t.Fatalf("repository read unexpectedly succeeded: %v %q", err, contents)
	}
	if output, err := exec.Command("/bin/echo", "child").CombinedOutput(); err == nil {
		t.Fatalf("child exec unexpectedly succeeded: %q", output)
	}
	for _, probe := range []struct {
		name, path string
		flags      int
	}{
		{"stdin", "/dev/stdin", os.O_RDONLY},
		{"stdout", "/dev/stdout", os.O_WRONLY},
		{"stderr", "/dev/stderr", os.O_WRONLY},
		{"fd0", "/dev/fd/0", os.O_RDONLY},
		{"fd1", "/dev/fd/1", os.O_WRONLY},
		{"fd2", "/dev/fd/2", os.O_WRONLY},
	} {
		file, err := os.OpenFile(probe.path, probe.flags, 0)
		if err == nil && file.Close() != nil {
			t.Fatal("native descriptor probe close failed")
		}
		fmt.Fprintf(os.Stdout, "authoring-descriptor-probe name=%s open=%t permission=%t\n", probe.name, err == nil, errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES))
	}
	fmt.Fprintln(os.Stdout, "authoring-sandbox-ok")
}
