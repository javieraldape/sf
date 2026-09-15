package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
)

func TestDoctorFixCommandAcceptsRepoBeforeOrAfterSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"doctor", "--repo", ".", "fix", "--dry-run"},
		{"doctor", "fix", "--repo", ".", "--dry-run"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			deps := isolatedDoctorFixDeps(t)
			var selected string
			var output bytes.Buffer
			a := newApp(nil, &output, &output)
			a.channel = domain.ChannelDev
			a.doctorFixDeps = func(_ domain.Channel, repo string) DoctorFixDeps {
				selected = repo
				return deps
			}
			command := a.command()
			command.SetArgs(args)
			if _, err := command.ExecuteContextC(context.Background()); err != nil {
				t.Fatal(err)
			}
			if !filepath.IsAbs(selected) || a.last == nil {
				t.Fatalf("repo was not parsed canonically: selected=%q response=%+v", selected, a.last)
			}
		})
	}
}

func TestDoctorFixJSONRequiresYesWithoutMutation(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	var output bytes.Buffer
	a := newApp(nil, &output, &output)
	a.channel = domain.ChannelDev
	a.interactive = func() bool { return true }
	a.doctorFixDeps = func(domain.Channel, string) DoctorFixDeps { return deps }
	command := a.command()
	command.SetArgs([]string{"--json", "doctor", "fix"})
	if _, err := command.ExecuteContextC(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.last == nil || a.last.OK || a.last.Error == nil || a.last.Error.Code != "operator_action_required" || a.last.Mutation.Attempted {
		t.Fatalf("JSON repair did not refuse safely: %+v", a.last)
	}
	if _, err := os.Lstat(deps.Doctor.Paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("JSON confirmation refusal mutated root: %v", err)
	}
}

func isolatedDoctorFixDeps(t *testing.T) DoctorFixDeps {
	t.Helper()
	home := t.TempDir()
	base := filepath.Join(home, "Library", "Application Support")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	paths, err := config.PathsFor(home, domain.ChannelDev)
	if err != nil {
		t.Fatal(err)
	}
	return DoctorFixDeps{Home: home, Doctor: DoctorDeps{
		Channel: domain.ChannelDev, Binary: "sf-dev", Paths: paths,
		Lookup: func(string) (string, error) { return "/bin/tool", nil },
		StatFS: func(string) (*syscall.Statfs_t, error) {
			return &syscall.Statfs_t{Bavail: 100_000, Bsize: 4096}, nil
		},
	}}
}

func TestDoctorFixRepairsIsolatedDirectoriesAndSecondRunIsIdempotent(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	first := RunDoctorFix(context.Background(), deps, true)
	if first.DryRun {
		t.Fatal("applied repair reported dry run")
	}
	for _, target := range []string{deps.Doctor.Paths.Root, filepath.Dir(deps.Doctor.Paths.Socket), deps.Doctor.Paths.Logs, deps.Doctor.Paths.Events, deps.Doctor.Paths.Worktrees, deps.Doctor.Paths.Backups} {
		info, err := os.Lstat(target)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("target was not repaired safely: mode=%v err=%v", info, err)
		}
	}
	second := RunDoctorFix(context.Background(), deps, true)
	for _, action := range second.Actions {
		if action.Status != DoctorFixNoop {
			t.Fatalf("second repair was not idempotent: %+v", second.Actions)
		}
	}
	response := doctorFixResponse(second, "sf-dev")
	if response.Mutation.Attempted {
		t.Fatalf("idempotent run claimed mutation: %+v", response.Mutation)
	}
}

func TestDoctorFixDryRunDoesNotMutate(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	report := RunDoctorFix(context.Background(), deps, false)
	if !report.DryRun || !doctorFixHasPlanned(report) {
		t.Fatalf("unexpected preview: %+v", report)
	}
	if _, err := os.Lstat(deps.Doctor.Paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry run changed channel root: %v", err)
	}
}

