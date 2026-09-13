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
# Scheduling hints only, refreshed from successful macOS run 34786460096
# (2026-09-13). The live
# inventory is always authoritative: new names receive weight 1, never skip.
PACKAGE_SECONDS = {
    "github.com/nysa-company/sf/" + name: seconds for name, seconds in {
        "cmd/sf": 159, "internal/daemon": 639, "internal/git": 621,
        "internal/publication": 227, "internal/worktreecoord": 240,
        "internal/providercoord": 241, "internal/daemon/runtimecontrol": 129,
        "internal/cli": 88, "internal/localruntime": 102,
        "internal/processsupervisor": 139, "internal/workflowworker": 48,
        "internal/ghrunner": 47, "internal/github": 63, "internal/engine": 48,
        "internal/codexprovider": 24, "internal/bundle": 13,
        "internal/mergeproof": 17,
    }.items()
}
RUNTIME_RACE_SECONDS = {
    "TestPostbuildRepairCandidateFinalizationRecovery": 444,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoveryNewLeader": 269,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySameFence": 240,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySyncedNewLeader": 229,
    "TestRepositoryMaterializerPostbuildAmendmentRealEndToEnd": 228,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecovery": 223,
    "TestPostbuildAmendmentCandidateFinalizationRecovery": 219,
    "TestPostbuildAmendmentCandidateFinalizationRecoveryAfterRestart": 216,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecoveryAfterRestart": 202,
    "TestRepositoryMaterializerPreparePostbuildRepairRealBoundary": 83,
    "TestRepositoryMaterializerRealSourceResumePreparedObservationLoss": 80,
    "TestRepositoryMaterializerPostbuildRepairRealEndToEnd": 66,
    "TestRepositoryMaterializerRealStoreGitReplay": 43,
}
# Scheduling hints are intentionally scoped by execution mode: race, ordinary
# integration and crash instrumentation have measurably different costs. These
# reviewed values came from successful shards in the hosted macOS runs named in
# WEIGHT_PROVENANCE. Unknown/new tests use the conservative default below and
# remain in the live inventory.
STORE_RACE_SECONDS = {
    "TestCIV41CompositeForeignKeyTamperingRejectsOpenAndReadOnly": 293,
    "TestPostbuildPendingAmendmentTwoRecoveriesAndDecision": 114,
    "TestPostbuildRepairCandidateHandoffAndRecovery": 83,
    "TestProtectedBaseRefreshReviewedRecoveryRejectsTamperedHistory": 68,
    "TestFenceRecoveredRunnersAcceptsRepeatedArmedPostPublicationCrashes": 62,
    "TestRunnerRecoveryAuthorityAuthenticatesControlGaps": 60,
    "TestPublicationEvidenceLifecycleReplayRecoveryAndBackup": 59,
    "TestRepositoryCommandResultAuthenticatedHistoricalLoadAndTampering": 56,
    "TestControlProofFencesEveryStoreAdmissionAtLinearization": 55,
    "TestFenceRecoveredRunnersAcceptsArmedPostPublicationRearm": 50,
    "TestProviderRetryWaitingApprovalRejectsTamperedAuthority": 49,
    "TestBeginProviderAttemptRejectsInvalidDirectLaunchInput": 43,
    "TestCompleteProtectedBaseRefreshRejectsUnreadyOrMismatchedEvidence": 42,
    "TestAuthenticatePostbuildFailureRefusals": 40,
    "TestProviderRetryWaitingApprovalRearmDecisionAndMergingRestarts": 39,
    "TestCandidateRepairCurrentReadersAndRearmRejectBrokenRecoveryPrefix": 38,
    "TestProtectedBaseRefreshReservationPreservesCompletedCIRepairParent": 34,
    "TestMergeObservationPrePublicationAllowlist": 34,
    "TestReviewBlockedRecoveryRearmsExactSealedEndpoint": 33,
    "TestSeededLeaseAdmissionStress": 32,
    "TestPostPublicationRearmProofAfterRestartAcrossStates": 31,
    "TestProviderRetryProtectedBaseRefreshPausedTakeoverRejectsUnboundEvidence": 31,
    "TestCIPollerAuthorityE2E": 31,
    "TestMergeObservationPrePublicationRejectsTamperedRecoveredStoppingCancellationLineage": 30,
    "TestProviderRetryProtectedBaseRefreshRejectsMalformedLineage": 28,
    "TestProviderRetryWorktreeProofReturnsSemanticHeadsAndReplays": 28,
    "TestTransitionGuardedMergeObservedRequiresSealedExactObservation": 28,
    "TestCurrentAttestedProviderPairChecksEveryRoleAndRestart": 28,
    "TestRepositoryCommandResourceRetirementRequiresExactDrainedLaunch": 27,
    "TestPostbuildRepairRecoveryRejectsTamperedBoundaryAndUnwitnessedGap": 26,
    "TestAuthoringNonSuccessCompletesWithEmptyBlobAndExactProof": 25,
    "TestCandidateRepairStartupRejectsTamperedConsumedRecoveryPrefix": 25,
    "TestProviderRetryWaitingApprovalRearmRejectsTamperedLiveProof": 25,
    "TestTransitionPostbuildRepairRejectsMalformedAndStaleRequests": 24,
    "TestPostbuildAmendmentMissingSchemaRejected": 24,
    "TestPostPublicationReconcilingResumeAuthenticatesBeforeCommit": 23,
    "TestMergeObservationPrePublicationRejectsTamperedResealedCancellationRecovery": 23,
    "TestActiveGitMutationLeasesQuarantinesInvalidRecoveryFacts": 22,
    "TestProtectedBaseRefreshAfterWaitingCIRestartAuthenticatesFreshBuilder": 21,
    "TestFinalReviewAuthorityRejectsBrokenPendingToGreenChain": 21,
    "TestFinalReviewTransitionsDeriveManualGuardedSpikeAndRejectAutonomous": 21,
    "TestCredentialBearingProviderQualificationRequiresExactCurrentSignature": 20,
}
RUNTIME_INTEGRATION_SECONDS = {
    # Inferred per-scenario hints divide the successful pre-split aggregate;
    # hosted timings should replace them after the first successful split run.
    "TestPostbuildAmendmentCandidateFinalizationRecovery": 134,
    "TestPostbuildAmendmentCandidateFinalizationRecoveryAfterRestart": 134,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecovery": 134,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecoveryAfterRestart": 134,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySameFence": 149,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoveryNewLeader": 149,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySyncedNewLeader": 149,
    "TestPostbuildRepairCandidateFinalizationRecovery": 336,
    "TestRepositoryMaterializerPostbuildAmendmentRealEndToEnd": 184,
    "TestRepositoryMaterializerPreparePostbuildRepairRealBoundary": 72,
    "TestRepositoryMaterializerRealSourceResumePreparedObservationLoss": 66,
    "TestRepositoryMaterializerPostbuildRepairRealEndToEnd": 62,
    "TestRepositoryMaterializerRealStoreGitReplay": 43,
}
CRASH_RUNTIME_SECONDS = {
    # Inferred per-scenario hints divide the successful pre-split aggregate;
    # hosted timings should replace them after the first successful split run.
    "TestPostbuildAmendmentCandidateFinalizationRecovery": 115,
    "TestPostbuildAmendmentCandidateFinalizationRecoveryAfterRestart": 115,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecovery": 115,
    "TestPostbuildAmendmentRecordedCandidateFinalizationRecoveryAfterRestart": 115,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySameFence": 164,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoveryNewLeader": 164,
    "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySyncedNewLeader": 164,
    "TestPostbuildRepairCandidateFinalizationRecovery": 314,
}
MODE_WEIGHTS = {
    "store": STORE_RACE_SECONDS,
    "runtime-race": RUNTIME_RACE_SECONDS,
    "runtime-integration": RUNTIME_INTEGRATION_SECONDS,
    "crash-runtime": CRASH_RUNTIME_SECONDS,
}
WEIGHT_PROVENANCE = {
    "other": "measured successful macos run 34786460096; package elapsed",
    "store": "measured successful macos run 34786460096; race top-level tests at least 20 seconds",
    "runtime-race": "measured successful macos run 34786460096; race top-level tests at least 20 seconds",
    "runtime-integration": "measured successful macos run 34771148550; normal top-level tests",
    "crash-runtime": "measured successful macos run 34783306067; normal crash top-level tests",
    "crash-other": "unweighted complete inventory; no inferred timings",
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
                   elapsed=None, test_timings=None, package_timings=None,
                   output_tail=""):
    """Write one bounded, machine-readable shard record without affecting truth."""
    if not directory:
        return
    path = Path(directory)
    path.mkdir(parents=True, exist_ok=True)
    record = {
        "schema": 1, "mode": mode, "shard": index, "shard_count": count,
        "inventory": sorted(names), "selected": sorted(selected),
        "weight_provenance": WEIGHT_PROVENANCE[mode],
        "exit_code": exit_code, "elapsed_seconds": elapsed,
        "test_timings": test_timings or {},
        "package_timings": package_timings or {},
        "output_tail": output_tail[-MAX_OUTPUT_BYTES:],
    }
    (path / f"{mode}-{index}.json").write_text(
        json.dumps(record, indent=2, sort_keys=True) + "\n")


