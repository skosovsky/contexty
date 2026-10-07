#!/usr/bin/env python3
"""AAA gate negatives use real child process failures, never infrastructure substitutes."""
import importlib.util
import json
import os
from pathlib import Path
import tempfile
import subprocess
import sys
import unittest
from argparse import Namespace

spec = importlib.util.spec_from_file_location("gate", Path(__file__).with_name("check.py"))
gate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gate)


class GateTests(unittest.TestCase):
    def test_independent_failures_collected_and_dependents_blocked(self):
        # Arrange: four real command failures representing each mandatory class.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            registry = {"toolchain": {"go": "1.27.1"}}
            action = gate.Actions(root, registry, Namespace(candidate_version=None), dict(os.environ))
            lanes = [{"id": name, "selected": True, "command": ["python3", "-c", "raise SystemExit(7)"]}
                     for name in ["ordinary-test", "linter", "script-test", "consumer"]]
            lanes.append({"id": "publication", "selected": True, "needs": ["consumer"],
                          "command": ["python3", "-c", "raise SystemExit(0)"]})
            # Act.
            results, success = gate.execute(lanes, action)
            # Assert.
            self.assertFalse(success)
            self.assertEqual([r["status"] for r in results], ["FAIL"] * 4 + ["BLOCKED"])
            self.assertTrue(all(r["exit_code"] == 7 for r in results[:4]))

    def test_required_skip_does_not_pass(self):
        # Arrange.
        lanes = [{"id": "required", "selected": True}]
        # Act: a provider returning SKIP is never accepted as a required PASS.
        results, success = gate.execute(lanes, lambda _: ("SKIP", "missing prerequisite", {}))
        # Assert.
        self.assertFalse(success)
        self.assertEqual(results[0]["status"], "SKIP")

    def test_inventory_rejects_forgotten_nested_module(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "go.mod").write_text("module example.invalid/root\n\ngo 1.27.1\n")
            (root / "nested").mkdir()
            (root / "nested/go.mod").write_text("module example.invalid/nested\n\ngo 1.27.1\n")
            registry = {"modules": {"test": ["."], "release": ["."]}, "toolchain": {"go": "1.27.1"}}
            # Act / Assert.
            with self.assertRaisesRegex(ValueError, "inventory drift"):
                gate.inventory(root, registry)

    def test_missing_docker_is_blocked(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            action = gate.Actions(root, {}, Namespace(candidate_version=None), {"PATH": str(root)})
            # Act.
            status, output, _ = action({"kind": "docker", "id": "docker"})
            # Assert.
            self.assertEqual(status, "BLOCKED")
            self.assertIn("docker", output)

    def test_hidden_test_skip_fails(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            action = gate.Actions(Path(temporary), {}, Namespace(candidate_version=None), dict(os.environ))
            lane = {"id": "integration", "forbid_test_skip": True, "command": ["python3", "-c",
                    'print(\'{"Action":"run","Test":"Integration"}\'); print(\'{"Action":"skip","Test":"Integration"}\')']}
            # Act.
            status, output, _ = action(lane)
            # Assert.
            self.assertEqual(status, "FAIL")
            self.assertIn("skipped", output)

    def test_zero_matched_tests_fails(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            action = gate.Actions(Path(temporary), {}, Namespace(candidate_version=None), dict(os.environ))
            lane = {"id": "acceptance", "forbid_test_skip": True, "command": ["python3", "-c", 'print("no tests")']}
            # Act.
            status, output, _ = action(lane)
            # Assert.
            self.assertEqual(status, "FAIL")
            self.assertIn("zero tests", output)

    def test_cli_reports_each_real_failure_and_exits_nonzero(self):
        # Arrange: an isolated registry fixture drives real CLI dispatch branches.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "scripts").mkdir()
            (root / "bin").mkdir()
            (root / "bin/go").write_text("#!/bin/sh\necho injected pinned linter failure\nexit 13\n")
            (root / "bin/go").chmod(0o755)
            (root / "scripts/check_chat.py").write_text("raise SystemExit(14)\n")
            (root / "scripts/failing_test.py").write_text("raise SystemExit(12)\n")
            lanes = [
                {"id": "ordinary-test", "profiles": ["check"], "command": [sys.executable, "-c", "raise SystemExit(11)"]},
                {"id": "linter", "profiles": ["check"], "kind": "linter", "command": ["run"]},
                {"id": "script-test", "profiles": ["check"], "command": [sys.executable, "scripts/failing_test.py"]},
                {"id": "consumer", "profiles": ["check"], "kind": "chat", "mode": "source"},
                {"id": "publish", "profiles": ["check"], "needs": ["consumer"], "command": [sys.executable, "-c", "raise SystemExit(0)"]}]
            registry = {"module_path": "fixture.invalid/contexty", "lanes": lanes, "toolchain": {"go": "1.27.1", "linter_package": "fixture/linter", "linter": "v2.14.0"}, "peers": {}}
            (root / "registry.json").write_text(json.dumps(registry))
            report = root / "report.json"
            code = "import sys; sys.path.insert(0, " + repr(str(Path(__file__).parent)) + "); import check; check.ROOT=" + repr(str(root)) + "; from pathlib import Path; check.ROOT=Path(check.ROOT); check.REGISTRY=check.ROOT/'registry.json'; raise SystemExit(check.main())"
            env = dict(os.environ, PATH=str(root / "bin") + os.pathsep + os.environ["PATH"])
            # Act.
            result = subprocess.run([sys.executable, "-c", code, "--report", str(report)], env=env, text=True, capture_output=True, check=False)
            # Assert: persisted summary, exit, and actual per-kind return codes agree.
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            evidence = json.loads(report.read_text())
            self.assertFalse(evidence["success"])
            self.assertEqual([lane["status"] for lane in evidence["results"]], ["FAIL"] * 4 + ["BLOCKED"])
            self.assertEqual([lane["exit_code"] for lane in evidence["results"][:4]], [11, 13, 12, 14])

    def test_missing_toolchain_is_blocked(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            action = gate.Actions(Path(temporary), {"toolchain": {"go": "1.27.1"}}, Namespace(candidate_version=None), {"PATH": temporary})
            # Act.
            status, output, _ = action({"kind": "toolchain", "id": "toolchain"})
            # Assert.
            self.assertEqual(status, "BLOCKED")
            self.assertIn("go", output)

    def test_formatter_diff_is_failure_even_with_zero_exit(self):
        # Arrange: formatter CLI emits a diff while returning success.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "go"
            binary.write_text("#!/bin/sh\necho 'diff --git a/file.go b/file.go'\nexit 0\n")
            binary.chmod(0o755)
            action = gate.Actions(root, {"toolchain": {"linter_package": "fixture/linter", "linter": "v2.14.0"}}, Namespace(candidate_version=None), {"PATH": temporary})
            # Act.
            status, output, evidence = action({"kind": "linter", "id": "format", "command": ["fmt", "--diff"]})
            # Assert.
            self.assertEqual(evidence["exit_code"], 0)
            self.assertEqual(status, "FAIL")
            self.assertIn("diff", output)

    def test_incompatible_toolchain_is_blocked(self):
        # Arrange.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "go"
            binary.write_text("#!/bin/sh\necho 'go version go1.26.0 linux/amd64'\n")
            binary.chmod(0o755)
            action = gate.Actions(root, {"toolchain": {"go": "1.27.1"}}, Namespace(candidate_version=None), {"PATH": temporary})
            # Act.
            status, output, _ = action({"kind": "toolchain", "id": "toolchain"})
            # Assert.
            self.assertEqual(status, "BLOCKED")
            self.assertIn("go1.26.0", output)

    def test_successful_child_source_mutation_blocks_gate_and_publication(self):
        # Arrange: the child exits zero after changing the bytes under test.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            (root / "victim.go").write_text("package original\n")
            published = root / "published-ref"
            lanes = [
                {"id": "ordinary-test", "profiles": ["check"], "command": [sys.executable, "-c", "from pathlib import Path; Path('victim.go').write_text('package changed\\n')"]},
                {"id": "publication", "profiles": ["check"], "needs": ["ordinary-test"], "command": [sys.executable, "-c", "from pathlib import Path; Path(" + repr(str(published)) + ").write_text('published')"]}]
            (root / "registry.json").write_text(json.dumps({"module_path": "fixture.invalid/contexty", "lanes": lanes, "toolchain": {"go": "1.27.1"}, "peers": {}}))
            report = root / "report.json"
            code = "import sys; sys.path.insert(0, " + repr(str(Path(__file__).parent)) + "); import check; from pathlib import Path; check.ROOT=Path(" + repr(str(root)) + "); check.REGISTRY=check.ROOT/'registry.json'; raise SystemExit(check.main())"
            # Act.
            result = subprocess.run([sys.executable, "-c", code, "--report", str(report)], env=dict(os.environ, PYTHONDONTWRITEBYTECODE="1"), text=True, capture_output=True, check=False)
            # Assert: a zero child exit cannot certify different source bytes.
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            evidence = json.loads(report.read_text())
            self.assertFalse(evidence["success"])
            self.assertFalse(evidence["full_gate"])
            self.assertEqual(evidence["results"][0]["exit_code"], 0)
            self.assertEqual(evidence["results"][0]["status"], "FAIL")
            self.assertEqual(evidence["results"][0]["source_mutations"], ["victim.go"])
            self.assertEqual(evidence["results"][1]["status"], "BLOCKED")
            self.assertFalse(published.exists())

    def test_hostile_environment_cannot_disable_checksums_or_select_source_peers(self):
        # Arrange.
        registry = {"module_path": "github.com/skosovsky/contexty", "toolchain": {"go": "1.27.1"}}
        hostile = {"GOSUMDB": "off", "GONOSUMDB": "*", "GOPRIVATE": "*", "GONOPROXY": "*", "GOPROXY": "https://hostile.invalid,direct", "GOFLAGS": "-mod=mod", "GOENV": "/tmp/hostile-config"}
        # Act.
        env = gate.check_environment(registry, inherited=hostile)
        # Assert.
        self.assertEqual(env["GOSUMDB"], "sum.golang.org")
        self.assertEqual(env["GONOSUMDB"], "none")
        self.assertEqual(env["GOPRIVATE"], "none")
        self.assertEqual(env["GONOPROXY"], "none")
        self.assertEqual(env["GOPROXY"], "https://proxy.golang.org")
        self.assertEqual(env["GOENV"], "off")
        self.assertEqual(env["GOFLAGS"], "")

    def test_candidate_environment_only_bypasses_checksums_for_own_namespace(self):
        # Arrange.
        registry = {"module_path": "github.com/skosovsky/contexty", "toolchain": {"go": "1.27.1"}}
        hostile = {"GOSUMDB": "off", "GONOSUMDB": "*", "GOPRIVATE": "*", "GOPROXY": "file:///tmp/immutable-artifact,direct"}
        # Act.
        env = gate.check_environment(registry, "v0.12.3", hostile)
        # Assert.
        self.assertEqual(env["GOSUMDB"], "sum.golang.org")
        self.assertEqual(env["GONOSUMDB"], "github.com/skosovsky/contexty,github.com/skosovsky/contexty/*")
        self.assertEqual(env["GOPROXY"], "file:///tmp/immutable-artifact,https://proxy.golang.org")
        self.assertEqual(env["GONOPROXY"], "none")
        self.assertNotIn("CONTEXTY_CANDIDATE_ENV_ERROR", env)

    def test_isolated_setup_restored_before_published_consumer_and_artifacts(self):
        # Arrange: actual fixture Go CLI mutates module metadata during owned setup.
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            module = root / "integration/chat"
            module.mkdir(parents=True)
            original_mod = b"module fixture.invalid/chat\n\ngo 1.27.1\n"
            original_sum = b"original checksums\n"
            (module / "go.mod").write_bytes(original_mod)
            (module / "go.sum").write_bytes(original_sum)
            binary = root / "go"
            binary.write_text("#!" + sys.executable + "\nimport sys\nfrom pathlib import Path\nif sys.argv[1:3]==['mod','edit']:\n Path('go.mod').write_text('module fixture.invalid/chat\\nreplace fixture.invalid/core => /source\\n')\nif sys.argv[1:3]==['mod','tidy']:\n Path('go.sum').write_text('temporary checksums\\n')\n")
            binary.chmod(0o755)
            registry = {"module_path": "fixture.invalid/core", "toolchain": {"linter_package": "fixture/linter", "linter": "v2.14.0"}}
            action = gate.Actions(root, registry, Namespace(candidate_version=None), dict(os.environ, PATH=temporary + os.pathsep + os.environ['PATH']))
            isolated = {"id": "chat-lint", "selected": True, "kind": "linter", "module": "integration/chat", "isolated": True, "command": ["run"]}
            verify = "from pathlib import Path; mod=Path('integration/chat/go.mod').read_text(); assert 'replace' not in mod; print(mod)"
            lanes = [isolated, {"id": "published-consumer-input", "selected": True, "command": [sys.executable, "-c", verify]},
                     {"id": "artifact-metadata-input", "selected": True, "command": [sys.executable, "-c", verify]}]
            # Act.
            results, success = gate.execute(lanes, action)
            # Assert: each independent input is the original module metadata.
            self.assertTrue(success, results)
            self.assertEqual([r['status'] for r in results], ['PASS'] * 3)
            self.assertEqual((module / 'go.mod').read_bytes(), original_mod)
            self.assertEqual((module / 'go.sum').read_bytes(), original_sum)
            self.assertEqual(results[1]['output'].strip(), original_mod.decode().strip())
            self.assertEqual(results[2]['output'].strip(), original_mod.decode().strip())

    def test_shard_dependency_closure(self):
        # Arrange.
        registry = {"lanes": [{"id": "peer", "profiles": ["check"]},
                              {"id": "consumer", "profiles": ["check"], "needs": ["peer"]}]}
        # Act.
        lanes = gate.plan(registry, "check", ["consumer"])
        # Assert.
        self.assertTrue(all(lane["selected"] for lane in lanes))


if __name__ == "__main__":
    unittest.main()
