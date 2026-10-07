#!/usr/bin/env python3
"""Run the registry gate on Linux with real host Docker and retained evidence."""
import argparse
import json
import os
from pathlib import Path
import shlex
import subprocess
import shutil
import tempfile
from urllib.parse import urlparse

ROOT = Path(__file__).resolve().parent.parent


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--candidate-version")
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    registry = json.loads((ROOT / "scripts/checks.json").read_text())
    image = "golang:" + registry["toolchain"]["go"]
    report = args.report or Path(os.environ.get("CONTEXTY_CHECK_REPORT_DIR", str(ROOT / ".check-results"))) / "linux.json"
    report = report.resolve()
    report.parent.mkdir(parents=True, exist_ok=True)
    staging = tempfile.TemporaryDirectory(prefix="contexty-linux-", dir="/private/tmp" if Path("/private/tmp").is_dir() else None)
    stage = Path(staging.name)
    source = stage / "source"
    evidence = stage / "evidence"
    evidence.mkdir()
    shutil.copytree(ROOT, source, ignore=shutil.ignore_patterns(".check-results", ".cache", ".peer", "__pycache__"))
    command = ["docker", "run", "--rm", "--network=host", "-v", str(source) + ":/source:ro",
               "-v", str(evidence) + ":/reports", "-v", "/var/run/docker.sock:/var/run/docker.sock",
               "-v", "contexty-check-go-cache:/cache", "-e", "GOCACHE=/cache/build", "-e", "GOPATH=/cache/gopath",
               "-e", "GOMODCACHE=" + ("/work/candidate-module-cache" if args.candidate_version else "/cache/modules"),
               "-e", "GOLANGCI_LINT_CACHE=/cache/linter", "-w", "/work"]
    if args.candidate_version:
        proxy = os.environ.get("GOPROXY", "")
        local = proxy.split(",", 1)[0]
        if not local.startswith("file://"):
            parser.error("candidate Linux check requires immutable file:// GOPROXY")
        directory = Path(urlparse(local).path)
        command += ["-v", str(directory) + ":/candidate-proxy:ro", "-e", "GOPROXY=file:///candidate-proxy,https://proxy.golang.org",
                    "-e", "GOSUMDB=sum.golang.org", "-e", "GONOSUMDB=" + os.environ.get("GONOSUMDB", "")]
    runner = ["python3", "scripts/check.py", "--profile", "check", "--report", "/reports/" + report.name]
    if args.candidate_version:
        runner += ["--candidate-version", args.candidate_version]
    command += [image, "sh", "-ec", "apt-get update && apt-get install -y --no-install-recommends python3 docker.io && "
                "cp -R /source/. /work/ && " + shlex.join(runner)]
    print("Linux profile image: " + image + "; report: " + str(report), flush=True)
    outcome = 1
    limitation = "container gate did not produce a report"
    try:
        outcome = subprocess.run(command, check=False).returncode
        saved = evidence / report.name
        if saved.exists():
            record = json.loads(saved.read_text())
            if not record.get("success") or not record.get("full_gate") or not record.get("platform", "").startswith("Linux"):
                outcome = outcome or 1
            if args.candidate_version and record.get("candidate_version") != args.candidate_version:
                outcome = 1
                limitation = "Linux report candidate version mismatch"
        else:
            outcome = outcome or 1
    except (OSError, ValueError) as error:
        limitation = str(error)
        print("BLOCKED: " + limitation)
        outcome = 1
    finally:
        saved = evidence / report.name
        if saved.exists():
            shutil.copyfile(saved, report)
        else:
            report.write_text(json.dumps({"schema": 1, "success": False, "full_gate": False,
                                          "results": [{"id": "linux-container", "status": "BLOCKED", "output": limitation}],
                                          "container_image": image}, indent=2) + "\n")
        staging.cleanup()
    return outcome


if __name__ == "__main__":
    raise SystemExit(main())
