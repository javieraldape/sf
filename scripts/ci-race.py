#!/usr/bin/env python3
"""Partition hosted acceptance suites without omitting tests.

CI runs every `other`, `runtime-race`, and Store shard on isolated macOS runners. Local
`make test-race` remains the unpartitioned reference command.
Normal workflow-runtime integration has separate disjoint shards; the other
integration packages remain in the Makefile's integration-other lane.
Crash shards preserve the reference Makefile's exact top-level selection.
"""

import argparse
from collections import deque
import json
import math
import os
from pathlib import Path
import re
import subprocess
import sys
import time

STORE = "github.com/nysa-company/sf/internal/store"
RUNTIME = "github.com/nysa-company/sf/internal/workflowruntime"
FLAGS = ["-race", "-count=1", "-shuffle=off", "-p", "1", "-timeout", "60m"]
INTEGRATION_FLAGS = ["-count=1", "-shuffle=off", "-p", "1", "-timeout", "30m"]
CRASH_PATTERN = "(^Test.*Crash|Crash|Recovery|Recover|Rearm|Quarantine)"
# Scheduling hints only, from macOS run 34416329428 (2026-09-09). The live
# inventory is always authoritative: new names receive weight 1, never skip.
PACKAGE_SECONDS = {
    "github.com/nysa-company/sf/" + name: seconds for name, seconds in {
        "cmd/sf": 214, "internal/daemon": 618, "internal/git": 391,
        "internal/publication": 229, "internal/worktreecoord": 225,
        "internal/providercoord": 219, "internal/daemon/runtimecontrol": 109,
        "internal/cli": 99, "internal/localruntime": 89,
        "internal/processsupervisor": 56, "internal/workflowworker": 46,
        "internal/ghrunner": 45, "internal/github": 43, "internal/engine": 43,
        "internal/codexprovider": 29, "internal/bundle": 16,
        "internal/mergeproof": 12,
    }.items()
}
RUNTIME_RACE_SECONDS = {
    "TestPostbuildAmendmentCandidateFinalizationRecovery": 564,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecovery": 542,
    "TestPostbuildRepairCandidateFinalizationRecovery": 291,
    "TestRepositoryMaterializerPostbuildAmendmentRealEndToEnd": 217,
    "TestRepositoryMaterializerPostbuildRepairRealEndToEnd": 84,
    "TestRepositoryMaterializerPreparePostbuildRepairRealBoundary": 74,
    "TestRepositoryMaterializerRealSourceResumePreparedObservationLoss": 68,
    "TestRepositoryMaterializerRealStoreGitReplay": 43,
}
# Scheduling hints are intentionally scoped by execution mode: race, ordinary
# integration and crash instrumentation have measurably different costs. These
# reviewed values came from hosted macOS run 34771148550. Unknown/new tests use
# the conservative default below and remain in the live inventory.
STORE_RACE_SECONDS = {
    "TestCIV41CompositeForeignKeyTamperingRejectsOpenAndReadOnly": 274,
    "TestPostbuildRepairCandidateHandoffAndRecovery": 92,
    "TestPublicationEvidenceLifecycleReplayRecoveryAndBackup": 85,
    "TestPostbuildPendingAmendmentTwoRecoveriesAndDecision": 80,
    "TestFenceRecoveredRunnersAcceptsRepeatedArmedPostPublicationCrashes": 77,
    "TestRepositoryCommandResultAuthenticatedHistoricalLoadAndTampering": 60,
    "TestFenceRecoveredRunnersAcceptsArmedPostPublicationRearm": 57,
    "TestProtectedBaseRefreshReviewedRecoveryRejectsTamperedHistory": 53,
    "TestRunnerRecoveryAuthorityAuthenticatesControlGaps": 48,
    "TestProtectedBaseRefreshReservationPreservesCompletedCIRepairParent": 45,
    "TestAuthenticatePostbuildFailureRefusals": 44,
    "TestBeginProviderAttemptRejectsInvalidDirectLaunchInput": 41,
    "TestControlProofFencesEveryStoreAdmissionAtLinearization": 38,
    "TestCandidateRepairCurrentReadersAndRearmRejectBrokenRecoveryPrefix": 37,
    "TestCurrentAttestedProviderPairChecksEveryRoleAndRestart": 36,
    "TestSeededLeaseAdmissionStress": 34,
    "TestProviderRetryWaitingApprovalRearmDecisionAndMergingRestarts": 33,
}
RUNTIME_INTEGRATION_SECONDS = {
    name: max(1, seconds / 3) for name, seconds in RUNTIME_RACE_SECONDS.items()
}
CRASH_RUNTIME_SECONDS = {
    "TestPostbuildAmendmentCandidateFinalizationRecovery": 540,
    "TestPostbuildRepairCandidateFinalizationRecovery": 260,
}
MODE_WEIGHTS = {
    "store": STORE_RACE_SECONDS,
    "runtime-race": RUNTIME_RACE_SECONDS,
    "runtime-integration": RUNTIME_INTEGRATION_SECONDS,
    "crash-runtime": CRASH_RUNTIME_SECONDS,
}
MAX_OUTPUT_BYTES = 1024 * 1024


