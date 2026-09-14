# CLI onboarding acceptance summary

This anonymized summary retains historical outcomes, failures and test
references while omitting private repository identities, workstation details
and live runtime chronology. This update adds no new validation evidence.

## Verdict

Local CLI implementation and automated acceptance gates passed on macOS ARM64.
A fresh CLI-led Go trial reached durable `done` through authenticated recovery,
with one approval, one merge intent and zero remaining capacity leases. It
required source repairs and author intervention, so it is not an unassisted
onboarding success. An earlier trial merged remotely but lost its registered
repository and worktree after reboot; terminal delivery remains unproved.
That failed persistence trial remains in the reliability denominator.

## Requirement evidence

| Requirement | Recorded evidence and limit |
| --- | --- |
| Install bundle | Build, six-payload manifest including LICENSE, verification, exclusive installation, installed identity, exact license copy and release-build smoke passed. Inventory, ancestry, symlink and overwrite faults are covered. Integrity does not establish publisher authentication. |
| Local onboarding | `TestCompiledDevOnboardingUsesPrivateHomeAndLocalCommands` covers helpers, non-mutating preview, registration replay, templates, PTY preview/save/cancel and stable-channel isolation without launching a provider. |
| Readiness | `TestInitCheckIsReadOnlyAndDoesNotClaimFullReadiness` and `TestInitCheckExplainsUnsupportedStacksWithoutRunningThem` distinguish recipe readiness from untested runtime/provider/publication readiness. |
| Stack gates | Native Go, dependency-free Node and restricted TypeScript acceptance executed and passed. Pinned Python cold preparation, registration and compiled workflow passed with explicit download opt-in. |
| Selection and run/watch | Real-PTY tests cover duplicate-title disambiguation, cancellation and full-head decision binding. CLI and Store/socket tests cover exact submit/start/watch, no implicit resume and refusal of blind mutation retries. Interrupting a live watcher did not cancel its ticket. |
| Diagnostics | Budget tests cover queue/pause time, elapsed/future clocks and terminal rendering. Immutable review projections cover tamper refusal, redaction, bounds, historical labels and unavailable results without mutation authority. |
| Python faults | `TestPreparedPythonStoreExecution` executed pass, red, timeout, cancellation, quota, file-limit, restart and restart-unclear cases. Restart-unclear covers a surviving child, quarantine, competing-writer refusal, no fabricated result and eventual zero lease residue. |
| Workflow invariants | Full Store/daemon/workflow regressions and compiled guarded/manual/takeover acceptance passed. Manual mode never requests merge; guarded mode requires exact approval. |
| Isolation and capacity | Compiled coexistence and `TestLeaseCapacityIsBoundedUnderConcurrency` passed. Provider capacity remains bounded to one/two; this checkpoint changed no production setting. |

The licensed bundle checkpoint used SF source
`b79166a4ea965b70914b0c460d191aaefa53d769`. Bundle tests authenticate the
license payload and preserve it during installation. The
[first-ticket tutorial](../tutorials/first-ticket.md) records prerequisites,
support matrix, validation, run/watch and exact-head approval.

## Failures and repairs retained

- GitHub and Codex authentication diagnostics disagreed with runtime selection
  of explicit configuration. Repairs aligned selection without copying credentials.
- Installation accepted a private leaf under shared writable ancestry that
  runtime activation rejected. The installer now enforces the same boundary
  and authenticates installed helpers; its regression failed before the repair
  and passed afterward.
- A scrubbed environment selected temporary storage incompatible with Git's
  executable-parent validation. Diagnosis established a composition/readiness
  mismatch without relaxing validation.
- Cancellation propagated into cleanup and prevented drain proof. A bounded
  independent cleanup context repaired the reproduced case while retaining
  refusal when cleanup remains uncertain.
- Explicit host-checkpoint/reboot recovery commands passed automated coverage
  and live checkpoint/same-boot refusal. The original reboot trial lost its
  registered checkout and cannot establish successful post-reboot recovery.
  Database preservation alone does not authenticate a recreated worktree.
- The durable trial's final reviewer reported an inspection limitation.
  Prompt clarification preserved read-only boundaries, but a diagnostic pass
  was not treated as an authoritative Store review.
- Recovery exposed typed-block rearming, consumed-verdict reuse, missing
  scheduler stop state, compensated endpoint and historical review-reader
  defects. Reproducing Store/daemon tests and strict recovery-authority repairs
  enabled fresh review, guarded approval and terminal reconciliation without
  resetting budgets or manually repairing database/worktree records.
- CLI status lacked review findings during diagnosis. Authenticated, bounded
  historical review projections repaired that visibility gap.

The successful durable trial retained two final-review attempts. Approval was
accepted after the original deadline elapsed without a budget change. The
earlier lost-environment trial is a remotely merged change with an unproved
terminal result, not a success repaired by the later trial. Future reboot
acceptance must preserve registered repositories and worktrees in durable,
owner-only storage; supported lost-checkout recovery was not established.

## Recorded validation and remaining limits

Historical validation passed full Go suites, targeted race tests, vet,
repository/secret/docs/artifact checks and bundle/release-build smoke at the
relevant checkpoints. `make test-compiled-e2e` and explicit
`SF_TEST_PYTHON_CLI_DOWNLOAD=1 make test-python-e2e` passed. The Python target
requires macOS ARM64 and download consent; skipped tests are not execution
evidence. Python workflows use real interpreter/repository execution with
controlled provider/GitHub fixtures, so live-model Python delivery is unproved.

A sandbox run could not preserve special-mode fixture bits; its exact host
rerun passed. A Python fixture extension's full run failed two Go workflow
fixtures due to disk exhaustion and did not execute chained static checks.
After obsolete build-cache cleanup, the exact regressions, fresh full suite
and static checks passed. Those failed runs are not counted as passing.

Rails, general dependency-bearing Node/TypeScript, extra Python dependencies
and actual Claude execution remained unsupported at this checkpoint. Three
unfamiliar users, setup within ten minutes and at least nine unassisted
deliveries out of ten were unobserved. Public signing/distribution, third-party
notices, upgrade experience and universal rollback remained pending. These
results establish neither external beta adoption nor public/stable release.
