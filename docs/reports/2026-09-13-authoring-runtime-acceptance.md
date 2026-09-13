# Ticket-first CLI: authoring runtime acceptance

Status: real draft/home operation acceptance passed; final broad CI and merge pending.

## Exact candidate and validation

Candidate: `cc55929652b1462c4e173497c63e63df3ac2216d`.
[GitHub validation](https://github.com/nysa-company/sf/actions/runs/34749449916)
passed CLI normal/race, scripted journeys, authoring regressions, native ICU
boundary checks, bundle build, repository, documentation and secret checks.
This targeted lane is not a substitute for the full repository acceptance gate.

The downloaded artifact manifest bound both GitHub and checkout SHA to that
candidate. Locally computed SHA-256 values matched before execution:

| Artifact | SHA-256 |
| --- | --- |
| processsupervisor.test | `2c89a11661ddfe91b1d364792998c1915b92140e28d816090387499aee0ec756` |
| sf-dev | `6cb818592985e14e6f6e04bff2138158e2a76c4697eb01d3167107ab7cd506eb` |
| authoring-icu-probe | `a07c59a4bf28db8b62c2dddd4205f06202d1d47d1aa12ec88a2c66f2bff65390` |

No local compilation or automated repository suite was run. The two narrow
host checks consumed these GitHub-built artifacts to validate the affected
macOS runtime and installed login unavailable in hosted CI.

## Root cause and bounded repair

Earlier versions failed before drafting. Binding `CLAUDE_CODE_TMPDIR` to the
already-private runtime directory removed one documented temp-directory
mismatch, but the next attempt terminated with a native trap and no output.
Exact-address inspection of the pinned binary traced the trap to an ICU
timezone enumeration count. The same process was denied the system ICU
timezone data file.

A code-owned, no-model C fixture exercised those ICU APIs in fresh processes.
Both GitHub macOS and the affected host observed:

| Property | Prior profile | Candidate profile |
| --- | --- | --- |
| ICU timezone count positive | No | Yes |
| Unrelated sentinel read denied | Yes | Yes |
| Unrelated sentinel write denied | Yes | Yes |
| Fork denied | Yes | Yes |
| Wait completed and process group absent | Yes | Yes |

The candidate adds only read access to `/private/var/db/timezone` and
`/var/db/timezone`, covering macOS's aliases within the system timezone tree.
No parent-directory grant, new write, fork or executable permission was added.
Authoring policy v4 rejects matching stale-v3 claims and capabilities.
The native probe passed in 0.27 seconds on the affected host.

The macOS report's `FOUNDATION` namespace alone did not identify a framework
cause; the investigation used exact code addresses, fixed denial categories
and reproduction rather than widening permissions from that label.

## Actual installed-provider result

`TestInstalledClaudeAuthoringPurposes` passed in 24.16 seconds with installed
Claude Code `2.1.263`, model `claude-sonnet-4-6`, existing OAuth and the pinned
CI gate. It used two fixed synthetic inputs with no repository context:

- Draft: returned a typed draft that rendered as valid ticket Markdown.
- Home: returned the typed `list` action with an empty selector.

Each purpose launched once, durably recorded its process identity, committed a
sanitized result with a verified drain proof, and replayed the completed turn
without another launch. The isolated Store had no tickets afterward.
No retry, live daemon restart, installation or existing-ticket mutation occurred.
The two turns may include bounded provider-internal requests; API count and
cost were not measured and are not asserted.

## Five-task workflow evidence and limits

| Task | Evidence | Limit |
| --- | --- | --- |
| Choose factory versus ticket command | Canonical command/help regressions and source launcher checks | Not a human discoverability score |
| Generate, preview and save a draft | Actual provider/Store operation above; connected CLI consent/preview/save regression | CLI transport in the connected regression is synthetic |
| Select a ticket | Fresh project-scoped inventory and short-selector regression | No live project inventory was modified |
| Start explicitly | Connected journey submits exact saved bytes, then starts only with explicit cost acceptance | No real factory ticket was started in this campaign |
| Interpret active, quiet and disconnected output | Connected view/watch journey verifies cursor continuity, quiet polls and reconnect behavior | Synthetic activity sequence, not human timing data |

`TestTicketConnectedAuthoringSaveStartViewWatchJourney` is deliberately labeled
synthetic. Together these layers establish production operation capability and
the connected CLI contract, not a measured end-to-end human usability study.
Activity presents bounded provider events and timing, not private reasoning or
raw secret-bearing transcripts. Other authoring providers are not established
by this Claude-only acceptance.
