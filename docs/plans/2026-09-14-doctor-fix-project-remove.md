# Safe setup repair and reversible project removal

Status: Approved; implementation and GitHub CI validation in progress.

## Goal

Let an operator repair known local setup faults with `sf doctor fix` and
disconnect a project with `sf project remove` without deleting source code,
configuration, worktrees, ticket history, or GitHub artifacts. Ship both through
a reviewed PR with passing GitHub CI.

## Command contract

- `sf doctor` remains read-only.
- `sf doctor fix --dry-run` reports a proposed repair without changing anything.
- `sf doctor fix` previews and confirms in a terminal; noninteractive mutation
  requires `--yes`. That flag authorizes only the predefined safe repair list.
- Repairs are limited to authenticated SF-owned directory setup/permissions.
  Login, missing executables, runtime preparation, qualification, low disk,
  incompatible databases, unhealthy daemons, and quarantines remain guided
  actions unless an existing narrow authority safely supports a repair.
- No automatic software installation, paid provider calls, daemon restarts,
  process killing, credential access changes, database resets, or Git changes.
- `sf project list` lists active registrations; `--all` includes removed ones.
- `sf project remove [project]` previews the exact channel, repository and
  retained data. Omitted identity uses a terminal picker. Automation supplies
  an exact project and explicit confirmation. `--dry-run` is read-only.
- Removal requires no unfinished tickets and no unresolved processes, leases,
  or external effects. Refusals name the next action; removal does not cancel
  tickets implicitly.
- Removal is durable and reversible, not a cascading SQL delete. All project
  admission paths enforce the removal state. Same-project concurrent start or
  submit is serialized with removal, including direct Store callers.
- `sf init` may reactivate a removed exact registration after normal repository
  and configuration validation. It never rewrites historical ticket evidence.
- Other projects, the shared daemon, and the other channel are untouched.

## Authority and reuse

Reuse the typed doctor checks, official auth/setup commands, CLI confirmation
and JSON envelope, and Store transaction/migration mechanisms. Project
maintenance is a direct local setup operation, like init/config apply, but all
admission and retirement decisions are made atomically by Store. A preview is
not authority: committing removal revalidates its registration and blockers.

```text
Doctor: diagnose -> preview -> confirm -> authenticate -> repair -> diagnose
Remove: identify -> preview -> confirm -> atomic safety check -> mark removed
                                             | blocked -> exact next action
Init: validate repository/config -> atomic exact reactivation
```

## Implementation tasks

1. Add bounded repair planning/execution and CLI tests with real isolated
   filesystem fixtures. Keep diagnosis and mutation separate.
2. Add project lifecycle schema, read-only inventory/preview, atomic retirement,
   reactivation, and guards against new admissions and configuration mutations.
3. Add project commands, terminal selection, confirmation, typed errors,
   versioned JSON and readable summaries.
4. Document retained data, active-ticket refusal, repair limits and reactivation.
5. Run focused and full GitHub CI; review safety boundaries, then merge.

## Verification

All new branches need unit or integration coverage. Required end-to-end cases:

- Bad SF-owned directory mode -> preview unchanged -> fix -> doctor recheck;
  second fix is idempotent. A missing directory is created privately.
- Symlink, foreign ownership, swapped path, cancelled input, partial failure,
  unsafe permissions and unsupported repair never mutate unrelated paths.
- Guided actions never execute login, provider calls or external installation.
- Init -> list -> preview -> remove -> reopen -> list all -> init reactivation;
  source/config/dirty retained files and historical rows stay unchanged.
- Unfinished ticket, unresolved effect, writer lease and quarantine each block
  removal. Concurrent submit/start either wins first and blocks removal, or
  loses to removal and is refused.
- A stale preview cannot remove a newly reactivated registration.
- Terminal picker cancellation, missing identity in JSON, dry-run, explicit
  confirmation, repeated removal, wrong channel and unknown project.
- Other projects remain available across retirement and daemon restart.

Tests run in GitHub CI, including hosted macOS filesystem/CLI fixtures. No live
user state or provider/GitHub credentials are required by the acceptance tests.

## Not in scope

Destructive purge, worktree garbage collection, deleting `.sf/config.toml`,
closing PRs, resetting project history, arbitrary bug repair, automatic
installation and automatic factory restart. These require separate explicit
contracts, not a force flag on this operation.
