# Guarded concurrency acceptance summary

This anonymized summary retains historical technical outcomes and coverage
limits while omitting private repository identities and operational records.
No new validation is claimed by this documentation update.

## Outcome

The scoped capacity-two campaign completed with a final pair of delivered
workflows, 177.439 seconds of successful provider overlap, and peak concurrency
of two. The second workflow refreshed its protected base on the same PR and
completed a fresh Builder, CI check, independent review and exact-head approval.
The final pair needed no provider retry, runtime restart, cancellation or
manual database/worktree repair.

| Cohort | Trials | Delivered | Cancelled |
| --- | --- | --- | --- |
| Original stress batch | 10 | 4 | 6 |
| First confirmation | 3 | 1 | 2 |
| Second confirmation | 2 | 1 | 1 |
| Final confirmation | 2 | 2 | 0 |

The original batch had no provider-call overlap between two eventually
delivered tickets, despite overlapping ticket lifetimes and peak concurrency
of two. Overall, eight deliveries across seventeen trials are not a clean
throughput or unattended-reliability benchmark: debugging, validation and
repair pauses consumed submission-based budgets. Monetary cost was unavailable.

Delivered candidates passed required CI and independent final review before
guarded exact-head approvals through SF. SF performed the merges without
protection bypass or duplicate merge mutations. The final read-only audit found
no active or quarantined providers, outstanding leases, executing or uncertain
effects, cross-ticket writes, or duplicate semantic operations. Failed trials
and draft evidence were retained. Final candidates changed only each ticket's
declared implementation and test files.

## Failures and repairs

- Generated verification assertions contradicted acceptance requirements in
  two original trials. Reviewer-owned tests were not silently edited; these
  remain product-quality failures.
- Worktree creation and command/commit contention could strand claims or
  commands. Authenticated absence recovery and bounded pre-insert contention
  handling repaired the reproduced cases without manual state edits.
- Protected merge-proof contention required supported restart recovery in one
  delivery. Bounded acquisition waits subsequently avoided that reproduced
  failure; uncertain external merges remain non-replayable without proof.
- An authenticated old PR base blocked refresh after the protected branch
  advanced. The repair accepts only the original published base or freshly
  observed remote base and rejects unrelated values.
- A refreshed workflow selected an obsolete final review and stalled on its
  stale fence. The regression failed before the repair and passed afterward;
  the final live pair confirmed fresh review of the direct refreshed successor.
- Provider indeterminacy, invalid artifacts, dirty-state retry refusal and
  budget exhaustion remained bounded failures. Added non-secret classification
  does not retrospectively identify earlier unknown causes.

## Recorded validation and limits

Full normal Go suites, targeted race tests and static gates passed at repair
checkpoints. Static gates included format, vet, repository, secret, artifact
and documentation checks plus release-build smoke. Compiled guarded, manual,
takeover and channel-coexistence acceptance also passed. The
[isolated baseline](2026-09-05-concurrency-stress-baseline.md) records 200 seeded
Store/runtime/Scheduler cases, including Store capacities two/four and runtime
two-worker/four-caller tests. This does not enable a four-worker daemon.

Representative regression references retained for coverage review:

- `TestDaemonFactoryTwoWorkerPauseDrainsOnlyTargetAndResumeRearms`
- `TestSeededLeaseAdmissionStress`
- `TestEnsureConcurrentCallersCreateExactlyOneWorktree`
- `TestEnsureReconcilesCreationResponseLossAfterReopen`
- `TestWorkerKeepsUnprovenPushUncertainAfterLostCommandResult`
- `TestUpdateFactoryPullRequestReconcilesLostResponseWithoutSecondEdit`
- `TestMergeLostResponseReconcilesFromOriginalBaseWitness`
- `TestInvalidArtifactFailureReasonsAreDurableAndBounded`
- `TestFallbackInvalidArtifactExhaustionIsStableAcrossRestart`
- `TestCreateFinalHandoffRefusesUnavailableOrMalformedBaseObservation`
- `TestTicketBudgetExhaustionIsStoreAuthenticatedAndNonRecoverable`
- `TestUnleasedRepositoryCommandCancellationCompletesAfterStartupRecovery`
- `TestPublishedBaseRefreshLostApplyResponseRecoversToBuilding`
- `TestPublishedBaseRefreshRejectsUnrelatedObservedPullRequestBase`

The push-loss fixture fails before Git runs, proving refusal of blind replay,
not recovery after an applied push loses its reply. The pause fixture checks
sibling continuity after pause, not a second snapshot after terminal
cancellation. Base refresh followed by a further CI-repair generation remains
unproven. Generated-test quality and queue-budget visibility remain priorities.
This is neither stable-v1 certification nor authority for unattended deployment
or a higher concurrency limit.
