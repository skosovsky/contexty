#!/usr/bin/env python3
"""Versioned, fail-closed local/CI/release gate. No runtime library dependencies."""
import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import platform
import re
import shlex
import shutil
import subprocess
import tempfile
import time

ROOT = Path(__file__).resolve().parent.parent
REGISTRY = ROOT / "scripts/checks.json"


def run_command(command, cwd, env):
    return subprocess.run(command, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE,
                          stderr=subprocess.STDOUT, check=False, timeout=1800)


def source_manifest(root):
    result = {}
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if any(part in {".git", ".peer", ".cache", ".check-results", "__pycache__"} for part in relative.parts):
            continue
        if path.is_symlink():
            result[str(relative)] = "symlink:" + os.readlink(path)
        elif path.is_file():
            result[str(relative)] = hashlib.sha256(path.read_bytes()).hexdigest()
    return result


def source_digest(root):
    digest = hashlib.sha256()
    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if any(part in {".git", ".peer", ".cache", ".check-results", "__pycache__"} for part in relative.parts):
            continue
        if path.is_file():
            digest.update(str(relative).encode() + b"\0" + path.read_bytes())
    return digest.hexdigest()


def inventory(root, registry):
    actual = sorted(str(path.parent.relative_to(root)) for path in root.rglob("go.mod")
                    if not any(part.startswith(".") or part == "vendor" for part in path.relative_to(root).parts))
    expected = sorted(registry["modules"]["test"])
    if actual != expected:
        raise ValueError(f"module inventory drift: actual={actual}; registry={expected}")
    if not set(registry["modules"]["release"]).issubset(expected):
        raise ValueError("release inventory must be a subset of test inventory")
    for module in actual:
        content = (root / module / "go.mod").read_text()
        if f"go {registry['toolchain']['go']}\n" not in content:
            raise ValueError(f"{module}: toolchain drift")
    return actual


def plan(registry, profile, shard=None, candidate=None, version=None):
    lanes = []
    for item in registry["lanes"]:
        lane = dict(item)
        lane["selected"] = profile in lane["profiles"]
        if shard and lane["id"] not in shard:
            lane["selected"] = False
        lanes.append(lane)
    if shard:
        unknown = set(shard) - {lane["id"] for lane in lanes}
        if unknown:
            raise ValueError(f"unknown shard lanes: {sorted(unknown)}")
        required = {lane["id"] for lane in lanes if lane["selected"]}
        while True:
            expanded = required | {dep for lane in lanes if lane["id"] in required for dep in lane.get("needs", [])}
            if expanded == required:
                break
            required = expanded
        for lane in lanes:
            lane["selected"] = lane["id"] in required
    return lanes


def execute(lanes, action):
    """Collect independent failures; never run a lane after a failed prerequisite."""
    results = []
    states = {}
    for lane in lanes:
        started = time.monotonic()
        if not lane["selected"]:
            status, output, evidence = "SKIP", "outside selected profile/variant", {}
        elif any(states.get(dep) != "PASS" for dep in lane.get("needs", [])):
            status, output, evidence = "BLOCKED", "prerequisite did not PASS", {}
        else:
            try:
                status, output, evidence = action(lane)
            except Exception as error:  # Each lane error is evidence; independent lanes still run.
                status, output, evidence = "FAIL", str(error), {}
        states[lane["id"]] = status
        results.append({"id": lane["id"], "status": status, "required": lane["selected"],
                        "seconds": round(time.monotonic() - started, 3), "output": output, "lane": lane, **evidence})
    success = all(result["status"] == "PASS" for result in results if result["required"])
    return results, success


