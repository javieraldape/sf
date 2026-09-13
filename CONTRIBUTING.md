# Contributing to SF

SF is a trusted-local macOS beta. Contributions should preserve SQLite as the
only lifecycle authority, exact-head Git/GitHub checks, provider qualification,
and the documented fail-closed behavior.

## Before opening a change

Read `AGENTS.md`, `docs/product-brief.md`, `docs/architecture.md`, and the
relevant plan in `docs/plans/`. Keep changes small and use a branch matching
`feat/`, `fix/`, `docs/`, `test/`, or another approved prefix. Never commit
credentials, provider transcripts, raw command output, or generated state.

For a normal source change, run the repository's documented Go, repository,
and secret checks. Native provider, macOS sandbox, GitHub, and paid-account
acceptance are separate gates; fixture or hosted-CI success must not be
described as live provider delivery. Label evidence as simulated, native, or
live and record unknown billing as unknown.

## Pull requests

Describe the user-visible behavior, safety boundary, tests run, and any
unsupported environments. Include exact reproduction and redacted output for
failures. Do not merge, publish, install a daemon, or change an existing live
project without explicit authorization.

