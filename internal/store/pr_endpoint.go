package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nysa-company/sf/internal/domain"
)

const ExecutionEndpointPR = "pr"

var migrationV63 = []string{
	`CREATE TABLE ticket_execution_policies(channel TEXT NOT NULL,project_id TEXT NOT NULL,ticket_id TEXT NOT NULL,endpoint TEXT NOT NULL CHECK(endpoint='pr'),start_ticket_version INTEGER NOT NULL CHECK(start_ticket_version>1),created_at TEXT NOT NULL,PRIMARY KEY(channel,project_id,ticket_id),FOREIGN KEY(channel,project_id,ticket_id) REFERENCES tickets(channel,project_id,id))`,
	`CREATE TABLE ticket_endpoint_consumptions(channel TEXT NOT NULL,project_id TEXT NOT NULL,ticket_id TEXT NOT NULL,endpoint TEXT NOT NULL CHECK(endpoint='pr'),paused_ticket_version INTEGER NOT NULL CHECK(paused_ticket_version>1),consumed_ticket_version INTEGER NOT NULL CHECK(consumed_ticket_version>paused_ticket_version),leader_epoch INTEGER NOT NULL CHECK(leader_epoch>0),runner_epoch INTEGER NOT NULL CHECK(runner_epoch>0),publication_witness_digest TEXT NOT NULL CHECK(length(publication_witness_digest)=71),created_at TEXT NOT NULL,PRIMARY KEY(channel,project_id,ticket_id,endpoint),FOREIGN KEY(channel,project_id,ticket_id) REFERENCES tickets(channel,project_id,id))`,
	`CREATE TRIGGER ticket_execution_policies_immutable_update BEFORE UPDATE ON ticket_execution_policies BEGIN SELECT RAISE(ABORT,'ticket execution policy is immutable'); END`,
	`CREATE TRIGGER ticket_execution_policies_immutable_delete BEFORE DELETE ON ticket_execution_policies BEGIN SELECT RAISE(ABORT,'ticket execution policy is append-only'); END`,
	`CREATE TRIGGER ticket_endpoint_consumptions_immutable_update BEFORE UPDATE ON ticket_endpoint_consumptions BEGIN SELECT RAISE(ABORT,'ticket endpoint consumption is immutable'); END`,
	`CREATE TRIGGER ticket_endpoint_consumptions_immutable_delete BEFORE DELETE ON ticket_endpoint_consumptions BEGIN SELECT RAISE(ABORT,'ticket endpoint consumption is append-only'); END`,
}