class Actions:
    def __init__(self, root, registry, args, env):
        self.supplied_candidate = args.candidate_version
        self.root, self.registry, self.args, self.env = root, registry, args, env

    def __call__(self, lane):
        originals = {}
        if lane.get("isolated"):
            cwd = self.root / lane.get("module", ".")
            for name in ["go.mod", "go.sum"]:
                path = cwd / name
                originals[path] = path.read_bytes() if path.exists() else None
        try:
            if lane.get("isolated"):
                prepared = self.prepare_isolated(lane)
                if prepared is not None:
                    return prepared
            before = source_manifest(self.root)
            status, output, evidence = self.action(lane)
            after = source_manifest(self.root)
            allowed = {lane["id"] + ".json"} if lane.get("kind") == "chat" else set()
            changed = sorted(path for path in before.keys() | after.keys()
                             if before.get(path) != after.get(path) and path not in allowed)
            evidence["source_mutations"] = changed
            if changed:
                status = "FAIL"
                output += "\nrequired lane mutated checked source bytes: " + ", ".join(changed)
            return status, output, evidence
        finally:
            # Gate-owned setup never becomes input to another independent lane.
            for path, content in originals.items():
                if content is None:
                    path.unlink(missing_ok=True)
                else:
                    path.write_bytes(content)

    def prepare_isolated(self, lane):
        cwd = self.root / lane.get("module", ".")
        # This entire checkout is already temporary; only its isolated adapter mod is edited.
        module = self.registry["module_path"]
        edit = ["go", "mod", "edit", "-dropreplace=" + module]
        result = run_command(edit, cwd, self.env)
        if result.returncode:
            return "FAIL", result.stdout, {"command": edit}
        if self.supplied_candidate:
            edit = ["go", "mod", "edit", "-require=" + module + "@" + self.supplied_candidate]
        else:
            edit = ["go", "mod", "edit", "-replace=" + module + "=" + str(self.root)]
        result = run_command(edit, cwd, self.env)
        if result.returncode:
            return "FAIL", result.stdout, {"command": edit}
        if self.supplied_candidate:
            download = ["go", "mod", "download", module + "@" + self.supplied_candidate]
            result = run_command(download, cwd, self.env)
            if result.returncode:
                return "FAIL", result.stdout, {"command": shlex.join(download)}
        # Normalize gate-owned temporary metadata before observing command writes.
        tidy = ["go", "mod", "tidy"]
        result = run_command(tidy, cwd, self.env)
        if result.returncode:
            return "FAIL", result.stdout, {"command": shlex.join(tidy)}
        return None

    def action(self, lane):
        kind = lane.get("kind", "command")
        command = lane.get("command", [])
        cwd = self.root / lane.get("module", ".")
        if kind == "local-candidate":
            return self.local_candidate()
        if kind == "published-modules":
            return self.published_modules()
        if kind == "source-identity":
            sha = self.env.get("CONTEXTY_SOURCE_SHA", "")
            return ("PASS" if re.fullmatch(r"[a-f0-9]{40}", sha) else "BLOCKED", "Git source revision: " + sha, {"command": "git rev-parse HEAD", "source_sha": sha})
        if kind == "inventory":
            return "PASS", json.dumps(inventory(self.root, self.registry)), {"command": "registry inventory verification"}
        if kind == "baseline-config":
            digest = hashlib.sha256((self.root / ".golangci.yml").read_bytes()).hexdigest()
            expected = self.registry["linter_baseline"]["sha256"]
            return ("PASS" if digest == expected else "FAIL", f"linter config SHA256 {digest}; expected {expected}", {"command": "verify versioned linter baseline SHA256"})
        if kind == "toolchain":
            command = ["go", "version"]
        elif kind == "docker":
            command = ["docker", "info", "--format", "{{.ServerVersion}}"]
        elif kind == "linter":
            command = ["go", "run", self.registry["toolchain"]["linter_package"] + "@" + self.registry["toolchain"]["linter"], *command]
        elif kind == "chat":
            command = ["python3", "scripts/check_chat.py"]
            mode = lane["mode"]
            if mode == "candidate":
                if not self.args.candidate_version:
                    return "BLOCKED", "candidate artifact prerequisite did not prepare version", {}
                command += ["--candidate", self.args.candidate_version]
            elif mode == "published":
                if not self.args.version:
                    return "BLOCKED", "published profile requires --version", {"command": command}
                command += ["--published", self.args.version]
            elif mode == "baseline":
                command += ["--published", self.registry["peers"]["baseline"], "--baseline"]
            command += ["--report", str(self.root / (lane["id"] + ".json"))]
        elif kind == "generation":
            before = source_digest(self.root)
            outputs = []
            success = True
            for module in self.registry["modules"]["test"]:
                result = run_command(["go", "generate", "./..."], self.root / module, self.env)
                outputs.append(module + ": " + result.stdout)
                success = success and result.returncode == 0
            unchanged = before == source_digest(self.root)
            return ("PASS" if success and unchanged else "FAIL", "\n".join(outputs) +
                    ("" if unchanged else "generation changed committed bytes"), {"command": "go generate ./... in all test modules; verify bytes unchanged"})
        try:
            result = run_command(command, cwd, getattr(self, "candidate_env", self.env) if kind == "chat" and lane["mode"] == "candidate" else self.env)
        except FileNotFoundError as error:
            return ("BLOCKED" if kind in {"docker", "toolchain"} else "FAIL", str(error), {"command": shlex.join(command)})
        status = "PASS" if result.returncode == 0 else "FAIL"
        if kind in {"docker", "toolchain"} and result.returncode:
            status = "BLOCKED"
        if kind == "toolchain" and f"go{self.registry['toolchain']['go']} " not in result.stdout:
            status = "BLOCKED"
        if kind == "linter" and lane["command"] == ["fmt", "--diff"] and result.stdout.strip():
            status = "FAIL"
        if lane.get("forbid_test_skip"):
            test_count = 0
            for line in result.stdout.splitlines():
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue
                if event.get("Action") == "run" and event.get("Test"):
                    test_count += 1
                if event.get("Action") == "skip" and event.get("Test"):
                    status = "FAIL"
                    result.stdout += "\nrequired integration test skipped"
                    break
            if test_count == 0:
                status = "FAIL"
                result.stdout += "\nrequired test lane matched zero tests"
        if lane.get("require_test") and "no tests to run" in result.stdout:
            status = "FAIL"
        evidence = {"command": shlex.join(command), "cwd": lane.get("module", "."), "exit_code": result.returncode}
        if lane.get("graph") and status == "PASS":
            graph = run_command(["go", "list", "-m", "-json", "all"], cwd, self.env)
            evidence["module_graph"] = graph.stdout
            if graph.returncode:
                status = "FAIL"
        if kind == "chat":
            report = self.root / (lane["id"] + ".json")
            if report.exists():
                evidence["consumer"] = json.loads(report.read_text())
        return status, result.stdout, evidence

    def local_candidate(self):
        if self.args.candidate_version:
            if self.env.get("CONTEXTY_CANDIDATE_ENV_ERROR"):
                return "BLOCKED", self.env["CONTEXTY_CANDIDATE_ENV_ERROR"], {}
            return "PASS", "immutable release candidate proxy supplied by release", {"candidate_version": self.args.candidate_version}
        spec = importlib.util.spec_from_file_location("candidate_release", self.root / "scripts/release.py")
        release = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(release)
        candidate = self.root.parent / "candidate"
        shutil.copytree(self.root, candidate)
        for command in [["git", "init", "--quiet"], ["git", "add", "."]]:
            result = run_command(command, candidate, self.env)
            if result.returncode:
                return "FAIL", result.stdout, {"command": shlex.join(command)}
        version = "v0.0.0"
        release.rewrite_modules(candidate, self.registry["modules"]["release"], version, environment=self.env)
        for command in [["git", "add", "."], ["git", "-c", "user.name=Gate Fixture", "-c", "user.email=gate@example.invalid", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "isolated candidate bytes"]]:
            result = run_command(command, candidate, self.env)
            if result.returncode:
                return "FAIL", result.stdout, {"command": shlex.join(command)}
        environment, manifest = release.candidate_proxy(candidate, self.registry["modules"]["release"], version, self.root.parent / "proxy", environment=self.env)
        self.candidate_env = dict(self.env, **environment)
        self.args.candidate_version = version
        return "PASS", "isolated local artifact built and Go ZIP validated without publication", {"artifacts": manifest, "candidate_version": version, "command": "release.rewrite_modules + release.candidate_proxy (temporary index only)"}

    def published_modules(self):
        if not self.args.version or not re.fullmatch(r"v[0-9]+\.[0-9]+\.[0-9]+", self.args.version):
            return "BLOCKED", "published module verification requires exact --version", {}
        expected_file = self.env.get("CONTEXTY_RELEASE_ARTIFACTS")
        expected = json.loads(Path(expected_file).read_text()) if expected_file else None
        downloads = []
        with tempfile.TemporaryDirectory(prefix="contexty-public-") as cache:
            (Path(cache) / "go.mod").write_text("module gate.invalid/public-verification\n\ngo " + self.registry["toolchain"]["go"] + "\n")
            env = dict(self.env, GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOMODCACHE=cache, GOPRIVATE="none", GONOSUMDB="none", GONOPROXY="none")
            for module in self.registry["modules"]["release"]:
                path = self.registry["module_path"] + ("/" + module if module != "." else "")
                command = ["go", "mod", "download", "-json", path + "@" + self.args.version]
                for attempt in range(8):
                    result = run_command(command, Path(cache), env)
                    if result.returncode == 0:
                        break
                    if attempt < 7:
                        print(f"Public proxy retry {attempt + 1}/8: {path}", flush=True)
                        time.sleep(5)
                if result.returncode:
                    return "FAIL", result.stdout, {"command": shlex.join(command), "downloads": downloads}
                record = json.loads(result.stdout)
                if not record.get("Sum") or not record.get("GoModSum") or record.get("Replace"):
                    return "FAIL", "missing public checksum or unexpected replace", {"downloads": downloads}
                if expected is not None:
                    artifact = expected.get(path)
                    if not artifact or any(record.get(key) != artifact.get(key) for key in ["Sum", "GoModSum"]):
                        return "FAIL", f"published bytes differ from checked candidate: {path}", {"downloads": downloads, "download": record}
                downloads.append(record)
        return "PASS", "all release modules downloaded with public checksum verification", {"downloads": downloads, "command": "go mod download -json each release module@" + self.args.version}