def race_packages(packages, mode):
    if mode not in ("other", "runtime-race"):
        raise ValueError("invalid package partition mode")
    if (not packages or len(set(packages)) != len(packages)
            or any(not p or p.strip() != p or any(c.isspace() for c in p) for p in packages)):
        raise ValueError("empty, duplicate, or malformed package inventory")
    if packages.count(STORE) != 1 or packages.count(RUNTIME) != 1:
        raise ValueError("Store or workflow runtime missing or duplicated in package inventory")
    selected = [p for p in packages if (p == RUNTIME if mode == "runtime-race"
                                      else p not in (STORE, RUNTIME))]
    if not selected:
        raise ValueError("empty race package partition")
    return selected


def partition(names, index, count):
    if not 1 <= count <= 64 or not 0 <= index < count:
        raise ValueError("invalid shard index/count")
    if not names or len(set(names)) != len(names):
        raise ValueError("empty or duplicate test inventory")
    selected = sorted(names)[index::count]
    if not selected:
        raise ValueError("empty shard")
    return selected


def balanced_partition(names, index, count, weights):
    # Validate the complete live inventory first. Longest-first greedy packing
    # is deterministic, complete and disjoint even with missing/stale timings.
    partition(names, index, count)
    if any(not isinstance(w, (int, float)) or not math.isfinite(w) or w <= 0
           for w in weights.values()):
        raise ValueError("invalid scheduling weight")
    shards, totals = [[] for _ in range(count)], [0] * count
    for name in sorted(names, key=lambda n: (-weights.get(n, 1), n)):
        destination = min(range(count), key=lambda i: (totals[i], i))
        shards[destination].append(name)
        totals[destination] += weights.get(name, 1)
    return sorted(shards[index])


def inventory(output):
    # go test -list also emits its package summary. Benchmarks are not run by
    # ordinary go test; examples and fuzz seeds are, so include both here.
    names = []
    for line in output.splitlines():
        if re.fullmatch(r"(?:Test|Example|Fuzz)\w*", line):
            names.append(line)
        elif re.match(r"^(?:ok|\?)\s+", line) or line.startswith("Benchmark") or not line:
            continue
        else:
            raise ValueError("unexpected test inventory output: " + line)
    return names


def write_artifact(directory, mode, index, count, names, selected, exit_code=None,
                   elapsed=None, timings=None, output_tail=""):
    """Write one bounded, machine-readable shard record without affecting truth."""
    if not directory:
        return
    path = Path(directory)
    path.mkdir(parents=True, exist_ok=True)
    record = {
        "schema": 1, "mode": mode, "shard": index, "shard_count": count,
        "inventory": sorted(names), "selected": sorted(selected),
        "exit_code": exit_code, "elapsed_seconds": elapsed,
        "test_timings": timings or {}, "output_tail": output_tail[-MAX_OUTPUT_BYTES:],
    }
    (path / f"{mode}-{index}.json").write_text(
        json.dumps(record, indent=2, sort_keys=True) + "\n")