func hasUnconsumedPREndpoint(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref domain.TicketRef) (bool, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_execution_policies p LEFT JOIN ticket_endpoint_consumptions c ON c.channel=p.channel AND c.project_id=p.project_id AND c.ticket_id=p.ticket_id AND c.endpoint=p.endpoint WHERE p.channel=? AND p.project_id=? AND p.ticket_id=? AND p.endpoint='pr' AND c.ticket_id IS NULL`, ref.Channel, ref.Project, ref.Ticket).Scan(&count)
	return count == 1, err
}

func (s *Store) IsPREndpointPaused(ctx context.Context, ticket Ticket) (bool, error) {
	if ticket.State != domain.StatePaused || ticket.ResumeState != domain.StateWaitingCI || ticket.BlockedCode != "pr_opened" || ticket.Version < 2 {
		return false, nil
	}
	_, _, err := authenticatePREndpoint(ctx, s.db, ticket.Ref, ticket.Version)
	return err == nil, err
}

func authenticatePREndpoint(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref domain.TicketRef, currentVersion uint64) (uint64, string, error) {
	var endpoint string
	var startVersion, pauseVersion uint64
	var created, payload, witness string
	if err := q.QueryRowContext(ctx, `SELECT endpoint,start_ticket_version,created_at FROM ticket_execution_policies WHERE channel=? AND project_id=? AND ticket_id=?`, ref.Channel, ref.Project, ref.Ticket).Scan(&endpoint, &startVersion, &created); err != nil || endpoint != ExecutionEndpointPR || startVersion < 2 || created == "" {
		return 0, "", ErrPublicationEvidence
	}
	var starts int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE channel=? AND project_id=? AND ticket_id=? AND ticket_version=? AND trigger='operator_start' AND from_state='queued' AND to_state='planning'`, ref.Channel, ref.Project, ref.Ticket, startVersion).Scan(&starts); err != nil || starts != 1 {
		return 0, "", ErrPublicationEvidence
	}
	var endpointEvents int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE channel=? AND project_id=? AND ticket_id=? AND trigger='endpoint_reached'`, ref.Channel, ref.Project, ref.Ticket).Scan(&endpointEvents); err != nil || endpointEvents != 1 {
		return 0, "", ErrPublicationEvidence
	}
	if err := q.QueryRowContext(ctx, `SELECT e.ticket_version,e.payload,p.witness_digest FROM events e JOIN publication_transition_evidence p ON p.channel=e.channel AND p.project_id=e.project_id AND p.ticket_id=e.ticket_id AND p.ticket_version=e.ticket_version-1 WHERE e.channel=? AND e.project_id=? AND e.ticket_id=? AND e.trigger='endpoint_reached' AND e.from_state='waiting_ci' AND e.to_state='paused'`, ref.Channel, ref.Project, ref.Ticket).Scan(&pauseVersion, &payload, &witness); err != nil || pauseVersion > currentVersion {
		return 0, "", ErrPublicationEvidence
	}
	if currentVersion != pauseVersion && currentVersion != pauseVersion+1 {
		return 0, "", ErrPublicationEvidence
	}
	publication, found, err := loadPublicationEvidenceRow(ctx, q, ref)
	if err != nil || !found || witness != publication.WitnessDigest {
		return 0, "", ErrPublicationEvidence
	}
	if err := loadLatestPublicationRebind(ctx, q, &publication); err != nil {
		return 0, "", ErrPublicationEvidence
	}
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", publication.PullRequest.Repository.Owner, publication.PullRequest.Repository.Name, publication.PullRequest.Number)
	expected, _ := json.Marshal(map[string]string{"endpoint": "pr", "reason": "pr_opened", "pr_url": prURL, "head": publication.Candidate.Snapshot.HeadSHA, "witness_digest": witness})
	if payload != string(expected) {
		return 0, "", ErrPublicationEvidence
	}
	if currentVersion == pauseVersion {
		var state domain.State
		var runner uint64
		if err := q.QueryRowContext(ctx, `SELECT state,runner_epoch FROM tickets WHERE channel=? AND project_id=? AND id=? AND version=?`, ref.Channel, ref.Project, ref.Ticket, pauseVersion).Scan(&state, &runner); err != nil || state != domain.StatePaused || runner != publication.CurrentFence.RunnerEpoch {
			return 0, "", ErrPublicationEvidence
		}
	}
	return pauseVersion, witness, nil
}

func (s *Store) PREndpointConsumed(ctx context.Context, ref domain.TicketRef) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_endpoint_consumptions WHERE channel=? AND project_id=? AND ticket_id=? AND endpoint='pr'`, ref.Channel, ref.Project, ref.Ticket).Scan(&count)
	return count == 1, err
}

