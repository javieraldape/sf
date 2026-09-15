package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/nysa-company/sf/internal/api"
	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
)

const doctorFixSchema = "sf.doctor.fix/v1"

type DoctorFixStatus string

const (
	DoctorFixPlanned DoctorFixStatus = "planned"
	DoctorFixApplied DoctorFixStatus = "applied"
	DoctorFixNoop    DoctorFixStatus = "not_needed"
	DoctorFixRefused DoctorFixStatus = "refused"
)

// DoctorFixAction is intentionally path-free. Channel locations can contain
// account names or other private host information and are never report data.
type DoctorFixAction struct {
	ID      string          `json:"id"`
	Status  DoctorFixStatus `json:"status"`
	Summary string          `json:"summary"`
}

type DoctorFixReport struct {
	Schema            string            `json:"schema"`
	Channel           domain.Channel    `json:"channel"`
	DryRun            bool              `json:"dry_run"`
	MutationAttempted bool              `json:"mutation_attempted"`
	Actions           []DoctorFixAction `json:"actions"`
	Recheck           DoctorReport      `json:"recheck"`
}

// DoctorFixDeps keeps the mutating surface injectable and closed. Doctor is
// reused for both the preview and the post-repair recheck.
type DoctorFixDeps struct {
	Doctor DoctorDeps
	Home   string
	Lstat  func(string) (os.FileInfo, error)
}

func (deps DoctorFixDeps) defaults() DoctorFixDeps {
	deps.Doctor = deps.Doctor.defaults()
	if deps.Home == "" {
		deps.Home, _ = os.UserHomeDir()
	}
	if deps.Lstat == nil {
		deps.Lstat = os.Lstat
	}
	return deps
}

type doctorFixTarget struct {
	id   string
	path string
	rel  []string
}

func doctorFixTargets(home string, channel domain.Channel, paths config.ChannelPaths) ([]doctorFixTarget, error) {
	if !filepath.IsAbs(paths.Root) || filepath.Clean(paths.Root) != paths.Root {
		return nil, errors.New("channel root is not an absolute clean path")
	}
	canonical, err := config.PathsFor(home, channel)
	if err != nil || canonical != paths {
		return nil, errors.New("channel paths do not match the selected channel")
	}
	want := map[string]string{
		"run":       filepath.Join(paths.Root, "run"),
		"logs":      filepath.Join(paths.Root, "logs"),
		"events":    filepath.Join(paths.Root, "events"),
		"worktrees": filepath.Join(paths.Root, "worktrees"),
		"backups":   filepath.Join(paths.Root, "backups"),
	}
	if filepath.Dir(paths.Database) != paths.Root || filepath.Dir(paths.Machine) != paths.Root ||
		paths.Socket != filepath.Join(paths.Root, "run", "sf.sock") || paths.Logs != want["logs"] ||
		paths.Events != want["events"] || paths.Worktrees != want["worktrees"] || paths.Backups != want["backups"] {
		return nil, errors.New("channel paths do not match the fixed channel layout")
	}
	return []doctorFixTarget{
		{id: "channel_root", path: paths.Root, rel: []string{"Library", "Application Support", "sf", string(channel)}},
		{id: "run_directory", path: want["run"], rel: []string{"Library", "Application Support", "sf", string(channel), "run"}},
		{id: "logs_directory", path: want["logs"], rel: []string{"Library", "Application Support", "sf", string(channel), "logs"}},
		{id: "events_directory", path: want["events"], rel: []string{"Library", "Application Support", "sf", string(channel), "events"}},
		{id: "worktrees_directory", path: want["worktrees"], rel: []string{"Library", "Application Support", "sf", string(channel), "worktrees"}},
		{id: "backups_directory", path: want["backups"], rel: []string{"Library", "Application Support", "sf", string(channel), "backups"}},
	}, nil
}

