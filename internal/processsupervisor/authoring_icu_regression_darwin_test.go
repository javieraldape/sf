package processsupervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type authoringICUFacts struct {
	started, waited, absent, captured, truncated           bool
	readDenied, writeDenied, forkDenied, entered, positive bool
	exit, signal                                           int
}

// The caller retains every fixture directory on ambiguous ownership. Each run
// gets five seconds plus an independent five-second drain budget. No provider,
// credentials, preferences, or reported-path lookup occurs.
func authoringICURun(profile, helper, outside, home, temporary string) authoringICUFacts {
	facts := authoringICUFacts{exit: -1}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	cmd := exec.Command("/usr/bin/sandbox-exec", "-p", profile, helper, outside)
	cmd.Dir = temporary
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "HOME=" + home, "TMPDIR=" + temporary, "CLAUDE_CODE_TMPDIR=" + temporary, "MAX_STRUCTURED_OUTPUT_RETRIES=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = time.Second
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = 16<<10, 16<<10
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if ctx.Err() != nil || cmd.Start() != nil {
		facts.absent = true
		return facts
	}
	facts.started = true
	identity, identityErr := processStartIdentity(cmd.Process.Pid)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	select {
	case <-done:
		facts.waited = true
	case <-ctx.Done():
		current, err := processStartIdentity(cmd.Process.Pid)
		group, groupErr := syscall.Getpgid(cmd.Process.Pid)
		if identityErr == nil && err == nil && current == identity && groupErr == nil && group == cmd.Process.Pid {
			_ = syscall.Kill(-group, syscall.SIGKILL)
		} else {
			_ = cmd.Process.Kill()
		}
	}
	drain, stopDrain := context.WithTimeout(context.Background(), 5*time.Second)
	defer stopDrain()
	if !facts.waited {
		select {
		case <-done:
			facts.waited = true
		case <-drain.Done():
			return facts
		}
	}
	for {
		if syscall.Kill(-cmd.Process.Pid, 0) == syscall.ESRCH {
			facts.absent = true
			break
		}
		select {
		case <-drain.Done():
			return facts
		case <-time.After(20 * time.Millisecond):
		}
	}
	if cmd.ProcessState == nil {
		return facts
	}
	facts.captured = true
	facts.truncated = stdout.truncated || stderr.truncated
	facts.exit = cmd.ProcessState.ExitCode()
	if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		facts.signal = int(status.Signal())
	}
	// Only exact code-owned whole lines are projected. Captures never escape.
	for _, line := range bytes.Split(stdout.Bytes(), []byte{'\n'}) {
		switch string(line) {
		case "outside-read-denied":
			facts.readDenied = true
		case "outside-write-denied":
			facts.writeDenied = true
		case "fork-denied":
			facts.forkDenied = true
		case "icu-entered":
			facts.entered = true
		case "icu-positive":
			facts.positive = true
		}
	}
	return facts
}

