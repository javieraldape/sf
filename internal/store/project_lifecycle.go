package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nysa-company/sf/internal/domain"
)

type ProjectLifecycle string

const (
	ProjectActive  ProjectLifecycle = "active"
	ProjectRemoved ProjectLifecycle = "removed"
)

type ProjectRemovalPreview struct {
	Project                  Project
	UnfinishedTickets        int
	ActiveWorkflowOwners     int
	ActiveLeases             int
	ActiveProviderAttempts   int
	ActivePhaseRuns          int
	ActiveRepositoryCommands int
	ActiveGitMutations       int
	UnresolvedEffects        int
	ActiveAuthoringTurns     int
	GlobalQuarantine         bool
	CanRemove                bool
}

func (s *Store) ProjectRemovalPreview(ctx context.Context, channel domain.Channel, id domain.ProjectID) (ProjectRemovalPreview, error) {
	if !channel.Valid() || id == "" {
		return ProjectRemovalPreview{}, errors.New("valid project channel and id are required")
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return ProjectRemovalPreview{}, normalizeBusy(ctx, err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return ProjectRemovalPreview{}, normalizeBusy(ctx, err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	preview, err := projectRemovalPreview(ctx, conn, channel, id)
	return preview, normalizeBusy(ctx, err)
}

// RemoveProject marks one registration removed without deleting any row or
// filesystem object. expectedRegistrationGeneration binds an earlier preview;
// zero is rejected so a caller cannot accidentally skip the comparison.
func (s *Store) RemoveProject(ctx context.Context, channel domain.Channel, id domain.ProjectID, expectedRegistrationGeneration uint64, at time.Time) (ProjectRemovalPreview, bool, error) {
	if !channel.Valid() || id == "" || expectedRegistrationGeneration == 0 || at.IsZero() {
		return ProjectRemovalPreview{}, false, errors.New("valid project, preview generation, and removal time are required")
	}
	var result ProjectRemovalPreview
	removed := false
	err := s.write(ctx, func(conn *sql.Conn) error {
		preview, err := projectRemovalPreview(ctx, conn, channel, id)
		if err != nil {
			return err
		}
		result = preview
		if preview.Project.RegistrationGeneration != expectedRegistrationGeneration {
			return ErrProjectLifecycleChanged
		}
		if preview.Project.Lifecycle == ProjectRemoved {
			return nil
		}
		if !preview.CanRemove {
			return ErrProjectRemovalBlocked
		}
		if preview.Project.RegistrationGeneration == ^uint64(0) {
			return ErrProjectLifecycleChanged
		}
		next := preview.Project.RegistrationGeneration + 1
		updated, err := conn.ExecContext(ctx, `UPDATE projects SET lifecycle='removed',registration_generation=?,removed_at=? WHERE channel=? AND id=? AND lifecycle='active' AND registration_generation=?`, next, at.UTC().Format(time.RFC3339Nano), channel, id, expectedRegistrationGeneration)
		if err != nil {
			return err
		}
		if changed, _ := updated.RowsAffected(); changed != 1 {
			return ErrProjectLifecycleChanged
		}
		removed = true
		result.Project.Lifecycle = ProjectRemoved
		result.Project.RegistrationGeneration = next
		result.Project.RemovedAt = at.UTC()
		result.CanRemove = false
		return nil
	})
	return result, removed, err
}

func projectRemovalPreview(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, channel domain.Channel, id domain.ProjectID) (ProjectRemovalPreview, error) {
	p := Project{Channel: channel, ID: id}
	var lifecycle, removedAt string
	err := q.QueryRowContext(ctx, `SELECT p.canonical_path,p.base_ref,p.current_config_generation,COALESCE(c.digest,''),COALESCE(c.snapshot_bytes,X''),p.lifecycle,p.registration_generation,p.removed_at FROM projects p LEFT JOIN project_configurations c ON c.channel=p.channel AND c.project_id=p.id AND c.generation=p.current_config_generation WHERE p.channel=? AND p.id=?`, channel, id).Scan(&p.Path, &p.BaseRef, &p.ConfigGeneration, &p.ConfigDigest, &p.ConfigSnapshot, &lifecycle, &p.RegistrationGeneration, &removedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectRemovalPreview{}, ErrNotFound
	}
	if err != nil {
		return ProjectRemovalPreview{}, err
	}
	p.Lifecycle = ProjectLifecycle(lifecycle)
	if removedAt != "" {
		p.RemovedAt, err = time.Parse(time.RFC3339Nano, removedAt)
		if err != nil {
			return ProjectRemovalPreview{}, fmt.Errorf("invalid project removal timestamp: %w", err)
		}
	}
	r := ProjectRemovalPreview{Project: p}
	queries := []struct {
		dst *int
		sql string
	}{
		{&r.UnfinishedTickets, `SELECT COUNT(*) FROM tickets WHERE channel=? AND project_id=? AND state NOT IN ('done','external_merged','cancelled')`},
		{&r.ActiveWorkflowOwners, `SELECT COUNT(*) FROM workflow_owners w LEFT JOIN tickets t ON t.channel=w.channel AND t.project_id=w.project_id AND t.id=w.ticket_id WHERE w.channel=? AND w.project_id=? AND (t.id IS NULL OR t.state NOT IN ('done','external_merged','cancelled'))`},
		{&r.ActiveLeases, `SELECT COUNT(*) FROM leases WHERE channel=? AND project_id=?`},
		{&r.ActiveProviderAttempts, `SELECT COUNT(*) FROM provider_attempts WHERE channel=? AND project_id=? AND (state IN ('active','quarantined') OR launch_state IN ('launching','released','quarantined'))`},
		{&r.ActivePhaseRuns, `SELECT COUNT(*) FROM phase_runs WHERE channel=? AND project_id=? AND state='active'`},
		{&r.ActiveRepositoryCommands, `SELECT COUNT(*) FROM repository_command_leases WHERE channel=? AND project_id=? AND state IN ('active','quarantined')`},
		{&r.ActiveGitMutations, `SELECT COUNT(*) FROM git_mutation_leases WHERE channel=? AND project_id=? AND state IN ('active','quarantined')`},
		{&r.UnresolvedEffects, `SELECT COUNT(*) FROM effects WHERE channel=? AND project_id=? AND state IN ('planned','executing','uncertain')`},
		{&r.ActiveAuthoringTurns, `SELECT COUNT(*) FROM authoring_turns t JOIN authoring_sessions s ON s.channel=t.channel AND s.id=t.session_id WHERE s.channel=? AND s.project_id=? AND t.state IN ('reserved','launched','uncertain')`},
	}
	for _, item := range queries {
		if err := q.QueryRowContext(ctx, item.sql, channel, id).Scan(item.dst); err != nil {
			return ProjectRemovalPreview{}, err
		}
	}
	var quarantine int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM external_mutation_quarantine`).Scan(&quarantine); err != nil {
		return ProjectRemovalPreview{}, err
	}
	r.GlobalQuarantine = quarantine != 0
	r.CanRemove = p.Lifecycle == ProjectActive && !r.GlobalQuarantine && r.UnfinishedTickets+r.ActiveWorkflowOwners+r.ActiveLeases+r.ActiveProviderAttempts+r.ActivePhaseRuns+r.ActiveRepositoryCommands+r.ActiveGitMutations+r.UnresolvedEffects+r.ActiveAuthoringTurns == 0
	return r, nil
}
