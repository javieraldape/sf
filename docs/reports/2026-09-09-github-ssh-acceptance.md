# GitHub SSH acceptance summary

This anonymized summary retains historical technical evidence and limits while
omitting operator identity, private repository details and operational records.
No new validation is claimed by this documentation update.

Implementation acceptance completed on SF source
`fcb44195afbca4a3e81cd4d286ff58437d8142c4`.

## Requirement evidence

| Requirement | Recorded evidence |
| --- | --- |
| Independent review | Three agent lanes reviewed plan, transport and CLI diagnostics. SSH-option precedence, agent-only identity use and HTTPS diagnostic findings were repaired before final validation. |
| Origins and dispatch | `internal/gitssh/ssh_test.go` covers four supported origins, fixed endpoint/host keys and hostile argv/URL refusal. `internal/git/git_test.go` exercises real Runner/helper dispatch and a complete local Git protocol exchange. |
| Runtime composition | `internal/localruntime/factory_test.go` binds channel-specific sibling assets and an explicit agent, excluding SSH from prepublication-only composition. Runtime-assets and Store/publication tests cover asset and repository identity. |
| CLI/readiness | Auth tests cover explicit transport selection, existing login and no automatic key upload. Doctor tests distinguish API login, local assets/agent and unverified repository access, including conflicting fetch/push identities. |
| HTTPS compatibility | Existing suites passed; Doctor regressions preserve accepted `.github`, `_repo` and `-repo` names. No fallback transport or registered-origin rewrite was added. |

## Hosted validation

- [Focused SSH workflow](https://github.com/javieraldape/sf/actions/runs/34379839983)
  passed auth/CLI/Git/SSH/assets/localruntime/publication tests, a Store parser
  regression, selected race tests, vet, repository/docs/secret checks and a
  complete development bundle build.
- [Full acceptance](https://github.com/javieraldape/sf/actions/runs/34379860551)
  passed all 21 jobs, including Store/runtime shards, broad race lanes,
  crash/recovery, security, upgrade, compiled end-to-end and static validation.

A superseded fixture failed with a broken pipe because its fake SSH helper
exited before Git completed the exchange. The repaired fixture uses real local
`git-upload-pack`; the failing run was not counted as passing.

## Host acceptance and limits

The exact CI-built development helper authenticated an explicitly selected
SSH agent, read a private repository's protected branch and completed a push
dry-run using pinned host keys and a scrubbed environment. The dry-run did not
create or update a remote ref. No live SSH ticket, PR, merge, protection change
or private-repository CI execution is established by this test. Higher-level
workflow evidence comes from hosted hermetic tests. No local automated test
suite or build ran for this host acceptance.

No private-key contents were read, copied or uploaded. The default agent was
empty; explicit agent selection succeeded. A foreground daemon must inherit
the working `SSH_AUTH_SOCK` and restart when that socket changes. GitHub API
authentication remains independently required for PRs/checks/merges. The
[SSH setup guide](../how-to/github-ssh.md) describes matching-channel assets,
fixed port 443, agent recovery and origin-drift refusal. This acceptance did
not itself establish deployment or installed-runtime changes.