def run_recorded(command, directory, mode, index, count, names, selected):
    if not directory:
        return subprocess.call(command)
    started = time.monotonic()
    output = deque()
    output_bytes = 0
    timings = {}
    # -v output stays human-readable in the Actions log and is also parsed into
    # scheduling evidence. The bounded artifact retains the tail on failures.
    process = subprocess.Popen(command, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True)
    assert process.stdout is not None
    try:
        for line in process.stdout:
            print(line, end="", flush=True)
            output.append(line)
            output_bytes += len(line.encode("utf-8", errors="replace"))
            while output and output_bytes > MAX_OUTPUT_BYTES:
                output_bytes -= len(output.popleft().encode("utf-8", errors="replace"))
            match = re.match(r"--- (?:PASS|FAIL|SKIP): (\S+) \(([0-9.]+)s\)", line.strip())
            if match and "/" not in match.group(1):
                timings[match.group(1)] = float(match.group(2))
        exit_code = process.wait()
    finally:
        elapsed = time.monotonic() - started
        write_artifact(directory, mode, index, count, names, selected,
                       process.poll(), elapsed, timings, "".join(output))
    return exit_code


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["other", "runtime-race", "store", "runtime-integration", "crash-other", "crash-runtime"])
    parser.add_argument("--index", type=int, default=0)
    parser.add_argument("--count", type=int, default=1)
    parser.add_argument("--list-only", action="store_true")
    parser.add_argument("--artifact-dir", default=os.environ.get("SF_CI_ARTIFACT_DIR", ""))
    args = parser.parse_args()
    if args.mode in ("other", "crash-other"):
        packages = subprocess.check_output(["go", "list", "./..."], text=True).splitlines()
        selected = race_packages(packages, "other")
        if args.mode == "other":
            names = selected
            selected = balanced_partition(names, args.index, args.count, PACKAGE_SECONDS)
            command = ["go", "test", *FLAGS, "-v", *selected]
        else:
            names = [p for p in packages if p != RUNTIME]
            selected = names
            command = ["go", "test", *INTEGRATION_FLAGS, "-v", *selected,
                       "-run", CRASH_PATTERN]
        print(f"{args.mode} shard {args.index + 1}/{args.count}: " + ", ".join(selected), flush=True)
    else:
        package = STORE if args.mode == "store" else RUNTIME
        race = args.mode in ("store", "runtime-race")
        flags = FLAGS if race else INTEGRATION_FLAGS
        inventory_flags = ["-race"] if race else []
        output = subprocess.check_output(
            ["go", "test", *inventory_flags, "-list", ".", package], text=True
        )
        names = inventory(output)
        if args.mode == "crash-runtime":
            names = [n for n in names if re.search(CRASH_PATTERN, n)]
        selected = balanced_partition(names, args.index, args.count, MODE_WEIGHTS[args.mode])
        print(f"{package} shard {args.index + 1}/{args.count}: {len(selected)}/{len(names)} tests", flush=True)
        command = ["go", "test", *flags, "-v", package,
                   "-run", "^(?:" + "|".join(re.escape(n) for n in selected) + ")$"]
    if args.list_only:
        write_artifact(args.artifact_dir, args.mode, args.index, args.count,
                       names, selected)
        print("\n".join(selected))
        return 0
    write_artifact(args.artifact_dir, args.mode, args.index, args.count,
                   names, selected)
    return run_recorded(command, args.artifact_dir, args.mode, args.index,
                        args.count, names, selected)


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (ValueError, subprocess.CalledProcessError) as exc:
        print(str(exc), file=sys.stderr)
        sys.exit(1)
