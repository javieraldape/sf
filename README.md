# SF — Software Factory

**Turn a small, well-defined ticket into a tested GitHub pull request, with AI doing the work and you controlling the handoff.**

SF is an open-source CLI for developers who already use AI coding tools and want
to delegate a workflow, not supervise every prompt. You describe the change;
SF coordinates planning, independent verification, implementation, local tests,
and a draft PR. Stop there, or continue through CI, review, and human-approved merge.

**Current status: macOS source-build beta.** Start with a disposable repository
and a small ticket. SF is not yet a universal project runner or a one-command installer.

## Why use SF?

- **Delegate a ticket, not just a coding session.** Keep the request, acceptance criteria, implementation, and verification connected.
- **Separate building from reviewing.** Use independently qualified Builder and Reviewer models, including supported Codex/Claude pairs.
- **Keep your working branch separate.** Each ticket uses its own linked Git worktree.
- **Retain control.** Inspect progress, pause or cancel a ticket, and choose whether to stop at a draft PR.
- **Keep durable state.** SF records workflow state and evidence locally in SQLite. Some interrupted or failed operations still require operator intervention.

SF orchestrates your provider CLIs; it is not another model subscription.
You need your own supported provider accounts. The coordinator runs on your
Mac, but provider requests and GitHub operations use their respective services.
“Local” does not mean offline or that code never leaves your machine.

## How it works

```text
Draft → Plan → Independent verification → Build and test → Draft PR
                                                             │
                             --until pr: pause here ◀────────┤
                                                             ▼
                  CI and corrections → Final review → Human approval → Merge
```

1. **Draft:** write a Markdown ticket, import a GitHub Issue, or use guided creation. Saving a draft does not start work.
2. **Plan:** SF asks a model to turn the request into an implementation plan.
3. **Verify:** a separate Reviewer defines checks the implementation must satisfy.
4. **Build:** the Builder implements the change; SF runs the configured, supported test recipe.
5. **Publish:** SF opens or updates a draft PR. With `--until pr`, the ticket pauses durably here.
6. **Continue when wanted:** the full workflow checks CI, handles bounded corrections, and performs final review. Guarded merge requires human approval of the reviewed commit. Manual mode leaves the merge to you on GitHub.

Failures and uncertain outcomes can pause or block work instead of silently
retrying a mutation. Autonomous merge is unavailable in this beta.

## Supported projects

SF runs supported test recipes, not arbitrary shell commands.

| Project | Current support |
|---|---|
| Go | Dependency-free modules or compatible checked-in vendor dependencies |
| JavaScript / Node | Dependency-free projects using `node --test`, with an existing discoverable test |
| TypeScript | A restricted, explicitly configured pure-test profile; not general TypeScript support |
| Python | Experimental pinned Python/pytest profile on Apple Silicon, without additional dependencies |
| Ruby on Rails | Not supported locally yet |

Provider and project support are separate: selecting another model does not
enable an unsupported test recipe. Codex is supported; Claude/Codex pairs require
qualification. Cursor is experimental. See the [first-ticket guide](docs/tutorials/first-ticket.md)
for exact tested scope and [configuration](docs/configuration.md) for constraints.

## Get started

