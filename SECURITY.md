# Security policy

SF is a trusted-local beta and is not a hostile same-UID or general-purpose
sandbox. Do not use it with secrets or repositories that require stronger
isolation than the documented profile provides.

## Report privately

Use [GitHub private vulnerability reporting](https://github.com/javieraldape/sf/security/advisories/new),
which is enabled for this repository. Do not open a public issue
for an unpatched vulnerability. Include a minimal reproduction, affected
version/commit, platform, and impact. Redact tokens, cookies, OAuth data,
private paths, provider transcripts, and credential-bearing URLs before
sending anything.

Never attach `~/.config/gh`, provider home directories, database files,
environment dumps, or raw logs. SF diagnostics must remain bounded and
redacted; a provider error or credential-bearing URL is untrusted input.

## Scope limits

Provider login is not execution qualification. Credentials are kept in the
operator's existing official login/keychain flow and must not be copied into
GitHub, tickets, tests, fixtures, or repository configuration. Report any
credential disclosure, unauthorized Git/GitHub mutation, qualification bypass,
duplicate external effect, or lifecycle state that contradicts SQLite.