func TestDoctorFixRepairsOnlyOwnedDirectoryPermissions(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	if err := os.MkdirAll(deps.Doctor.Paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(deps.Doctor.Paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	report := RunDoctorFix(context.Background(), deps, true)
	info, err := os.Lstat(deps.Doctor.Paths.Root)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("owned root mode=%v err=%v", info.Mode().Perm(), err)
	}
	if report.Actions[0].Status != DoctorFixApplied {
		t.Fatalf("root action=%+v", report.Actions[0])
	}
}

func TestDoctorFixRefusesSymlinkAndDoesNotTouchTarget(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	if err := os.MkdirAll(deps.Doctor.Paths.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, deps.Doctor.Paths.Logs); err != nil {
		t.Fatal(err)
	}
	report := RunDoctorFix(context.Background(), deps, true)
	if report.Actions[2].Status != DoctorFixRefused {
		t.Fatalf("symlink was not refused: %+v", report.Actions)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("symlink target was mutated: mode=%v err=%v", info.Mode().Perm(), err)
	}
}

func TestDoctorFixRefusesForeignOwnership(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	if err := os.MkdirAll(deps.Doctor.Paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	deps.Doctor.CurrentUID = func() uint32 { return uint32(os.Geteuid()) + 1 }
	report := RunDoctorFix(context.Background(), deps, true)
	if report.Actions[0].Status != DoctorFixRefused {
		t.Fatalf("foreign-owned root was not refused: %+v", report.Actions[0])
	}
	info, _ := os.Lstat(deps.Doctor.Paths.Root)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("foreign-owned root mode changed to %o", info.Mode().Perm())
	}
}

func TestDoctorFixRefusesLstatChmodRace(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	if err := os.MkdirAll(deps.Doctor.Paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	realLstat := os.Lstat
	swapped := false
	deps.Lstat = func(path string) (os.FileInfo, error) {
		info, err := realLstat(path)
		if path == deps.Doctor.Paths.Root && err == nil && !swapped {
			swapped = true
			if removeErr := os.Remove(path); removeErr != nil {
				t.Fatal(removeErr)
			}
			if linkErr := os.Symlink(target, path); linkErr != nil {
				t.Fatal(linkErr)
			}
		}
		return info, err
	}
	report := RunDoctorFix(context.Background(), deps, true)
	if report.Actions[0].Status != DoctorFixRefused {
		t.Fatalf("raced directory was not refused: %+v", report.Actions[0])
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("race target was mutated to %o", info.Mode().Perm())
	}
}

func TestDoctorFixRevalidatesAfterPreview(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	if err := os.MkdirAll(deps.Doctor.Paths.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	if preview := RunDoctorFix(context.Background(), deps, false); !doctorFixHasPlanned(preview) {
		t.Fatalf("expected a planned mode repair: %+v", preview.Actions)
	}
	if err := os.Remove(deps.Doctor.Paths.Root); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Chmod(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, deps.Doctor.Paths.Root); err != nil {
		t.Fatal(err)
	}
	applied := RunDoctorFix(context.Background(), deps, true)
	if applied.Actions[0].Status != DoctorFixRefused {
		t.Fatalf("stale preview path was not refused: %+v", applied.Actions[0])
	}
	info, _ := os.Stat(target)
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("replacement target was mutated to %o", info.Mode().Perm())
	}
}

func TestDoctorFixRefusesWorldWritableParent(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	support := filepath.Join(deps.Home, "Library", "Application Support")
	if err := os.Chmod(support, 0o777); err != nil {
		t.Fatal(err)
	}
	report := RunDoctorFix(context.Background(), deps, true)
	if !doctorFixHasRefusal(report) {
		t.Fatalf("world-writable parent was not refused: %+v", report.Actions)
	}
	if _, err := os.Lstat(deps.Doctor.Paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe-parent repair created a channel root: %v", err)
	}
}

func TestDoctorFixReportIsStructuredAndPathFree(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	report := RunDoctorFix(context.Background(), deps, false)
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), deps.Doctor.Paths.Root) || report.Schema != doctorFixSchema {
		t.Fatalf("repair report leaked a path or schema changed: %s", encoded)
	}
}

func TestDoctorFixRejectsNonCanonicalLayout(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	deps.Doctor.Paths.Logs = filepath.Join(t.TempDir(), "escape")
	report := RunDoctorFix(context.Background(), deps, true)
	if len(report.Actions) != 1 || report.Actions[0].Status != DoctorFixRefused {
		t.Fatalf("noncanonical layout was not refused: %+v", report.Actions)
	}
}

func TestDoctorFixRejectsChannelSwappedLayout(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	deps.Doctor.Channel = domain.ChannelStable
	report := RunDoctorFix(context.Background(), deps, true)
	if len(report.Actions) != 1 || report.Actions[0].Status != DoctorFixRefused {
		t.Fatalf("channel-swapped layout was not refused: %+v", report.Actions)
	}
	if _, err := os.Lstat(deps.Doctor.Paths.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("channel-swapped repair mutated the dev root: %v", err)
	}
}

func TestDoctorFixRefusesSymlinkedParent(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	realParent := filepath.Join(deps.Home, "real-support")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	support := filepath.Join(deps.Home, "Library", "Application Support")
	if err := os.Remove(support); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realParent, support); err != nil {
		t.Fatal(err)
	}
	report := RunDoctorFix(context.Background(), deps, true)
	if !doctorFixHasRefusal(report) {
		t.Fatalf("symlinked parent was not refused: %+v", report.Actions)
	}
	if entries, err := os.ReadDir(realParent); err != nil || len(entries) != 0 {
		t.Fatalf("symlink target was mutated: entries=%v err=%v", entries, err)
	}
}

func TestDoctorFixDryRunFailureIsNotReportedHealthy(t *testing.T) {
	deps := isolatedDoctorFixDeps(t)
	report := RunDoctorFix(context.Background(), deps, false)
	response := doctorFixResponse(report, "sf-dev")
	if response.OK || response.Error == nil || response.Error.Code != "doctor_failed" || response.Mutation.Attempted {
		t.Fatalf("unresolved dry run reported healthy: %+v", response)
	}
}

func TestDoctorFixResponseDisclosesPartialSafeMutationBeforeRefusal(t *testing.T) {
	report := DoctorFixReport{
		Schema: doctorFixSchema, Channel: domain.ChannelDev, MutationAttempted: true,
		Actions: []DoctorFixAction{
			{ID: "channel_root", Status: DoctorFixApplied, Summary: "create owner-only channel directory"},
			{ID: "logs_directory", Status: DoctorFixRefused, Summary: "automatic repair refused because safety could not be proven"},
		},
	}
	response := doctorFixResponse(report, "sf-dev")
	if response.OK || response.Error == nil || !response.Mutation.Attempted ||
		!strings.Contains(response.Error.Message, "after applying one or more safe directory repairs") ||
		strings.Contains(response.Error.Message, "no changes were made") {
		t.Fatalf("partial mutation was not disclosed accurately: %+v", response)
	}
}
