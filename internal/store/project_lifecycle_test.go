package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/nysa-company/sf/internal/config"
	"github.com/nysa-company/sf/internal/domain"
)

func TestProjectRemovalRetainsHistoryAndReactivation(t *testing.T) {
	database, ctx := openTestStore(t)
	before, err := database.ProjectRemovalPreview(ctx, domain.ChannelDev, "nysa")
	if err != nil || !before.CanRemove || before.Project.RegistrationGeneration != 1 {
		t.Fatalf("preview=%+v err=%v", before, err)
	}
	removed, changed, err := database.RemoveProject(ctx, domain.ChannelDev, "nysa", before.Project.RegistrationGeneration, time.Now())
	if err != nil || !changed || removed.Project.Lifecycle != ProjectRemoved || removed.Project.RegistrationGeneration != 2 {
		t.Fatalf("removed=%+v changed=%v err=%v", removed, changed, err)
	}
	replay, changed, err := database.RemoveProject(ctx, domain.ChannelDev, "nysa", removed.Project.RegistrationGeneration, time.Now())
	if err != nil || changed || replay.Project.Lifecycle != ProjectRemoved {
		t.Fatalf("replay=%+v changed=%v err=%v", replay, changed, err)
	}
	project := removed.Project
	project.ConfigGeneration = 1 // init supplies an initial proposal, not a history pointer.
	project.Lifecycle, project.RegistrationGeneration, project.RemovedAt = "", 0, time.Time{}
	created, err := database.RegisterProject(ctx, project)
	if err != nil || !created {
		t.Fatalf("reactivate created=%v err=%v", created, err)
	}
	after, err := database.Project(ctx, domain.ChannelDev, "nysa")
	if err != nil || after.Lifecycle != ProjectActive || after.RegistrationGeneration != 3 || after.ConfigGeneration != 1 {
		t.Fatalf("after=%+v err=%v", after, err)
	}
}