// The helper and test binary must come from the same externally verified CI
// revision. A caller-supplied checksum alone does not attest provenance. No
// compile/download fallback exists here, including on developer machines.
func TestAuthoringICUTimeZoneSandbox(t *testing.T) {
	helper := os.Getenv("SF_TEST_AUTHORING_ICU_HELPER")
	expected := os.Getenv("SF_TEST_AUTHORING_ICU_HELPER_SHA256")
	if helper == "" && expected == "" {
		t.Skip("CI-built ICU helper not supplied; no ICU acceptance performed")
	}
	if helper == "" || expected == "" || !filepath.IsAbs(helper) || filepath.Clean(helper) != helper || len(expected) != 64 || strings.ToLower(expected) != expected {
		t.Fatal("invalid ICU helper artifact configuration")
	}
	if _, err := hex.DecodeString(expected); err != nil {
		t.Fatal("invalid ICU helper artifact digest")
	}
	resolved, err := filepath.EvalSymlinks(helper)
	if err != nil || resolved != helper {
		t.Fatal("ICU helper artifact is not canonical")
	}
	fd, err := unix.Open(helper, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal("ICU helper artifact unavailable")
	}
	file := os.NewFile(uintptr(fd), "icu-helper")
	info, statErr := file.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Size() <= 0 || info.Size() > 8<<20 {
		file.Close()
		t.Fatal("invalid ICU helper artifact type")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, (8<<20)+1))
	closeErr := file.Close()
	digest := sha256.Sum256(contents)
	if readErr != nil || closeErr != nil || len(contents) > 8<<20 || hex.EncodeToString(digest[:]) != expected {
		t.Fatal("ICU helper artifact verification failed")
	}
	root, err := os.MkdirTemp("", "sf-authoring-icu-")
	if err != nil {
		t.Fatal("ICU fixture directory unavailable")
	}
	safe := true
	cleanupRoot := root
	defer func() {
		if safe {
			_ = os.RemoveAll(cleanupRoot)
		}
	}()
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal("ICU fixture canonical directory unavailable")
	}
	stage, home, temporary := filepath.Join(root, "stage"), filepath.Join(root, "home"), filepath.Join(root, "tmp")
	for _, directory := range []string{stage, home, temporary} {
		if os.Mkdir(directory, 0700) != nil {
			t.Fatal("ICU fixture private directory unavailable")
		}
	}
	frozen := filepath.Join(stage, "icu-probe")
	if os.WriteFile(frozen, contents, 0700) != nil {
		t.Fatal("ICU helper freeze failed")
	}
	outside := filepath.Join(root, "outside-sentinel")
	const sentinel = "fixed-outside-sentinel"
	if os.WriteFile(outside, []byte(sentinel), 0600) != nil {
		t.Fatal("ICU fixture sentinel unavailable")
	}
	candidate, err := authoringSandbox(stage, frozen, home, temporary)
	if err != nil {
		t.Fatal("ICU candidate profile unavailable")
	}
	baseline := candidate
	for _, path := range []string{"/private/var/db/timezone", "/var/db/timezone"} {
		line := "(allow file-read* (subpath " + seatbeltString(path) + "))\n"
		if strings.Count(baseline, line) != 1 {
			t.Fatal("timezone grant is not exact and unique")
		}
		baseline = strings.Replace(baseline, line, "", 1)
	}
	for _, forbidden := range []string{`(subpath "/var/db")`, `(subpath "/private/var/db")`, `(allow process-fork)`} {
		if strings.Contains(candidate, forbidden) {
			t.Fatal("timezone profile widened unrelated authority")
		}
	}
	for _, test := range []struct {
		name, profile   string
		requirePositive bool
	}{{"baseline", baseline, false}, {"candidate", candidate, true}} {
		facts := authoringICURun(test.profile, frozen, outside, home, temporary)
		safe = !facts.started || facts.waited && facts.absent
		t.Logf("icu profile=%s started=%t waited=%t observed_group_absent=%t capture_known=%t truncated=%t exit_code=%d signal=%d outside_read_denied=%t outside_write_denied=%t fork_denied=%t icu_entered=%t count_positive=%t", test.name, facts.started, facts.waited, facts.absent, facts.captured, facts.truncated, facts.exit, facts.signal, facts.readDenied, facts.writeDenied, facts.forkDenied, facts.entered, facts.positive)
		if !safe || !facts.started || !facts.captured || facts.truncated {
			t.Fatal("ICU fixture ownership or capture failed; ambiguous resources retained")
		}
		if test.requirePositive && (!facts.readDenied || !facts.writeDenied || !facts.forkDenied || !facts.entered || !facts.positive || facts.exit != 0) {
			t.Fatal("candidate timezone enumeration or negative boundary failed")
		}
		if data, err := os.ReadFile(outside); err != nil || string(data) != sentinel {
			t.Fatal("ICU fixture outside sentinel changed")
		}
	}
}