func authenticatePREndpointResume(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref domain.TicketRef, resumedVersion uint64) error {
	if resumedVersion < 2 {
		return ErrPublicationEvidence
	}
	pauseVersion, witness, err := authenticatePREndpoint(ctx, q, ref, resumedVersion)
	if err != nil || resumedVersion != pauseVersion+1 {
		return ErrPublicationEvidence
	}
	expectedPayload, _ := json.Marshal(map[string]string{"endpoint": "pr", "witness_digest": witness})
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_endpoint_consumptions c JOIN events e ON e.channel=c.channel AND e.project_id=c.project_id AND e.ticket_id=c.ticket_id AND e.ticket_version=c.consumed_ticket_version WHERE c.channel=? AND c.project_id=? AND c.ticket_id=? AND c.endpoint='pr' AND c.paused_ticket_version=? AND c.consumed_ticket_version=? AND c.publication_witness_digest=? AND c.leader_epoch>0 AND c.runner_epoch>0 AND e.trigger='operator_resume' AND e.from_state='paused' AND e.to_state='waiting_ci' AND e.payload=?`, ref.Channel, ref.Project, ref.Ticket, pauseVersion, resumedVersion, witness, string(expectedPayload)).Scan(&count); err != nil || count != 1 {
		return ErrPublicationEvidence
	}
	return nil
}

func prEndpointResumeFence(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ref domain.TicketRef, version uint64) (domain.Fence, error) {
	var fence domain.Fence
	if err := q.QueryRowContext(ctx, `SELECT leader_epoch,runner_epoch FROM ticket_endpoint_consumptions WHERE channel=? AND project_id=? AND ticket_id=? AND endpoint='pr' AND consumed_ticket_version=?`, ref.Channel, ref.Project, ref.Ticket, version).Scan(&fence.LeaderEpoch, &fence.RunnerEpoch); err != nil {
		return domain.Fence{}, ErrPublicationEvidence
	}
	return fence, nil
}

// ResumePREndpoint consumes the one-shot first-PR handoff and resumes CI in
// the same transaction. Exact replay after a lost response returns observed.
func (s *Store) ResumePREndpoint(ctx context.Context, ref domain.TicketRef, expected uint64, fence domain.Fence) (Ticket, bool, error) {
	var observed bool
	err := s.write(ctx, func(conn *sql.Conn) error {
		var state, resume domain.State
		var blocked, witness string
		var version, runner, pauseVersion uint64
		if err := conn.QueryRowContext(ctx, `SELECT state,COALESCE(resume_state,''),blocked_code,version,runner_epoch FROM tickets WHERE channel=? AND project_id=? AND id=?`, ref.Channel, ref.Project, ref.Ticket).Scan(&state, &resume, &blocked, &version, &runner); err != nil {
			return err
		}
		if state == domain.StateWaitingCI && version == expected+1 {
			if runner != fence.RunnerEpoch {
				return ErrStaleFence
			}
			var leader uint64
			if err := conn.QueryRowContext(ctx, `SELECT leader_epoch FROM daemon_instances WHERE channel=?`, ref.Channel).Scan(&leader); err != nil || leader != fence.LeaderEpoch {
				return ErrStaleFence
			}
			var count int
			if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM ticket_endpoint_consumptions WHERE channel=? AND project_id=? AND ticket_id=? AND endpoint='pr' AND paused_ticket_version=? AND consumed_ticket_version=?`, ref.Channel, ref.Project, ref.Ticket, expected, version).Scan(&count); err != nil || count != 1 {
				return ErrPublicationEvidence
			}
			observed = true
			return nil
		}
		if state != domain.StatePaused || resume != domain.StateWaitingCI || blocked != "pr_opened" || version != expected || runner != fence.RunnerEpoch {
			return ErrStaleFence
		}
		var leader uint64
		if err := conn.QueryRowContext(ctx, `SELECT leader_epoch FROM daemon_instances WHERE channel=?`, ref.Channel).Scan(&leader); err != nil || leader != fence.LeaderEpoch {
			return ErrStaleFence
		}
		if ok, err := hasUnconsumedPREndpoint(ctx, conn, ref); err != nil || !ok {
			return ErrPublicationEvidence
		}
		var err error
		pauseVersion, witness, err = authenticatePREndpoint(ctx, conn, ref, version)
		if err != nil {
			return err
		}
		if err := reacquireTicketCapacity(ctx, conn, ref, runner); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"endpoint": "pr", "witness_digest": witness})
		created := time.Now().UTC().Format(time.RFC3339Nano)
		updated, err := conn.ExecContext(ctx, `UPDATE tickets SET state='waiting_ci',resume_state=NULL,blocked_code='',version=version+1 WHERE channel=? AND project_id=? AND id=? AND state='paused' AND version=? AND runner_epoch=?`, ref.Channel, ref.Project, ref.Ticket, version, runner)
		if err != nil {
			return err
		}
		if changed, _ := updated.RowsAffected(); changed != 1 {
			return ErrStaleFence
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO events(channel,project_id,ticket_id,ticket_version,trigger,from_state,to_state,payload,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, ref.Channel, ref.Project, ref.Ticket, version+1, "operator_resume", domain.StatePaused, domain.StateWaitingCI, string(payload), created); err != nil {
			return err
		}
		_, err = conn.ExecContext(ctx, `INSERT INTO ticket_endpoint_consumptions(channel,project_id,ticket_id,endpoint,paused_ticket_version,consumed_ticket_version,leader_epoch,runner_epoch,publication_witness_digest,created_at) VALUES(?,?,?,'pr',?,?,?,?,?,?)`, ref.Channel, ref.Project, ref.Ticket, pauseVersion, version+1, fence.LeaderEpoch, fence.RunnerEpoch, witness, created)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ticket{}, false, ErrNotFound
		}
		return Ticket{}, false, err
	}
	ticket, err := s.Ticket(ctx, ref)
	return ticket, observed, err
}
