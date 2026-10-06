#!/usr/bin/env python3
"""Verify preserved behavioral probes against immutable review SHA in temp files."""
import os
import json
from pathlib import Path
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parent.parent
SHA = "2a1a7ab7fb919c436e210c2804dd615e2d80626a"
ASSERTIONS = {
    "identity": ["third after trimming oldest contexty: compile request: contexty: duplicate message id", "different images same generated ID true"],
    "compile": ['segment="memroy" policy="append" err=<nil> calls=1', 'policy="replcae" err=<nil> calls=1'],
    "materialization": ["pointer=false err=contexty: invalid artifact materialization", "pointer=true err=<nil>"],
    "views": ['custom_called=false out="user: ORIGINAL\\n" err=<nil>'],
    "budget": ['caller="original-b" output="mutated"', "fixed total=2 err=<nil>", "fixed admitted=2", "char 2-runes/max=-1 err=<nil>"],
    "overhead": ["full_input_cost=12 limit=12 err=<nil> kept_history=0"],
    "arch": ['provenance: err=<nil> encoding_error=<nil> reencoded={"type_id":"system"'],
}


def main():
    with tempfile.TemporaryDirectory(prefix="contexty-review-baseline-") as directory:
        base = Path(directory).resolve()
        checkout = base / "library"
        checkout.mkdir()
        probes = base / "probes"
        probes.mkdir()
        archive = base / "snapshot.tar"
        with archive.open("wb") as output:
            subprocess.run(["git", "archive", SHA], cwd=ROOT, stdout=output, check=True)
        subprocess.run(["tar", "xf", str(archive), "-C", str(checkout)], check=True)
        env = dict(os.environ, GOWORK="off", GOCACHE="/private/tmp/contexty-go-cache")
        for name, markers in ASSERTIONS.items():
            probe = probes / (name + ".go")
            probe.write_bytes((ROOT / "docs/remediation-evidence/baseline/repro" / (name + ".go.txt")).read_bytes())
            result = subprocess.run(["go", "run", str(probe)], cwd=checkout, env=env,
                                    text=True, capture_output=True, check=True)
            print(f"=== {name} @ {SHA} ===\n{result.stdout}", flush=True)
            for marker in markers:
                if marker not in result.stdout:
                    raise AssertionError(f"missing behavioral evidence {marker!r}")

        for name, module, test, marker in [
            ("cancel", ".", "TestReviewClearCanceledWhileWaiting", "published version=1 after ctx cancellation"),
            ("ttl", "adapters/store/redis", "TestReviewSubMillisecondTTL", "reached Lua as 0 ms; PEXPIRE branch is skipped"),
        ]:
            probe = probes / (name + "_test.go")
            probe.write_bytes((ROOT / "docs/remediation-evidence/baseline/repro" / (name + ".go.txt")).read_bytes())
            module_root = checkout / module
            overlay = probes / (name + "_overlay.json")
            overlay_target = module_root / "zz_review_probe_test.go"
            overlay_target.write_text("// Temporary overlay discovery placeholder.\npackage " + ("contexty" if module == "." else "redis") + "\n")
            overlay.write_text(json.dumps({"Replace": {str(overlay_target): str(probe)}}))
            result = subprocess.run(["go", "test", "-race", "-v", "-count=10", "-overlay", str(overlay), "-run", "^" + test + "$", "."],
                                    cwd=module_root, env=env, text=True, capture_output=True, check=True)
            print(f"=== {name} overlay @ {SHA} ===\n{result.stdout}", flush=True)
            if marker not in result.stdout:
                raise AssertionError(f"missing behavioral evidence {marker!r}")
        print("Baseline F03-F12 behavioral assertions PASS", flush=True)


if __name__ == "__main__":
    main()