def check_environment(registry, candidate_version=None, inherited=None):
    """Do not let user Go configuration disable public verification or select peer bytes."""
    inherited = dict(os.environ if inherited is None else inherited)
    root_module = registry["module_path"]
    proxy = "https://proxy.golang.org"
    candidate_error = None
    if candidate_version:
        artifact = inherited.get("GOPROXY", "").split(",", 1)[0]
        if not artifact.startswith("file://"):
            candidate_error = "candidate requires the supplied immutable file:// artifact proxy"
            proxy = "off"
        else:
            proxy = artifact + ",https://proxy.golang.org"
    env = dict(inherited, GOWORK="off", GOTOOLCHAIN="go" + registry["toolchain"]["go"],
               CI="true", GOENV="off", GOFLAGS="", GOPRIVATE="none", GONOPROXY="none",
               GOSUMDB="sum.golang.org", GOPROXY=proxy,
               GONOSUMDB=root_module + "," + root_module + "/*" if candidate_version else "none")
    if candidate_error:
        env["CONTEXTY_CANDIDATE_ENV_ERROR"] = candidate_error
    else:
        env.pop("CONTEXTY_CANDIDATE_ENV_ERROR", None)
    return env


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--profile", choices=["fast", "test", "lint", "check", "published"], default="check")
    parser.add_argument("--candidate-version")
    parser.add_argument("--version")
    parser.add_argument("--plan", "--list", action="store_true")
    parser.add_argument("--modules-format", choices=["json", "plain"], default="json")
    parser.add_argument("--modules", choices=["test", "release"])
    parser.add_argument("--shard", help="comma-separated lane IDs plus automatic prerequisite closure")
    parser.add_argument("--report", type=Path)
    args = parser.parse_args()
    registry = json.loads(REGISTRY.read_text())
    if args.modules:
        print(" ".join(registry["modules"][args.modules]) if args.modules_format == "plain" else json.dumps(registry["modules"][args.modules]))
        return 0
    lanes = plan(registry, args.profile, args.shard.split(",") if args.shard else None, args.candidate_version, args.version)
    if args.plan:
        print(json.dumps({"registry": registry, "profile": args.profile, "lanes": lanes}, indent=2))
        return 0
    env = check_environment(registry, args.candidate_version)
    env.setdefault("GOLANGCI_LINT_CACHE", str(Path(tempfile.gettempdir()) / "contexty-golangci-cache"))
    env.setdefault("GOCACHE", str(Path(tempfile.gettempdir()) / "contexty-go-cache"))
    env.setdefault("GOPATH", str(Path(tempfile.gettempdir()) / "contexty-go-path"))
    if args.profile == "published":
        env.update(GOPROXY="https://proxy.golang.org", GOSUMDB="sum.golang.org", GOPRIVATE="none", GONOSUMDB="none", GONOPROXY="none")
    revision = run_command(["git", "rev-parse", "HEAD"], ROOT, env)
    sha = revision.stdout.strip() if revision.returncode == 0 else ""
    env["CONTEXTY_SOURCE_SHA"] = sha
    with tempfile.TemporaryDirectory(prefix="contexty-check-") as temp:
        root = Path(temp) / "source"
        shutil.copytree(ROOT, root, ignore=shutil.ignore_patterns(".git", ".peer", ".cache", "__pycache__"))
        digest = source_digest(root)
        actions = Actions(root, registry, args, env)
        def action(lane):
            print("RUN " + lane["id"], flush=True)
            return actions(lane)
        results, success = execute(lanes, action)
    report = {"schema": 1, "profile": args.profile, "success": success,
              "full_gate": args.profile == "check" and not args.shard and success,
              "platform": platform.platform(), "source_sha": sha, "source_sha256": digest,
              "toolchain": registry["toolchain"], "peers": registry["peers"],
              "candidate_version": args.candidate_version, "published_version": args.version,
              "registry_sha256": hashlib.sha256(REGISTRY.read_bytes()).hexdigest(), "results": results}
    for result in results:
        print(f"{result['status']:7} {result['seconds']:8.3f}s {result['id']}")
        if result["status"] in {"FAIL", "BLOCKED"}:
            print(result["output"])
    if args.report is None:
        args.report = Path(os.environ.get("CONTEXTY_CHECK_REPORT_DIR", str(ROOT / ".check-results"))) / (args.profile + ".json")
    if args.report:
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(report, indent=2) + "\n")
        print("Report: " + str(args.report.resolve()))
    else:
        print(json.dumps(report, indent=2))
    return 0 if success else 1


if __name__ == "__main__":
    raise SystemExit(main())