You need macOS, Git, **Go 1.25+**, the official GitHub CLI (`gh`), and supported
provider CLIs. This example uses a Claude Builder and Codex Reviewer.
Codex requires its matching `codex-code-mode-host` executable; check the
[prerequisites](docs/tutorials/source-build-foreground.md#prerequisites).

### 1. Build SF and sign in

Clone this repository and build from the **SF source directory**:

```sh
git clone https://github.com/javieraldape/sf.git
cd sf
make build-dev
export PATH="$PWD/scripts:$PWD/bin:$PATH"
sf version --json
sf auth login github
sf auth login claude
sf auth login codex
```

This makes `sf` invoke this checkout's development binary, `sf-dev`. Use the
same absolute PATH entries in both terminals below. It does not replace an
installed stable SF or move its data. Docker and Colima are not required.

### 2. Register your project

Change into **the project you want SF to work on**, not the SF source directory.
Use a clean, committed checkout with a GitHub origin and a local base branch.
For Node, commit a meaningful baseline test before setup.

```sh
cd /absolute/path/to/your-project
sf init --check
sf init --providers claude-codex
```

Review any generated `.sf/config.toml` and commit the intended configuration
before starting a ticket. Note the project name printed by `init`.
Unsupported or ambiguous projects are refused; follow the reported next action.

### 3. Run the factory

In a second terminal with the same SF PATH and authentication settings:

```sh
sf factory run
```

Leave it running. This starts the foreground coordinator, **not a ticket**.
It does not need to run inside your product repository; registration tells it
where the project lives. Back in your project terminal:

```sh
sf providers qualify --preset claude-codex
sf doctor --repo .
```

Qualification checks provider readiness and may invoke paid models. Login alone
is not qualification. Use the same pair at registration and qualification.

### 4. Create a ticket and stop at a draft PR

The offline guided form makes no model call:

```sh
sf ticket new --no-ai ticket.md
sf ticket validate ticket.md
```

Describe one observable change and its acceptance criteria. Review the draft,
then explicitly start it, replacing `my-project` with your registered name:

```sh
sf ticket start --file ticket.md --project my-project --until pr --accept-cost-estimates --watch
```

The Claude/Codex example requires consent to **estimated** accounting. Estimates
are not a hard billing cap. Deadlines begin at submission, so queueing and pauses
consume the ticket's time budget.

After publication, the ticket is `paused` with reason `pr_opened`. GitHub checks
may still run, but SF does not start CI corrections, final review, or merge.
Restarting the daemon does not resume it. Use `sf ticket resume <ticket>` only
when you want the full workflow to continue.

For AI-assisted drafting, Issue import, a complete sample ticket, and the full
merge workflow, follow [Run your first ticket](docs/tutorials/first-ticket.md).

## Everyday commands

| What you want | Command |
|---|---|
| Start the foreground factory | `sf factory run` |
| Check the factory | `sf factory status` |
| Create an offline draft | `sf ticket new --no-ai ticket.md` |
| Find tickets | `sf ticket list` |
| Inspect a ticket | `sf ticket view <ticket>` |
| Watch progress | `sf ticket watch <ticket>` |
| Start a submitted ticket | `sf ticket start <ticket> --until pr` |
| Pause / resume / cancel | `sf ticket pause <ticket>` / `sf ticket resume <ticket>` / `sf ticket cancel <ticket>` |
| Diagnose setup | `sf doctor --repo .` |

Add `--accept-cost-estimates` when starting a Claude/Cursor ticket. Interactive
selection and unique ID prefixes avoid copying full IDs; scripts must supply
an unambiguous selector. Use `--help` and `--json` for automation.

Watching shows state, process activity, and bounded provider-reported events
when available, **not private model reasoning or raw transcripts**. Ctrl-C
stops the watcher, not the ticket. Ctrl-C in the factory terminal stops the
foreground factory. Resume, retry, and take have different meanings; follow
the [CLI reference](docs/cli.md), not database or worktree edits.

## Safety and limitations

- Use trusted repositories on a trusted Mac. SF is not a security boundary against a hostile same-user process.
- Keep credentials out of tickets and reports. Use the supported interactive login flows.
- Inspect the actual PR diff before approving a merge. A changed candidate requires fresh evidence and approval.
- Retain the registered repository, worktrees, and runtime snapshots. A database backup alone cannot reconstruct an interrupted checkout.
- Concurrent work is bounded by configured machine, project, and provider capacity; tickets do not necessarily start simultaneously.
- Automated fixtures do not prove every hosted provider/project combination. Broader language support and hosted cross-provider acceptance are still being expanded.

## Documentation and contributing

- [First ticket](docs/tutorials/first-ticket.md): a full walkthrough and compatibility matrix.
- [Configuration](docs/configuration.md): providers, test recipes, and project settings.
- [CLI reference](docs/cli.md): commands, controls, and accounting.
- [Architecture](docs/architecture.md): coordinator, providers, and durable state.
- [Contributing](CONTRIBUTING.md): development setup and verification requirements.
- [Security](SECURITY.md): private reporting; never attach credentials or raw provider logs to a public issue.

## License and development build

SF source is available under the [MIT License](LICENSE). Third-party
dependencies retain their own licenses. Public release packaging and publisher
verification remain separate from this local source-build beta.

`make build-dev` embeds the development channel and exact source commit.
The `scripts/sf` launcher always uses this checkout's `bin/sf-dev` and matching
helpers. Help and recovery actions may retain the explicit `sf-dev` name.
Remove the source PATH prefix to select an installed stable `sf` again.
Stable artifacts are intentionally explicit: `make build VERSION=<semver>`
embeds the stable channel and refuses to create an unversioned stable binary.
Neither command copies state between the isolated channels.

Detailed product, architecture, verification, and state-machine contracts live
in [`docs/plans/`](docs/plans/).
