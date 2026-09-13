import importlib.util
import io
import itertools
import json
from pathlib import Path
import re
import subprocess
import tempfile
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("ci_race", Path(__file__).with_name("ci-race.py"))
ci = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ci)


class RacePartitionTest(unittest.TestCase):
    def test_workflow_runs_every_acceptance_lane_and_shard(self):
        root = Path(__file__).resolve().parent.parent
        workflow = (root / ".github/workflows/repository-baseline.yml").read_text()
        targets = re.search(r"target: \[([^]]+)\]", workflow).group(1).split(", ")
        self.assertEqual(set(targets), {"test-integration-other", "crash-other",
                                      "test-security", "test-upgrade", "test-compiled-e2e", "verify-static"})
        shards = re.search(r"shard: \[([^]]+)\]", workflow).group(1).split(", ")
        self.assertEqual([int(i) for i in shards], list(range(8)))
        self.assertIn('--count 8', (root / "Makefile").read_text())
        runtime = workflow.split("  runtime-integration:\n", 1)[1]
        runtime_shards = re.search(r"shard: \[([^]]+)\]", runtime).group(1).split(", ")
        self.assertEqual([int(i) for i in runtime_shards], list(range(4)))
        balanced = workflow.split("  balanced:\n", 1)[1].split("  runtime-integration:\n", 1)[0]
        self.assertIn("lane: [race-other, runtime-race, crash-runtime]", balanced)
        self.assertIn("shard: [0, 1, 2, 3]", balanced)
        self.assertIn("include:\n          - lane: race-other\n            shard: 4", balanced)
        lanes = re.search(r"lane: \[([^]]+)\]", balanced).group(1).split(", ")
        balanced_shards = [int(value) for value in
                           re.search(r"shard: \[([^]]+)\]", balanced).group(1).split(", ")]
        jobs = {(lane, shard) for lane, shard in itertools.product(lanes, balanced_shards)}
        included = [(lane, int(shard)) for lane, shard in
                    re.findall(r"- lane: ([a-z-]+)\n\s+shard: (\d+)", balanced)]
        self.assertEqual(included, [("race-other", 4)])
        self.assertNotIn("exclude:", balanced)
        jobs.update(included)
        expected_jobs = ({("race-other", shard) for shard in range(5)} |
                         {(lane, shard) for lane in ("runtime-race", "crash-runtime")
                          for shard in range(4)})
        self.assertEqual(jobs, expected_jobs)
        self.assertEqual(len(jobs), 13)
        makefile = (root / "Makefile").read_text()
        self.assertIn('runtime-race) python3 scripts/run-bounded --timeout 65m -- python3 scripts/ci-race.py runtime-race', makefile)
        self.assertIn('runtime-integration --index "$$SHARD" --count 4', makefile)
        self.assertIn('other --index "$$SHARD" --count 5', makefile)
        for mode in ("runtime-race", "crash-runtime"):
            self.assertIn(f'{mode} --index "$$SHARD" --count 4', makefile)
        reference = re.search(r"\ntest-integration:\n\t([^\n]+)", makefile).group(1).split()
        other = re.search(r"\ntest-integration-other:\n\t([^\n]+)", makefile).group(1).split()
        self.assertEqual([part for part in reference if part != "./internal/workflowruntime"], other)
        self.assertNotIn("continue-on-error", workflow)
        self.assertIn("github.event.pull_request.number || github.run_id", workflow)
        self.assertIn("cancel-in-progress: ${{ github.event_name == 'pull_request' }}", workflow)
        self.assertEqual(workflow.count("uses: actions/upload-artifact@v4"), 4)
        self.assertEqual(workflow.count("include-hidden-files: true"), 4)
        self.assertEqual(workflow.count("python3 -B -m unittest discover -s scripts -p 'test_ci_race.py'"), 1)
        for job in ("baseline", "store-race", "balanced", "runtime-integration"):
            body = workflow.split(f"  {job}:\n", 1)[1]
            body = re.split(r"\n  [a-z-]+:\n", body, maxsplit=1)[0]
            self.assertIn("SF_CI_ARTIFACT_DIR: .ci-timings", body)
            self.assertIn("uses: actions/upload-artifact@v4", body)
            self.assertIn("needs: preflight", body)

    def test_recovery_candidate_lanes_select_each_split_scenario_once(self):
        root = Path(__file__).resolve().parent.parent
        workflow = (root / ".github/workflows/recovery-candidate.yml").read_text()
        runtime_source = (root / "internal/workflowruntime/protected_amendment_recovery_test.go").read_text()
        candidate_source = (root / "internal/workflowruntime/postbuild_candidate_recovery_test.go").read_text()
        expected = {
            "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySameFence",
            "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoveryNewLeader",
            "TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecoverySyncedNewLeader",
            "TestPostbuildAmendmentCandidateFinalizationRecovery",
            "TestPostbuildAmendmentCandidateFinalizationRecoveryAfterRestart",
            "TestPostbuildAmendmentRecordedCandidateFinalizationRecovery",
            "TestPostbuildAmendmentRecordedCandidateFinalizationRecoveryAfterRestart",
        }
        declared = set(re.findall(r"func (Test\w+)\(t \*testing\.T\)",
                                  runtime_source + candidate_source))
        self.assertLessEqual(expected, declared)
        for name in expected:
            self.assertEqual(workflow.count("selector='" + name + "'"), 1)
        self.assertNotIn("TestRepositoryMaterializerPostbuildAmendmentPreparedIndexRecovery$/", workflow)
        self.assertNotIn("TestPostbuildAmendmentCandidateFinalizationRecovery$/", workflow)
        # The repair matrix was not split and must retain its subtest selector.
        self.assertIn("TestPostbuildRepairCandidateFinalizationRecovery$/$selector", workflow)

    def test_compiled_node_uses_supported_bounded_homebrew_runtime(self):
        workflow = (Path(__file__).resolve().parent.parent /
                    ".github/workflows/repository-baseline.yml").read_text()
        self.assertIn("brew install node@22", workflow)
        self.assertIn('setup_node="$(brew --prefix node@22)/bin/node"', workflow)
        self.assertIn('sudo ln -sfn "$setup_node" /opt/homebrew/bin/node', workflow)
        self.assertNotIn("uses: actions/setup-node", workflow)

    def test_required_acceptance_gate_is_fail_closed(self):
        workflow = (Path(__file__).resolve().parent.parent /
                    ".github/workflows/repository-baseline.yml").read_text()
        gate = workflow.split("  acceptance:\n", 1)[1].split("  baseline:\n", 1)[0]
        self.assertIn("name: SF acceptance", gate)
        self.assertIn("if: ${{ always() }}", gate)
        self.assertIn("needs: [preflight, baseline, store-race, runtime-integration, balanced]", gate)
        self.assertIn("${{ needs.preflight.result }}", gate)
        self.assertIn("${{ needs.baseline.result }}", gate)
        self.assertIn("${{ needs.store-race.result }}", gate)
        self.assertIn("${{ needs.runtime-integration.result }}", gate)
        self.assertIn('test "$BASELINE_RESULT" = success', gate)
        self.assertIn('test "$STORE_RACE_RESULT" = success', gate)
        self.assertIn('test "$RUNTIME_INTEGRATION_RESULT" = success', gate)
        self.assertIn("${{ needs.balanced.result }}", gate)
        self.assertIn('test "$BALANCED_RESULT" = success', gate)
        commands = gate.split("        run: |\n", 1)[1]
        self.assertIn('test "$PREFLIGHT_RESULT" = success', gate)
        result_names = ("PREFLIGHT_RESULT", "BASELINE_RESULT", "STORE_RACE_RESULT",
                        "RUNTIME_INTEGRATION_RESULT", "BALANCED_RESULT")
        # Preserve exhaustive combinations, extending the existing four-job
        # matrix to include preflight rather than dropping multi-failure cases.
        for outcomes in itertools.product(("success", "failure", "cancelled", "skipped", ""), repeat=5):
            with self.subTest(outcomes=outcomes):
                env = dict(zip(result_names, outcomes))
                result = subprocess.run(
                    ["bash", "--noprofile", "--norc", "-e", "-c", commands],
                    env=env, capture_output=True, timeout=5)
                self.assertEqual(result.returncode == 0,
                                 all(outcome == "success" for outcome in outcomes))

    def test_complete_disjoint_stable_partition(self):
        names = [f"TestCase{i}" for i in range(541)] + ["ExampleStore", "FuzzDecode"]
        shards = [ci.partition(names, i, 8) for i in range(8)]
        flat = [name for shard in shards for name in shard]
        self.assertEqual(sorted(names), sorted(flat))
        self.assertEqual(len(flat), len(set(flat)))
        self.assertEqual(shards[2], ci.partition(list(reversed(names)), 2, 8))

    def test_invalid_inventory_or_shard_refused(self):
        for names, index, count in [([], 0, 8), (["TestA"] * 2, 0, 1),
                                    (["TestA"], 1, 8), (["TestA"], -1, 8),
                                    (["TestA"], 0, 0), (["TestA"], 8, 8)]:
            with self.assertRaises(ValueError):
                ci.partition(names, index, count)
        with self.assertRaises(ValueError):
            ci.inventory("unexpected fixture output")

    def test_weighted_partition_is_complete_disjoint_and_balanced(self):
        names = ["heavy", "medium", "new", "ExampleSeed", "FuzzSeed", "old"]
        weights = {"heavy": 100, "medium": 60, "old": 40, "removed": 999}
        shards = [ci.balanced_partition(names, i, 3, weights) for i in range(3)]
        flat = [n for shard in shards for n in shard]
        self.assertEqual(sorted(flat), sorted(names))
        self.assertEqual(len(flat), len(set(flat)))
        self.assertEqual(shards, [ci.balanced_partition(list(reversed(names)), i, 3, weights)
                                 for i in range(3)])
        self.assertEqual(shards[0], ["heavy"])
        for weight in (0, -1, float("inf"), float("nan"), "bad"):
            with self.assertRaises(ValueError):
                ci.balanced_partition(names, 0, 3, {"heavy": weight})

    def test_each_test_mode_has_independent_scheduling_hints(self):
        self.assertEqual(set(ci.MODE_WEIGHTS), {"store", "runtime-race",
                                                "runtime-integration", "crash-runtime"})
        self.assertIsNot(ci.MODE_WEIGHTS["runtime-race"],
                         ci.MODE_WEIGHTS["runtime-integration"])
        names = ["known", "new-a", "new-b"]
        for weights in ci.MODE_WEIGHTS.values():
            shards = [ci.balanced_partition(names, i, 2, weights) for i in range(2)]
            self.assertEqual(sorted(sum(shards, [])), sorted(names))

    def test_inventory_keeps_examples_and_fuzz_seeds(self):
        self.assertEqual(ci.inventory("TestA\nExampleB\nFuzzC\nBenchmarkD\nok  \tpackage 1s\n"),
                         ["TestA", "ExampleB", "FuzzC"])

    def test_store_failure_is_propagated_and_pattern_is_exact(self):
        with patch("sys.argv", ["ci-race.py", "store", "--count", "1"]), \
             patch.object(ci.subprocess, "check_output", return_value="TestA\nTestAB\n"), \
             patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
            self.assertEqual(ci.main(), 1)
            self.assertEqual(run.call_args.args[0][-1], "^(?:TestA|TestAB)$")
            self.assertIn("-race", run.call_args.args[0])

    def test_other_runs_every_package_outside_store_and_runtime(self):
        with patch("sys.argv", ["ci-race.py", "other"]), \
             patch.object(ci.subprocess, "check_output", return_value=f"first\n{ci.STORE}\n{ci.RUNTIME}\nlast\n"), \
             patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 0)) as run:
            self.assertEqual(ci.main(), 0)
            self.assertEqual(run.call_args.args[0][-2:], ["first", "last"])
            self.assertNotIn(ci.STORE, run.call_args.args[0])
            self.assertNotIn(ci.RUNTIME, run.call_args.args[0])

    def test_runtime_race_partitions_inventory_with_unchanged_flags(self):
        with patch("sys.argv", ["ci-race.py", "runtime-race"]), \
             patch.object(ci.subprocess, "check_output", return_value="TestA\nExampleB\nFuzzC\n") as listing, \
             patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
            self.assertEqual(ci.main(), 1)
            self.assertEqual(listing.call_args.args[0], ["go", "test", "-race", "-list", ".", ci.RUNTIME])
            self.assertEqual(run.call_args.args[0], ["go", "test", *ci.FLAGS, "-json", ci.RUNTIME,
                                                   "-run", "^(?:ExampleB|FuzzC|TestA)$"])

    def test_other_package_shards_keep_new_packages_and_propagate_failure(self):
        packages = ["first", ci.STORE, ci.RUNTIME, "last", "new", "fourth"]
        commands = []
        for index in range(4):
            with patch("sys.argv", ["ci-race.py", "other", "--index", str(index), "--count", "4"]), \
                 patch.object(ci.subprocess, "check_output", return_value="\n".join(packages)), \
                 patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
                self.assertEqual(ci.main(), 1)
                commands.extend(run.call_args.args[0][3 + len(ci.FLAGS):])
        self.assertEqual(sorted(commands), ["first", "fourth", "last", "new"])

    def test_crash_partition_keeps_exact_original_selection(self):
        makefile = (Path(__file__).resolve().parent.parent / "Makefile").read_text()
        original = re.search(r"\ntest-crash:\n\t[^\n]*-run '([^']+)'", makefile).group(1)
        self.assertEqual(ci.CRASH_PATTERN, original)
        names = ["TestCrashA", "TestRecovery", "TestRecover", "TestRearm", "TestQuarantine",
                 "TestOrdinary", "ExampleOther", "FuzzDecode"]
        selected = []
        for index in range(4):
            with patch("sys.argv", ["ci-race.py", "crash-runtime", "--index", str(index), "--count", "4"]), \
                 patch.object(ci.subprocess, "check_output", return_value="\n".join(names)), \
                 patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
                self.assertEqual(ci.main(), 1)
                command = run.call_args.args[0]
                self.assertNotIn("-race", command)
                selected.extend(n for n in names if re.search(command[-1], n))
        self.assertEqual(sorted(selected), sorted(n for n in names if re.search(original, n)))
        self.assertEqual(len(selected), len(set(selected)))
        with patch("sys.argv", ["ci-race.py", "crash-other"]), \
             patch.object(ci.subprocess, "check_output", return_value=f"first\n{ci.STORE}\n{ci.RUNTIME}\n"), \
             patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
            self.assertEqual(ci.main(), 1)
            self.assertEqual(run.call_args.args[0], ["go", "test", *ci.INTEGRATION_FLAGS,
                                                   "-json", "first", ci.STORE, "-run", ci.CRASH_PATTERN])

    def test_race_package_lanes_are_complete_and_disjoint(self):
        packages = ["first", ci.STORE, ci.RUNTIME, "last"]
        other = ci.race_packages(packages, "other")
        runtime = ci.race_packages(packages, "runtime-race")
        self.assertEqual(set(other + runtime + [ci.STORE]), set(packages))
        self.assertEqual(len(other + runtime + [ci.STORE]), len(packages))
        self.assertEqual(runtime, [ci.RUNTIME])

    def test_race_package_lanes_refuse_malformed_inventory(self):
        for packages in ([], ["first", ci.STORE], ["first", ci.RUNTIME],
                         ["first", ci.STORE, ci.RUNTIME, ci.STORE],
                         ["first", ci.STORE, ci.RUNTIME, ci.RUNTIME],
                         ["first", "first", ci.STORE, ci.RUNTIME],
                         ["", ci.STORE, ci.RUNTIME],
                         [" bad", ci.STORE, ci.RUNTIME],
                         ["bad package", ci.STORE, ci.RUNTIME]):
            for mode in ("other", "runtime-race"):
                with self.subTest(packages=packages, mode=mode), self.assertRaises(ValueError):
                    ci.race_packages(packages, mode)
        with self.assertRaises(ValueError):
            ci.race_packages([ci.STORE, ci.RUNTIME], "other")

    def test_runtime_integration_keeps_normal_flags_and_all_seed_kinds(self):
        with patch("sys.argv", ["ci-race.py", "runtime-integration", "--count", "1"]), \
             patch.object(ci.subprocess, "check_output", return_value="TestA\nExampleB\nFuzzC\n") as listing, \
             patch.object(ci.subprocess, "Popen", return_value=FakeProcess([], 1)) as run:
            self.assertEqual(ci.main(), 1)
            self.assertEqual(listing.call_args.args[0], ["go", "test", "-list", ".", ci.RUNTIME])
            command = run.call_args.args[0]
            self.assertIn(ci.RUNTIME, command)
            self.assertIn("30m", command)
            self.assertNotIn("-race", command)
            self.assertEqual(command[-1], "^(?:ExampleB|FuzzC|TestA)$")

    def test_json_stream_records_package_scoped_timings_and_human_output(self):
        lines = [
            json.dumps({"Action": "output", "Package": "one", "Test": "TestSame",
                        "Output": "=== RUN   TestSame\n"}) + "\n",
            json.dumps({"Action": "pass", "Package": "one", "Test": "TestSame",
                        "Elapsed": 1.25, "Output": "--- PASS: TestSame (1.25s)\n"}) + "\n",
            json.dumps({"Action": "pass", "Package": "two", "Test": "TestSame",
                        "Elapsed": 2.5, "Output": "--- PASS: TestSame (2.50s)\n"}) + "\n",
            json.dumps({"Action": "pass", "Package": "one", "Elapsed": 1.5}) + "\n",
        ]
        process = FakeProcess(lines, 0)
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(ci.subprocess, "Popen", return_value=process), \
             redirect_stdout(io.StringIO()) as stdout:
            self.assertEqual(ci.run_recorded(["go"], directory, "other", 0, 1,
                                             ["one", "two"], ["one", "two"]), 0)
            record = json.loads((Path(directory) / "other-0.json").read_text())
        self.assertIn("--- PASS: TestSame", stdout.getvalue())
        self.assertEqual(record["test_timings"]["one"]["TestSame"], 1.25)
        self.assertEqual(record["test_timings"]["two"]["TestSame"], 2.5)
        self.assertEqual(record["package_timings"], {"one": 1.5})
        self.assertEqual(record["exit_code"], 0)
        self.assertIn("measured successful", record["weight_provenance"])

    def test_json_stream_preserves_nonzero_exit_and_malformed_failure_output(self):
        failure = "compiler: malformed output must survive\n"
        process = FakeProcess([failure], 2)
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(ci.subprocess, "Popen", return_value=process), \
             redirect_stdout(io.StringIO()) as stdout:
            self.assertEqual(ci.run_recorded(["go"], directory, "store", 0, 1,
                                             ["TestA"], ["TestA"]), 2)
            record = json.loads((Path(directory) / "store-0.json").read_text())
        self.assertIn(failure.strip(), stdout.getvalue())
        self.assertIn(failure.strip(), record["output_tail"])
        self.assertEqual(record["exit_code"], 2)

    def test_json_stream_records_signal_cancellation_exit(self):
        process = FakeProcess([], -15)
        with tempfile.TemporaryDirectory() as directory, \
             patch.object(ci.subprocess, "Popen", return_value=process):
            self.assertEqual(ci.run_recorded(["go"], directory, "store", 0, 1,
                                             ["TestA"], ["TestA"]), -15)
            record = json.loads((Path(directory) / "store-0.json").read_text())
        self.assertEqual(record["exit_code"], -15)

    def test_json_stream_without_artifact_still_renders_output_and_exit(self):
        line = json.dumps({"Action": "output", "Package": "one",
                           "Output": "human failure detail\n"}) + "\n"
        process = FakeProcess([line], 3)
        with patch.object(ci.subprocess, "Popen", return_value=process), \
             redirect_stdout(io.StringIO()) as stdout:
            self.assertEqual(ci.run_recorded(["go"], "", "crash-other", 0, 1,
                                             ["one"], ["one"]), 3)
        self.assertEqual(stdout.getvalue(), "human failure detail\n")


class FakeProcess:
    def __init__(self, lines, returncode):
        self.stdout = iter(lines)
        self.returncode = returncode

    def wait(self):
        return self.returncode

    def poll(self):
        return self.returncode


if __name__ == "__main__":
    unittest.main()
