# Bounded Go helper verification for Alexandria

Status: prerequisite implementation authorized; source complete, GitHub validation pending.

## Outcome

Enable the approved Alexandria extensionless-filename ticket to run through SF
without loading its unvendored application dependencies. The user approved this
prerequisite after read-only init refused the project. Do not implement the
Alexandria fix here; SF must still write its verification and implementation.

## Contract

- Exact argv: `go --sf-go-pure-files-v1 <source.go> <source_test.go>`.
- Same directory, exactly matching stem, canonical relative paths, no symlinks,
  1 MiB per file. Readiness alone tolerates an absent test for verification-first.
- Parse bounded bytes; only an explicit standard-library import allowlist,
  matching non-main package, no Go compiler/build/embed directives or cgo.
- Copy parsed bytes into private staged files. Compile only those files with
  modules off, private GOPATH, no ambient flags, no network dependency resolution.
- Retain current immutable command/spec/policy binding, authenticated worktree
  identity, staged compiler and executable, process group recording/acknowledgment,
  strict test sandbox, timeout, quarantine and drain behavior. Retain supervisor
  scratch/toolchain/gate while process drain is ambiguous.
- No SQLite migration, arbitrary command admission, vendor installation, provider
  policy expansion, live database edit, or production deployment.

## Validation

GitHub-only unit/race/admission and native compiled supervisor fixtures. Cover
unvendored unrelated dependencies and invalid unselected Go files without compiling
them; missing test readiness versus execution; canonical path/flags/imports/
directives/size/symlink rejection; staged-byte independence from source replacement;
real test-gate process-group traversal. Preserve the existing module recipe suite.
Native sandbox negative tests remain applicable because the gate/profile is unchanged.

## Delivery gate

Do not run Alexandria on this source until GitHub validation and independent
review pass, and a verified bundle containing the new recipe is available.
Then configure only the isolated Alexandria pilot target and run the original
ticket to authoritative done. Passing this prerequisite does not satisfy that goal.
