"""Verify frozen source identity and final gate logs without recursive hashes."""
import hashlib
import json
import pathlib

EVIDENCE = pathlib.Path(__file__).resolve().parent
ROOT = EVIDENCE.parent.parent
identity = json.loads((EVIDENCE / "identity.json").read_text())
manifest_bytes = (EVIDENCE / "source-files.json").read_bytes()
assert hashlib.sha256(manifest_bytes).hexdigest() == identity["source_tree_sha256"]
manifest = json.loads(manifest_bytes)
for checkout in (ROOT, pathlib.Path(identity["frozen_checkout"])):
    for name, expected in manifest.items():
        actual = hashlib.sha256((checkout / name).read_bytes()).hexdigest()
        assert actual == expected, (checkout, name, actual, expected)
assert hashlib.sha256((EVIDENCE / "candidate.diff").read_bytes()).hexdigest() == identity["diff_sha256"]
for name in ("baseline-bench.log", "candidate-bench.log", "candidate-regression.log",
             "baseline-compatibility.log", "root-race.log", "root-lint.log", "format.log"):
    log = (EVIDENCE / name).read_text()
    assert log.rstrip().endswith("AUDIT_LOCK_RELEASED 0"), name
assert (EVIDENCE / "baseline-regression.log").read_text().rstrip().endswith("AUDIT_LOCK_RELEASED 1")
assert "FAIL: TestSelectionArtifactCloneBound/N300/A300/matchedfalse" in (
    EVIDENCE / "baseline-regression.log").read_text()
env = json.loads((EVIDENCE / "environment.json").read_text())
assert env["baseline"] == env["candidate"]
assert env["benchmark_test_sha256"] == manifest["selection_artifact_performance_test.go"]
print(f'PASS: {len(manifest)} source files match frozen tree {identity["source_tree_sha256"]}')
print("PASS: final gate exit codes, expected red baseline and toolchain identity verified")
