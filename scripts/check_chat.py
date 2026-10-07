#!/usr/bin/env python3
"""Verify isolated consumer semantics against source, candidate or public bytes."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
CORE = "github.com/skosovsky/contexty"
PEER = "github.com/skosovsky/prompty"
REGISTRY = json.loads((ROOT / "scripts/checks.json").read_text())
PEER_VERSION = REGISTRY["peers"][PEER]
PEER_SHA = REGISTRY["peers"][PEER + "_sha"]
BASELINE = REGISTRY["peers"]["baseline"]


def transient_download_failure(message):
    message = message.lower()
    if any(marker in message for marker in ("checksum mismatch", "security error", "verifying module: checksum", "invalid version", "unknown revision")):
        return False
    return any(marker in message for marker in (
        "404 not found", "429 too many requests", "500 internal server error",
        "502 bad gateway", "503 service unavailable", "504 gateway timeout",
        "connection reset", "connection refused", "i/o timeout", "tls handshake timeout",
        "temporary failure", "network is unreachable", "unexpected eof"))


def run(args, cwd, env, capture=False, public_retry=False):
    attempts = 8 if public_retry else 1
    for attempt in range(attempts):
        result = subprocess.run(args, cwd=cwd, env=env, check=False, text=True,
                                stdout=subprocess.PIPE if capture or public_retry else None,
                                stderr=subprocess.PIPE if public_retry else None)
        if not result.returncode:
            if public_retry and not capture:
                print(result.stdout or "", end="")
                print(result.stderr or "", end="", file=sys.stderr)
            return result.stdout if capture else None
        if public_retry and attempt < attempts - 1 and transient_download_failure((result.stdout or "") + (result.stderr or "")):
            print(f"Public proxy retry {attempt + 1}/8: {' '.join(args)}", flush=True)
            time.sleep(5)
            continue
        raise subprocess.CalledProcessError(result.returncode, args, output=result.stdout, stderr=result.stderr)


def json_stream(text):
    decoder = json.JSONDecoder()
    values = []
    while text.strip():
        value, end = decoder.raw_decode(text.lstrip())
        values.append(value)
        text = text.lstrip()[end:]
    return values


def validate_graph(graph, version, source=False, peer=None):
    modules = {item["Path"]: item for item in graph}
    for item in graph:
        if item.get("Main"):
            continue
        replacement = item.get("Replace")
        permitted = source and (item["Path"] == CORE or (item["Path"] == PEER and peer))
        if replacement and not permitted:
            raise ValueError(f"unexpected module replacement: {item['Path']}")
        if not replacement and (not item.get("Sum") or not item.get("GoModSum")):
            raise ValueError(f"missing verified checksums: {item['Path']}")
    core = modules.get(CORE, {})
    if source:
        if core.get("Replace", {}).get("Dir") != str(ROOT):
            raise ValueError("source core replacement does not select this checkout")
    elif core.get("Version") != version:
        raise ValueError(f"core version mismatch: expected {version}, got {core.get('Version')}")
    actual_peer = modules.get(PEER, {})
    if peer:
        if actual_peer.get("Replace", {}).get("Dir") != str(peer.resolve()):
            raise ValueError("peer replacement does not select requested checkout")
    elif actual_peer.get("Version") != PEER_VERSION or actual_peer.get("Replace"):
        raise ValueError(f"unsupported peer: expected public {PEER_VERSION}")
    return modules


def validate_test_events(output):
    events = [json.loads(line) for line in output.splitlines() if line.strip()]
    skipped = [event.get("Test", event.get("Package")) for event in events if event.get("Action") == "skip" and event.get("Test")]
    if skipped:
        raise ValueError("required consumer tests skipped: " + ", ".join(skipped))
    if not any(event.get("Action") == "pass" and event.get("Test") for event in events):
        raise ValueError("no semantic consumer tests executed")


def validate_peer_download(download):
    if download.get("Error"):
        raise ValueError(download["Error"])
    if download.get("Path") != PEER or download.get("Version") != PEER_VERSION:
        raise ValueError("peer download version mismatch")
    if download.get("Origin", {}).get("Hash") != PEER_SHA:
        raise ValueError("peer resolved SHA differs from supported immutable ref")
    if not download.get("Sum") or not download.get("GoModSum"):
        raise ValueError("peer checksums missing")


def parser():
    result = argparse.ArgumentParser()
    mode = result.add_mutually_exclusive_group()
    mode.add_argument("--published", metavar="VERSION")
    mode.add_argument("--candidate", metavar="VERSION")
    result.add_argument("--peer", type=Path)
    result.add_argument("--baseline", action="store_true")
    result.add_argument("--report", type=Path)
    return result


def main(argv=None):
    cli = parser()
    args = cli.parse_args(argv)
    version = args.published or args.candidate
    if version and not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", version):
        cli.error("an exact semantic version is required")
    if args.baseline:
        if args.candidate or args.peer or (args.published and args.published != BASELINE):
            cli.error(f"baseline selects only public {BASELINE}")
        version = BASELINE
    if args.peer and version:
        cli.error("--peer is only supported for explicit source compatibility checks")
    mode = "baseline" if args.baseline else "published" if args.published else "candidate" if args.candidate else "source"
    evidence = {"mode": mode, "version": version, "peer_ref": PEER_VERSION,
                "peer_sha": PEER_SHA, "status": "FAIL"}
    try:
        with tempfile.TemporaryDirectory(prefix="contexty-chat-") as temporary:
            checkout = Path(temporary) / "consumer"
            shutil.copytree(ROOT / "integration/chat", checkout)
            env = dict(os.environ, GOWORK="off", GOENV="off", GOTOOLCHAIN="go" + REGISTRY["toolchain"]["go"], GOFLAGS="", GOPATH=str(Path(temporary) / "gopath"),
                       GOMODCACHE=str(Path(temporary) / "modules"),
                       GOCACHE=os.environ.get("GOCACHE", str(Path(temporary) / "build-cache")), GOSUMDB="sum.golang.org",
                       GOPRIVATE="none", GONOPROXY="none", GONOSUMDB="none")
            if mode == "candidate":
                if not env.get("GOPROXY", "").startswith("file://"):
                    raise ValueError("candidate requires an isolated file artifact GOPROXY")
                env["GONOSUMDB"] = CORE + "," + CORE + "/*"
            else:
                env["GOPROXY"] = "https://proxy.golang.org"
            if args.baseline:
                for file in checkout.rglob("*.go"):
                    file.unlink()
                shutil.copy(ROOT / "integration/baseline_test.go.txt", checkout / "baseline_test.go")
            # Pin the peer before tidy; graph validation rejects transitive upgrades.
            run(["go", "mod", "edit", "-require=" + PEER + "@" + PEER_VERSION], checkout, env)
            if version:
                run(["go", "mod", "edit", "-require=" + CORE + "@" + version], checkout, env)
            else:
                run(["go", "mod", "edit", "-replace=" + CORE + "=" + str(ROOT)], checkout, env)
                digest = hashlib.sha256()
                for file in sorted(ROOT.rglob("*")):
                    if file.is_file() and (file.suffix == ".go" or file.name in ("go.mod", "go.sum")) and ".git" not in file.parts:
                        digest.update(file.relative_to(ROOT).as_posix().encode() + b"\0" + file.read_bytes())
                evidence["source_bytes_sha256"] = digest.hexdigest()
                evidence["source_revision"] = env.get("CONTEXTY_SOURCE_SHA")
                if (ROOT / ".git").exists():
                    evidence["source_sha"] = run(["git", "rev-parse", "HEAD"], ROOT, env, True).strip()
                    evidence["source_dirty"] = bool(run(["git", "status", "--porcelain"], ROOT, env, True).strip())
                if args.peer:
                    run(["go", "mod", "edit", "-replace=" + PEER + "=" + str(args.peer.resolve())], checkout, env)
                    evidence["peer_sha"] = run(["git", "rev-parse", "HEAD"], args.peer, env, True).strip()
            run(["go", "mod", "tidy"], checkout, env, public_retry=mode == "published")
            # Download all ZIPs first so every graph entry has independently verified sums.
            run(["go", "mod", "download", "all"], checkout, env, public_retry=mode == "published")
            graph = json_stream(run(["go", "list", "-m", "-json", "all"], checkout, env, True))
            validate_graph(graph, version, source=not version, peer=args.peer)
            evidence["module_graph"] = graph
            if not args.peer:
                peer_env = dict(env, GOPROXY="https://proxy.golang.org", GONOSUMDB="none")
                download = json.loads(run(["go", "mod", "download", "-json", PEER + "@" + PEER_VERSION], checkout, peer_env, True))
                validate_peer_download(download)
                evidence["peer_download"] = download
            run(["go", "mod", "verify"], checkout, env)
            test_output = run(["go", "test", "-json", "-race", "-count=1", "./..."], checkout, env, True)
            validate_test_events(test_output)
            evidence["semantic_test_events"] = [json.loads(line) for line in test_output.splitlines() if line.strip() and json.loads(line).get("Action") in ("pass", "skip", "fail")]
            print(test_output, end="")
            run(["go", "vet", "./..."], checkout, env)
            if not args.baseline:
                run(["go", "run", "./cmd/recipe"], checkout, env)
            evidence["status"] = "PASS"
            print("Published baseline only; full role contract NOT checked" if args.baseline else "Full consumer semantic fixtures PASS", flush=True)
    except (ValueError, OSError, subprocess.CalledProcessError) as error:
        evidence["error"] = str(error) + ("\n" + (error.stderr or error.output or "") if isinstance(error, subprocess.CalledProcessError) else "")
        print(f"Consumer {mode} FAIL: {evidence['error']}", file=sys.stderr)
    finally:
        if args.report:
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(json.dumps(evidence, indent=2) + "\n")
    return 0 if evidence["status"] == "PASS" else 1


if __name__ == "__main__":
    sys.exit(main())