// RunDoctorFix previews or applies the only automatic repairs supported by
// doctor: creation of fixed channel directories and mode 0700 on current-user
// real directories. It never touches files, repositories, databases, sockets,
// credentials, runtimes, providers, quarantine, or daemon lifecycle.
func RunDoctorFix(ctx context.Context, deps DoctorFixDeps, apply bool) DoctorFixReport {
	deps = deps.defaults()
	report := DoctorFixReport{Schema: doctorFixSchema, Channel: deps.Doctor.Channel, DryRun: !apply, Actions: []DoctorFixAction{}}
	targets, err := doctorFixTargets(deps.Home, deps.Doctor.Channel, deps.Doctor.Paths)
	if err != nil {
		report.Actions = append(report.Actions, refusedDoctorFix("channel_layout"))
		report.Recheck = RunDoctor(ctx, deps.Doctor)
		return report
	}

	for _, target := range targets {
		if ctx.Err() != nil {
			report.Actions = append(report.Actions, refusedDoctorFix(target.id))
			continue
		}
		info, inspectErr := deps.Lstat(target.path)
		if (inspectErr == nil && authenticateDoctorPath(deps.Home, target.rel, false) != nil) ||
			(errors.Is(inspectErr, os.ErrNotExist) && authenticateDoctorPath(deps.Home, target.rel, true) != nil) {
			report.Actions = append(report.Actions, refusedDoctorFix(target.id))
			continue
		}
		switch {
		case errors.Is(inspectErr, os.ErrNotExist):
			status := DoctorFixPlanned
			if apply {
				mutated, err := secureDoctorDirectory(deps.Home, target.rel, true)
				report.MutationAttempted = report.MutationAttempted || mutated
				if err != nil {
					report.Actions = append(report.Actions, refusedDoctorFix(target.id))
					continue
				}
				status = DoctorFixApplied
			}
			report.Actions = append(report.Actions, DoctorFixAction{ID: target.id, Status: status, Summary: "create owner-only channel directory"})
		case inspectErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir():
			report.Actions = append(report.Actions, refusedDoctorFix(target.id))
		case !doctorDirectoryOwned(info, deps.Doctor.CurrentUID()):
			report.Actions = append(report.Actions, refusedDoctorFix(target.id))
		case info.Mode().Perm() != 0o700:
			status := DoctorFixPlanned
			if apply {
				mutated, err := secureDoctorDirectory(deps.Home, target.rel, false)
				report.MutationAttempted = report.MutationAttempted || mutated
				if err != nil {
					report.Actions = append(report.Actions, refusedDoctorFix(target.id))
					continue
				}
				status = DoctorFixApplied
			}
			report.Actions = append(report.Actions, DoctorFixAction{ID: target.id, Status: status, Summary: "set owner-only channel directory permissions"})
		default:
			report.Actions = append(report.Actions, DoctorFixAction{ID: target.id, Status: DoctorFixNoop, Summary: "owner-only channel directory is already safe"})
		}
	}
	report.Recheck = RunDoctor(ctx, deps.Doctor)
	return report
}

func refusedDoctorFix(id string) DoctorFixAction {
	return DoctorFixAction{ID: id, Status: DoctorFixRefused, Summary: "automatic repair refused because safety could not be proven"}
}

func doctorDirectoryOwned(info os.FileInfo, uid uint32) bool {
	owner, ok := fileOwner(info)
	return ok && owner == uid
}

// secureDoctorDirectory anchors the entire walk at the authenticated home and
// uses no-follow descriptors for every component. Only the fixed `sf`, channel,
// and owned channel-child components (index 2 onward) may be created or chmodded.
func secureDoctorDirectory(home string, components []string, create bool) (bool, error) {
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer func() { _ = unix.Close(fd) }()
	var homeStat unix.Stat_t
	if err := unix.Fstat(fd, &homeStat); err != nil || homeStat.Mode&unix.S_IFMT != unix.S_IFDIR || uint32(homeStat.Uid) != uint32(os.Geteuid()) || homeStat.Mode&0o022 != 0 {
		return false, errors.New("home is not an owned real directory")
	}
	mutated := false
	for index, component := range components {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil && errors.Is(openErr, unix.ENOENT) && create && index >= 2 {
			if mkdirErr := unix.Mkdirat(fd, component, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, unix.EEXIST) {
				return mutated, mkdirErr
			}
			mutated = true
			next, openErr = unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		}
		if openErr != nil {
			return mutated, openErr
		}
		_ = unix.Close(fd)
		fd = next
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return mutated, err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 || (index >= 2 && uint32(stat.Uid) != uint32(os.Geteuid())) {
			return mutated, errors.New("channel path component is not an owned real directory")
		}
	}
	if err := unix.Fchmod(fd, 0o700); err != nil {
		return mutated, err
	}
	return true, nil
}