def run_recorded(command, directory, mode, index, count, names, selected):
    started = time.monotonic()
    output = deque()
    output_bytes = 0
    test_timings = {}
    package_timings = {}
    # Go's event stream preserves package identity even when packages reuse a
    # test name. Only Output payloads are printed, keeping the familiar verbose
    # log; malformed/non-JSON compiler output is printed and retained verbatim.
    process = subprocess.Popen(command, stdout=subprocess.PIPE,
                               stderr=subprocess.STDOUT, text=True)
    assert process.stdout is not None
    try:
        for line in process.stdout:
            rendered = line
            try:
                event = json.loads(line)
            except (json.JSONDecodeError, TypeError):
                event = None
            if isinstance(event, dict):
                rendered = event.get("Output", "")
                package = event.get("Package")
                test = event.get("Test")
                action = event.get("Action")
                elapsed_value = event.get("Elapsed")
                if (package and test and "/" not in test and action in ("pass", "fail", "skip")
                        and isinstance(elapsed_value, (int, float))):
                    test_timings.setdefault(package, {})[test] = elapsed_value
                elif (package and not test and action in ("pass", "fail", "skip")
                      and isinstance(elapsed_value, (int, float))):
                    package_timings[package] = elapsed_value
            if rendered:
                print(rendered, end="" if rendered.endswith("\n") else "\n", flush=True)
            retained = rendered
            encoded = retained.encode("utf-8", errors="replace")
            if len(encoded) > MAX_OUTPUT_BYTES:
                retained = encoded[-MAX_OUTPUT_BYTES:].decode("utf-8", errors="ignore")
                encoded = retained.encode("utf-8", errors="replace")
            output.append(retained)
            output_bytes += len(encoded)
            while output and output_bytes > MAX_OUTPUT_BYTES:
                output_bytes -= len(output.popleft().encode("utf-8", errors="replace"))
        exit_code = process.wait()
    finally:
        elapsed = time.monotonic() - started
        write_artifact(directory, mode, index, count, names, selected,
                       process.poll(), elapsed, test_timings, package_timings,
                       "".join(output))
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
            command = ["go", "test", *FLAGS, "-json", *selected]
        else:
            names = [p for p in packages if p != RUNTIME]
            selected = names
            command = ["go", "test", *INTEGRATION_FLAGS, "-json", *selected,
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
        command = ["go", "test", *flags, "-json", package,
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
