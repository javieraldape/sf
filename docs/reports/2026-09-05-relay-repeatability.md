# Serial repeatability and bounded recovery summary

This anonymized summary retains historical technical acceptance results and
limits while omitting private repository identities and operational records.
No new validation is claimed by this documentation update.

## Outcome

Two additional tickets reached durable `done` serially on one unchanged factory
binary, with required and post-merge CI passing. Capacity was one, with guarded
mode and independently qualified Builder and Reviewer models. Each delivery
used one provider attempt per phase and changed only its declared tool and test.
The second ticket obtained the first delivery's hosted merge as its base through
authenticated worktree creation.

Between the two executions there were no source changes, daemon replacements,
provider retries, manual worktree edits, database restorations or resubmissions.
Interventions were submission/start, inspection and one exact-candidate guarded
approval per delivery. Approvals were performed under delegated authority;
they were not new personal head-specific reviews. SF performed the merges with
no direct GitHub merge or protection bypass. Durable token/billing breakdowns
were unavailable; zero recorded usage units did not establish zero cost.

## Failed trials retained

- A stale local base after a hosted merge caused publication to refuse. The
  failed trial retained its candidate and worktree. The repair pins fresh hosted
  bases under the creation lease; a real temporary-remote regression covers
  fresh creation and unchanged existing-ticket identity after remote drift.
- Blocked-provider restart recovery failed historical leader validation. The
  repair atomically records authenticated prior/current leaders while retaining
  the attempt window; tests cover repeated restart and missing proof.
- A planner question produced a semantic pause with no supported answer path.
  Ordinary resume would reuse that immutable result. This remained a usability
  limitation rather than an inferred answer or an edited plan.
- Repeated pause incorrectly reported success without draining retained
  capacity. The repair requires runtime join, sealed Store drain proof and
  exact-fence release; supported pause freed capacity without rewriting
  the paused trial or resubmitting the queued ticket.

## Recorded verification

Full fresh Go suites, focused daemon and Store/Git/worktree race tests, vet,
repository checks, secret scan and diff check passed at repair checkpoints.
An uncached anchored run on the unchanged delivery source passed:

- `TestProviderBlockedRecoveryAfterLeaderReplacement`
- `TestWorkerReconcilesLostCreateWithoutBlindReplay`
- `TestGuardedApprovalMergeRecoversLostProofResponse`
- `TestVerifyProtectedBranchReusesConfirmedProofAfterLostResponse`
- `TestDaemonProviderRetryRetainsProviderWrittenInvalidArtifactWorktree`
- `TestTicketBudgetExhaustionIsStoreAuthenticatedAndNonRecoverable`
- `TestWaitingCIWorkerBlocksWhenObserverIsUnavailable`
- `TestPauseDrainsQuestionPausedTicketBeforeReleasingCapacity`
- `TestPauseQuestionCapacityRefusesOutstandingEffect`

Final read-only checks found no capacity, repository-command or Git mutation
leases and no active/quarantined providers. The fault cases are deterministic
fixtures, not live GitHub/provider chaos or hostile-process containment evidence.
This establishes bounded serial repeatability, not stable-v1, autonomous,
concurrent or load-test reliability. A durable planner-answer workflow and a
question-bearing end-to-end trial remained follow-up work at this checkpoint.