func authenticateDoctorPath(home string, components []string, allowMissing bool) error {
	fd, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	var homeStat unix.Stat_t
	if err := unix.Fstat(fd, &homeStat); err != nil || homeStat.Mode&unix.S_IFMT != unix.S_IFDIR || uint32(homeStat.Uid) != uint32(os.Geteuid()) || homeStat.Mode&0o022 != 0 {
		return errors.New("home is not an owned real directory")
	}
	for index, component := range components {
		next, openErr := unix.Openat(fd, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if errors.Is(openErr, unix.ENOENT) && allowMissing && index >= 2 {
			return nil
		}
		if openErr != nil {
			return openErr
		}
		_ = unix.Close(fd)
		fd = next
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			return err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o022 != 0 || (index >= 2 && uint32(stat.Uid) != uint32(os.Geteuid())) {
			return errors.New("channel path component is not an owned real directory")
		}
	}
	return nil
}

func doctorFixResponse(report DoctorFixReport, binary string) api.Response {
	data, err := json.Marshal(report)
	if err != nil {
		return failure("internal_error", "could not encode doctor repair report", []string{binary, "doctor", "--help"})
	}
	response := api.Response{Version: api.Version, RequestID: requestID(), OK: true, Data: data}
	var applied, refused int
	for _, action := range report.Actions {
		if action.Status == DoctorFixApplied {
			applied++
		}
		if action.Status == DoctorFixRefused {
			refused++
		}
	}
	response.Mutation = api.Mutation{Attempted: report.MutationAttempted || applied > 0, Kind: "secure_channel_directories", Observed: applied > 0}
	if refused > 0 {
		response.OK = false
		message := fmt.Sprintf("doctor refused %d automatic repair(s); no changes were made", refused)
		if response.Mutation.Attempted {
			message = fmt.Sprintf("doctor refused %d automatic repair(s) after applying one or more safe directory repairs; review the recheck before retrying", refused)
		}
		response.Error = &api.Error{Code: "safety_blocked", Message: message}
		response.NextAction = &domain.NextAction{Code: "doctor_fix_refused", Argv: []string{binary, "doctor"}}
	} else if !doctorReportPasses(report.Recheck) {
		response.OK = false
		message := "safe directory repairs completed; doctor still requires operator action"
		if report.DryRun {
			message = "dry-run preview complete; doctor still requires operator action and no mutations were run"
		}
		response.Error = &api.Error{Code: "doctor_failed", Message: message}
		response.NextAction = firstDoctorAction(report, binary)
	}
	return response
}

func doctorReportPasses(report DoctorReport) bool {
	for _, check := range report.Checks {
		if check.Status == CheckFail {
			return false
		}
	}
	return true
}

func firstDoctorAction(fix DoctorFixReport, binary string) *domain.NextAction {
	for _, check := range fix.Recheck.Checks {
		if check.Status == CheckFail && check.NextAction != nil {
			if check.ID == "channel_root" && doctorFixHasPlanned(fix) {
				return &domain.NextAction{Code: "doctor_fix", Argv: []string{binary, "doctor", "fix"}}
			}
			copy := *check.NextAction
			copy.Argv = append([]string(nil), check.NextAction.Argv...)
			return &copy
		}
	}
	return &domain.NextAction{Code: "doctor", Argv: []string{binary, "doctor"}}
}

func renderDoctorFix(writer io.Writer, report map[string]any) error {
	mode := "apply"
	if boolField(report, "dry_run") {
		mode = "dry-run"
	}
	if _, err := fmt.Fprintf(writer, "Doctor fix (%s, channel: %s)\n", mode, safeSelectionLabel(stringField(report, "channel"))); err != nil {
		return err
	}
	actions, _ := report["actions"].([]any)
	if _, err := io.WriteString(writer, "Repairs:\n"); err != nil {
		return err
	}
	if len(actions) == 0 {
		if _, err := io.WriteString(writer, "- none\n"); err != nil {
			return err
		}
	}
	for _, raw := range actions {
		action, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, err := fmt.Fprintf(writer, "- %s: %s — %s\n", safeSelectionLabel(stringField(action, "id")), safeSelectionLabel(stringField(action, "status")), safeSelectionLabel(stringField(action, "summary"))); err != nil {
			return err
		}
	}
	if recheck, ok := report["recheck"].(map[string]any); ok {
		if _, err := io.WriteString(writer, "Recheck:\n"); err != nil {
			return err
		}
		return renderDoctor(writer, recheck)
	}
	return nil
}

func doctorFixHasPlanned(report DoctorFixReport) bool {
	for _, action := range report.Actions {
		if action.Status == DoctorFixPlanned {
			return true
		}
	}
	return false
}

func doctorFixHasRefusal(report DoctorFixReport) bool {
	for _, action := range report.Actions {
		if action.Status == DoctorFixRefused {
			return true
		}
	}
	return false
}

func doctorFixConfirmation(input string) bool {
	return strings.TrimSpace(input) == "fix"
}