func TestRemovedProjectReinitAppendsChangedConfiguration(t *testing.T) {
	database, ctx := openTestStore(t)
	preview, _ := database.ProjectRemovalPreview(ctx, domain.ChannelDev, "nysa")
	if _, _, err := database.RemoveProject(ctx, domain.ChannelDev, "nysa", preview.Project.RegistrationGeneration, time.Now()); err != nil {
		t.Fatal(err)
	}
	effective, err := config.Resolve(config.DefaultMachineLimits(), config.DefaultProject("nysa", "/tmp/nysa"), config.TicketOverride{MaxCostMicroUSD: 1})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, digest, err := config.Snapshot(effective)
	if err != nil {
		t.Fatal(err)
	}
	if created, err := database.RegisterProject(ctx, Project{Channel: domain.ChannelDev, ID: "nysa", Path: "/tmp/nysa", BaseRef: "main", ConfigGeneration: 1, ConfigDigest: digest, ConfigSnapshot: snapshot}); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	project, err := database.Project(ctx, domain.ChannelDev, "nysa")
	if err != nil || project.ConfigGeneration != 2 || project.RegistrationGeneration != 3 {
		t.Fatalf("project=%+v err=%v", project, err)
	}
	var generations int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_configurations WHERE channel=? AND project_id=?`, domain.ChannelDev, "nysa").Scan(&generations); err != nil || generations != 2 {
		t.Fatalf("generations=%d err=%v", generations, err)
	}
}

func TestProjectRemovalPreviewCountsUnresolvedEffectAndLease(t *testing.T) {
	database, ctx := openTestStore(t)
	ref := domain.TicketRef{Channel: domain.ChannelDev, Project: "nysa", Ticket: "SF-removal-authority"}
	if err := database.CreateTicket(ctx, ticket(ref, "removal-authority")); err != nil {
		t.Fatal(err)
	}
	leader, err := database.AcquireLeader(ctx, domain.ChannelDev, "removal-authority")
	if err != nil {
		t.Fatal(err)
	}
	current, _ := database.Ticket(ctx, ref)
	fence := domain.Fence{LeaderEpoch: leader, RunnerEpoch: current.RunnerEpoch}
	if _, err := database.PlanEffect(ctx, EffectPlan{SemanticKey: "project-removal/effect", Ref: ref, Kind: "fixture", TicketVersion: current.Version, Fence: fence, RequestDigest: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if _, err := database.AcquireLeases(ctx, ref, current.Version, fence, []LeaseRequest{{Scope: "project", Resource: "nysa", Capacity: 1}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tickets SET state='cancelled' WHERE channel=? AND project_id=? AND id=?`, ref.Channel, ref.Project, ref.Ticket); err != nil {
		t.Fatal(err)
	}
	preview, err := database.ProjectRemovalPreview(ctx, ref.Channel, ref.Project)
	if err != nil || preview.UnfinishedTickets != 0 || preview.UnresolvedEffects != 1 || preview.ActiveLeases != 1 || preview.CanRemove {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestProjectRemovalRefusesGlobalQuarantine(t *testing.T) {
	database, ctx := openTestStore(t)
	if _, err := database.db.ExecContext(ctx, `INSERT INTO external_mutation_quarantine(singleton,reason,observed_at) VALUES(1,'fixture',?)`, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	preview, err := database.ProjectRemovalPreview(ctx, domain.ChannelDev, "nysa")
	if err != nil || !preview.GlobalQuarantine || preview.CanRemove {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if _, _, err := database.RemoveProject(ctx, domain.ChannelDev, "nysa", preview.Project.RegistrationGeneration, time.Now()); !errors.Is(err, ErrProjectRemovalBlocked) {
		t.Fatalf("remove error=%v", err)
	}
}

func TestProjectRemovalTreatsTerminalTicketOwnerAsHistory(t *testing.T) {
	database, ctx := openTestStore(t)
	ref := domain.TicketRef{Channel: domain.ChannelDev, Project: "nysa", Ticket: "SF-terminal-owner"}
	if err := database.CreateTicket(ctx, ticket(ref, "terminal-owner")); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO workflow_owners(channel,project_id,ticket_id,workflow_id,state,created_at) VALUES(?,?,?,?,?,?)`, ref.Channel, ref.Project, ref.Ticket, "historical-owner", "owned", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tickets SET state='done' WHERE channel=? AND project_id=? AND id=?`, ref.Channel, ref.Project, ref.Ticket); err != nil {
		t.Fatal(err)
	}
	preview, err := database.ProjectRemovalPreview(ctx, ref.Channel, ref.Project)
	if err != nil || preview.UnfinishedTickets != 0 || preview.ActiveWorkflowOwners != 0 || !preview.CanRemove {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
}

func TestProjectRemovalBlocksUnfinishedTicketAndRemovedAdmission(t *testing.T) {
	database, ctx := openTestStore(t)
	ref := domain.TicketRef{Channel: domain.ChannelDev, Project: "nysa", Ticket: "SF-removal-block"}
	if err := database.CreateTicket(ctx, ticket(ref, "removal-block")); err != nil {
		t.Fatal(err)
	}
	preview, err := database.ProjectRemovalPreview(ctx, ref.Channel, ref.Project)
	if err != nil || preview.CanRemove || preview.UnfinishedTickets != 1 {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if _, _, err := database.RemoveProject(ctx, ref.Channel, ref.Project, preview.Project.RegistrationGeneration, time.Now()); !errors.Is(err, ErrProjectRemovalBlocked) {
		t.Fatalf("remove error=%v", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE tickets SET state='cancelled' WHERE channel=? AND project_id=? AND id=?`, ref.Channel, ref.Project, ref.Ticket); err != nil {
		t.Fatal(err)
	}
	preview, _ = database.ProjectRemovalPreview(ctx, ref.Channel, ref.Project)
	removed, _, err := database.RemoveProject(ctx, ref.Channel, ref.Project, preview.Project.RegistrationGeneration, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("# removed")
	sum := sha256.Sum256(source)
	input := Ticket{Ref: domain.TicketRef{Channel: ref.Channel, Project: ref.Project, Ticket: "SF-after-remove"}, Source: source, SourceDigest: fmt.Sprintf("%x", sum[:]), Type: domain.TicketBug, MergeMode: domain.MergeGuarded}
	if _, _, err := database.SubmitTicket(ctx, input, false); !errors.Is(err, ErrProjectRemoved) {
		t.Fatalf("submit error=%v removed=%+v", err, removed)
	}
	terminal, err := database.Ticket(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.StartWithProjectOwnership(ctx, ref, terminal.Version, domain.Fence{LeaderEpoch: 1, RunnerEpoch: terminal.RunnerEpoch}, "removed", time.Now()); !errors.Is(err, ErrStartState) && !errors.Is(err, ErrProjectRemoved) {
		t.Fatalf("start error=%v", err)
	}
}

func TestProjectRemovalPreviewGenerationRejectsConfigurationRace(t *testing.T) {
	database, ctx := openTestStore(t)
	preview, err := database.ProjectRemovalPreview(ctx, domain.ChannelDev, "nysa")
	if err != nil {
		t.Fatal(err)
	}
	// A registration lifecycle change after preview must prevent removal.
	if _, err := database.db.ExecContext(ctx, `UPDATE projects SET registration_generation=registration_generation+1 WHERE channel=? AND id=?`, domain.ChannelDev, "nysa"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.RemoveProject(ctx, domain.ChannelDev, "nysa", preview.Project.RegistrationGeneration, time.Now()); !errors.Is(err, ErrProjectLifecycleChanged) {
		t.Fatalf("remove error=%v", err)
	}
}

func TestProjectRemovalSerializesWithSubmit(t *testing.T) {
	database, ctx := openTestStore(t)
	preview, err := database.ProjectRemovalPreview(ctx, domain.ChannelDev, "nysa")
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("# concurrent submission")
	sum := sha256.Sum256(source)
	input := Ticket{Ref: domain.TicketRef{Channel: domain.ChannelDev, Project: "nysa", Ticket: "SF-concurrent-remove"}, Source: source, SourceDigest: fmt.Sprintf("%x", sum[:]), Type: domain.TicketBug, MergeMode: domain.MergeGuarded}
	start := make(chan struct{})
	removeResult := make(chan error, 1)
	submitResult := make(chan error, 1)
	go func() {
		<-start
		_, _, err := database.RemoveProject(context.Background(), domain.ChannelDev, "nysa", preview.Project.RegistrationGeneration, time.Now())
		removeResult <- err
	}()
	go func() {
		<-start
		_, _, err := database.SubmitTicket(context.Background(), input, false)
		submitResult <- err
	}()
	close(start)
	removeErr, submitErr := <-removeResult, <-submitResult
	removeWon := removeErr == nil && errors.Is(submitErr, ErrProjectRemoved)
	submitWon := submitErr == nil && errors.Is(removeErr, ErrProjectRemovalBlocked)
	if !removeWon && !submitWon {
		t.Fatalf("non-serial outcome remove=%v submit=%v", removeErr, submitErr)
	}
}

func TestProjectRemovalCannotRaceQueuedTicketStart(t *testing.T) {
	database, ctx := openTestStore(t)
	ref := domain.TicketRef{Channel: domain.ChannelDev, Project: "nysa", Ticket: "SF-concurrent-start"}
	if err := database.CreateTicket(ctx, ticket(ref, "concurrent-start")); err != nil {
		t.Fatal(err)
	}
	queued, err := database.Ticket(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	leader, err := database.AcquireLeader(ctx, domain.ChannelDev, "concurrent-start")
	if err != nil {
		t.Fatal(err)
	}
	preview, err := database.ProjectRemovalPreview(ctx, ref.Channel, ref.Project)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	removeResult := make(chan error, 1)
	startResult := make(chan error, 1)
	go func() {
		<-start
		_, _, err := database.RemoveProject(context.Background(), ref.Channel, ref.Project, preview.Project.RegistrationGeneration, time.Now())
		removeResult <- err
	}()
	go func() {
		<-start
		_, _, err := database.StartWithProjectOwnership(context.Background(), ref, queued.Version, domain.Fence{LeaderEpoch: leader, RunnerEpoch: queued.RunnerEpoch}, "concurrent-start", time.Now())
		startResult <- err
	}()
	close(start)
	if removeErr, startErr := <-removeResult, <-startResult; !errors.Is(removeErr, ErrProjectRemovalBlocked) || startErr != nil {
		t.Fatalf("remove=%v start=%v", removeErr, startErr)
	}
}
