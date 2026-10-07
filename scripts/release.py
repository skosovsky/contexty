#!/usr/bin/env python3
"""Prepare and publish exact release tags from an isolated committed checkout."""

import json
import os
from pathlib import Path
import re
import signal
import subprocess
import sys
import tempfile
import platform
import hashlib


class ReleaseError(Exception):
    pass


class PublicationState:
    def __init__(self):
        self.outcome = "not published (failed before push)"


def run(args, cwd, *, capture=True, check=True, environment=None):
    # Read-only source queries must not refresh or write its index.
    env = dict(os.environ, GIT_OPTIONAL_LOCKS="0")
    env.update(environment or {})
    result = subprocess.run(args, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    if check and result.returncode:
        raise ReleaseError(f"{' '.join(args)} failed: {result.stderr or result.stdout or result.returncode}")
    return result


def git(cwd, *args, **kwargs):
    return run(["git", *args], cwd, **kwargs)


def published_refs(cwd, remote, refs):
    result = git(cwd, "ls-remote", "--refs", remote, *refs)
    return {line.split()[1]: line.split()[0] for line in result.stdout.splitlines()}


def module_paths(argument):
    modules = argument.split()
    if not modules:
        raise ReleaseError("module list is empty")
    canonical = []
    for value in modules:
        path = Path(value)
        if path.is_absolute() or ".." in path.parts:
            raise ReleaseError(f"invalid module directory: {value}")
        name = path.as_posix()
        if name in canonical:
            raise ReleaseError(f"duplicate module directory: {value}")
        canonical.append(name)
    if "." not in canonical:
        raise ReleaseError("module list must include root '.'")
    return canonical


def next_version(kind, tags):
    versions = [tuple(map(int, match.groups())) for tag in tags
                if (match := re.fullmatch(r"refs/tags/v([0-9]+)\.([0-9]+)\.([0-9]+)", tag))]
    major, minor, patch = max(versions, default=(0, 0, 0))
    old = f"v{major}.{minor}.{patch}"
    if kind == "patch":
        patch += 1
    elif major == 0:
        minor, patch = minor + 1, 0
    else:
        major, minor, patch = major + 1, 0, 0
    return old, f"v{major}.{minor}.{patch}"


def rewrite_modules(checkout, modules, version, environment=None):
    parsed = []
    for directory in modules:
        file = checkout / directory / "go.mod"
        if not file.is_file() or file.is_symlink() or not file.resolve().is_relative_to(checkout.resolve()):
            raise ReleaseError(f"invalid module file: {directory}/go.mod")
        git(checkout, "ls-files", "--error-unmatch", f"{directory}/go.mod")
        parsed.append((file, json.loads(run(["go", "mod", "edit", "-json", str(file)], checkout, environment=environment).stdout)))
    root = next(data["Module"]["Path"] for file, data in parsed if file.parent == checkout)
    belongs = lambda name: name == root or name.startswith(root + "/")
    for file, data in parsed:
        relative = file.parent.relative_to(checkout).as_posix()
        expected_path = root if relative == "." else root + "/" + relative
        if data["Module"]["Path"] != expected_path:
            raise ReleaseError(f"module path/tag directory mismatch: {relative}: {data['Module']['Path']}")
        if not belongs(data["Module"]["Path"]):
            raise ReleaseError(f"module outside root namespace: {data['Module']['Path']}")
        edits = []
        for requirement in data.get("Require") or []:
            if belongs(requirement["Path"]) :
                edits.append(f"-require={requirement['Path']}@{version}")
        for replacement in data.get("Replace") or []:
            old = replacement["Old"]
            if belongs(old["Path"]):
                suffix = "@" + old["Version"] if old.get("Version") else ""
                edits.append(f"-dropreplace={old['Path']}{suffix}")
        run(["go", "mod", "edit", *edits, "-fmt", str(file)], checkout, environment=environment)
    return [str(Path(directory) / "go.mod") for directory in modules]


def publish_outcome(checkout, remote, expected, *, interrupted=False):
    try:
        observed = published_refs(checkout, remote, list(expected))
    except (ReleaseError, OSError, KeyboardInterrupt):
        return "unknown (remote refs could not be observed)"
    if all(observed.get(ref) == identity for ref, identity in expected.items()):
        return "published (all exact refs observed despite push error)"
    if not observed and not interrupted:
        return "not published (none of the exact refs exist)"
    return "unknown (publication may still be in flight or remote refs conflict; inspect before retry)"


def candidate_identity(checkout):
    """Detect every tracked-byte/index/ref mutation, including changes later reverted in index."""
    if git(checkout, "status", "--porcelain", "--untracked-files=all").stdout:
        raise ReleaseError("candidate mutated: checkout is no longer clean")
    return git(checkout, "rev-parse", "HEAD").stdout.strip()


def candidate_proxy(checkout, modules, version, base, environment=None):
    """Build canonical Go proxy artifacts from the immutable candidate commit."""
    tool_environment = dict(environment or {})
    helper = base.parent / "module-zip-tool"
    helper.mkdir(exist_ok=True)
    registry = checkout / "scripts/checks.json"
    pins = json.loads(registry.read_text())["toolchain"]
    (helper / "go.mod").write_text(f"module release.local/ziptool\n\ngo {pins['go']}\n\nrequire golang.org/x/mod {pins['module_zip']}\n")
    (helper / "main.go").write_text('''package main
import("os"; "golang.org/x/mod/module"; "golang.org/x/mod/zip")
func main(){
 f,err:=os.Create(os.Args[1]); if err!=nil {panic(err)}
 defer f.Close()
 if err=zip.CreateFromVCS(f,module.Version{Path:os.Args[2],Version:os.Args[3]},os.Args[4],os.Args[5],os.Args[6]);err!=nil{panic(err)}
}
''')
    binary = helper / "module-zip"
    run(["go", "build", "-mod=mod", "-o", str(binary), "."], helper,
        environment=dict(tool_environment, **{"GOWORK": "off", "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org", "GONOSUMDB": "none", "GOPRIVATE": "none", "GONOPROXY": "none"}))
    revision = git(checkout, "rev-parse", "HEAD").stdout.strip()
    manifest = {}
    for directory in modules:
        root = checkout / directory
        data = json.loads(run(["go", "mod", "edit", "-json", str(root / "go.mod")], checkout, environment=tool_environment).stdout)
        name = data["Module"]["Path"]
        escaped = "".join("!" + c.lower() if c.isupper() else c for c in name)
        target = base / escaped / "@v"
        target.mkdir(parents=True)
        (target / (version + ".mod")).write_bytes((root / "go.mod").read_bytes())
        (target / (version + ".info")).write_text(json.dumps({"Version": version, "Time": "2000-01-01T00:00:00Z"}))
        (target / "list").write_text(version + "\n")
        run([str(binary), str(target / (version + ".zip")), name, version, str(checkout), revision,
             "" if directory == "." else directory], checkout)
        manifest[name] = {extension: hashlib.sha256((target / (version + "." + extension)).read_bytes()).hexdigest()
                          for extension in ("mod", "info", "zip")}
    # Let Go enforce module ZIP canonical form and compute module hashes from the actual artifact.
    environment = dict(tool_environment, **{"GOPROXY": base.as_uri(), "GOSUMDB": "off", "GOPRIVATE": "none", "GONOPROXY": "none", "GONOSUMDB": "none", "GOWORK": "off", "GOMODCACHE": str(base.parent / "artifact-cache")})
    for name in manifest:
        result = json.loads(run(["go", "mod", "download", "-json", name + "@" + version], checkout, environment=environment).stdout)
        if not result.get("Sum") or not result.get("GoModSum") or result.get("Error"):
            raise ReleaseError("candidate artifact validation failed: " + name)
        manifest[name]["Sum"] = result["Sum"]
        manifest[name]["GoModSum"] = result["GoModSum"]
    environment.update(GOPROXY=base.as_uri() + ",https://proxy.golang.org", GOSUMDB="sum.golang.org", GONOSUMDB=",".join(manifest))
    return environment, manifest


def write_state(path, state):
    temporary = path.with_suffix(".tmp")
    temporary.write_text(json.dumps(state, indent=2) + "\n")
    os.replace(temporary, path)


def artifact_environment(base, artifacts):
    path = base / "release-artifacts.json"
    path.write_text(json.dumps(artifacts))
    return {"CONTEXTY_RELEASE_ARTIFACTS": str(path)}


def gate(checkout, profile, version, environment=None):
    option = "--candidate-version" if profile == "check" else "--version"
    run([sys.executable, "scripts/check.py", "--profile", profile, option, version], checkout,
        capture=False, environment=environment)
    if profile == "check" and platform.system() != "Linux":
        # The mandatory Linux profile must pass before any tags are created/pushed.
        run([sys.executable, "scripts/check_linux.py", "--candidate-version", version,
             "--report", str(Path(environment["CONTEXTY_CHECK_REPORT_DIR"]) / "linux-summary.json")],
            checkout, capture=False, environment=environment)


def recover(source, version, publication):
    path = source / git(source, "rev-parse", "--git-path", "contexty-release-state.json").stdout.strip()
    if not path.is_file():
        raise ReleaseError("no release recovery state")
    state = json.loads(path.read_text())
    if state["version"] != version:
        raise ReleaseError("recovery version does not match saved state")
    if published_refs(source, state["remote"], list(state["refs"])) != state["refs"]:
        publication.outcome = "unknown (saved exact refs not observed; inspect remote before retry)"
        raise ReleaseError("recovery refuses missing or conflicting refs; no tags changed")
    publication.outcome = "published (saved exact refs observed)"
    # Fetch only into a disposable clone; verification uses the immutable released commit.
    with tempfile.TemporaryDirectory(prefix="contexty-recover-") as temporary:
        checkout = Path(temporary) / "checkout"
        git(source, "clone", "--quiet", state["remote"], str(checkout))
        git(checkout, "checkout", "--quiet", "--detach", state["commit"])
        gate(checkout, "published", version, dict(artifact_environment(Path(temporary), state["artifacts"]), CONTEXTY_CHECK_REPORT_DIR=str(Path(state["evidence"]) / "published")))
    state["status"] = "verified"
    write_state(path, state)
    print(f"Release {version} verified: {state['commit']}", flush=True)


def release(kind, argument, publication):
    if kind == "recover":
        return recover(Path.cwd().resolve(), argument, publication)
    if kind not in ("patch", "break"):
        raise ReleaseError("usage: release.sh {patch|break} 'module directories' | release.sh recover VERSION")
    modules = module_paths(argument)
    registry = Path("scripts/checks.json")
    if not registry.is_file():
        raise ReleaseError("release registry is missing")
    configuration = json.loads(registry.read_text())
    inventory = configuration["modules"]["release"]
    tool_environment = {"GOTOOLCHAIN": "go" + configuration["toolchain"]["go"], "GOENV": "off",
                        "GOWORK": "off", "GOFLAGS": "", "GOPRIVATE": "none", "GONOPROXY": "none", "GONOSUMDB": "none"}
    if set(modules) != set(inventory):
        raise ReleaseError("release module inventory disagrees with registry")
    source = Path.cwd().resolve()
    if not (source / "go.mod").is_file():
        raise ReleaseError("run from repository root containing go.mod")
    if Path(git(source, "rev-parse", "--show-toplevel").stdout.strip()).resolve() != source:
        raise ReleaseError("run from repository root")
    if git(source, "status", "--porcelain", "--untracked-files=no").stdout:
        raise ReleaseError("tracked changes present; commit or stash before release")
    state_path = source / git(source, "rev-parse", "--git-path", "contexty-release-state.json").stdout.strip()
    if state_path.is_file():
        previous = json.loads(state_path.read_text())
        if previous["status"] not in ("verified", "not-published"):
            raise ReleaseError("unresolved previous release; use recovery before preparing another version")
    remote = git(source, "remote", "get-url", "--push", "origin").stdout.strip()
    # Resolve relative local origins before entering the temporary checkout.
    if ":" not in remote and not Path(remote).is_absolute():
        remote = str((source / remote).resolve())
    tags = published_refs(source, remote, ["refs/tags/*"])
    old, version = next_version(kind, tags)
    while any(ref in tags for ref in ["refs/tags/" + version, *(f"refs/tags/{directory}/{version}" for directory in modules if directory != ".")]):
        if kind != "patch":
            raise ReleaseError("next breaking version conflicts with published module tag")
        major, minor, patch = map(int, version[1:].split("."))
        version = f"v{major}.{minor}.{patch + 1}"
    print(f"Current version: {old}\nNew version: {version} ({kind})", flush=True)
    if input(f"Proceed with release {version}? [y/N] ").strip().lower() != "y":
        raise ReleaseError("aborted; not published")
    head = git(source, "rev-parse", "HEAD").stdout.strip()
    with tempfile.TemporaryDirectory(prefix="contexty-release-") as temporary:
        checkout = Path(temporary) / "checkout"
        git(source, "clone", "--quiet", "--no-local", "--no-hardlinks", str(source), str(checkout))
        git(checkout, "checkout", "--quiet", "--detach", head)
        git(checkout, "remote", "set-url", "origin", remote)
        # Clone does not carry local repository identity configuration.
        for key in ("user.name", "user.email", "user.signingkey", "commit.gpgsign", "tag.gpgsign", "gpg.format", "gpg.program", "gpg.ssh.program"):
            value = git(source, "config", "--get", key, check=False)
            if value.returncode == 0:
                git(checkout, "config", key, value.stdout.strip())
        files = rewrite_modules(checkout, modules, version, environment=tool_environment)
        git(checkout, "add", "--", *files)
        if git(checkout, "diff", "--cached", "--quiet", check=False).returncode:
            git(checkout, "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", f"chore: release {version}")
        commit = candidate_identity(checkout)
        environment, artifacts = candidate_proxy(checkout, modules, version, Path(temporary) / "proxy", environment=tool_environment)
        evidence = source / git(source, "rev-parse", "--git-path", f"contexty-release-evidence/{version}").stdout.strip()
        evidence.mkdir(parents=True, exist_ok=True)
        environment["CONTEXTY_CHECK_REPORT_DIR"] = str(evidence / "check")
        gate(checkout, "check", version, environment)
        if candidate_identity(checkout) != commit or git(source, "rev-parse", "HEAD").stdout.strip() != head or git(source, "status", "--porcelain", "--untracked-files=no").stdout:
            raise ReleaseError("source/candidate changed after gate; validation invalidated")
        for name, hashes in artifacts.items():
            escaped = "".join("!" + c.lower() if c.isupper() else c for c in name)
            for extension in ("zip", "mod", "info"):
                path = Path(temporary) / "proxy" / escaped / "@v" / (version + "." + extension)
                if hashlib.sha256(path.read_bytes()).hexdigest() != hashes[extension]:
                    raise ReleaseError("candidate proxy bytes changed after gate")
        tags = [version, *(f"{directory}/{version}" for directory in modules if directory != ".")]
        refs = ["refs/tags/" + tag for tag in tags]
        signed_tags = git(checkout, "config", "--bool", "--get", "tag.gpgsign", check=False).stdout.strip() == "true"
        for tag in tags:
            message = ["-m", f"release {version}"] if signed_tags else []
            git(checkout, "tag", *message, tag)
        expected = {ref: git(checkout, "rev-parse", ref).stdout.strip() for ref in refs}
        commit = git(checkout, "rev-parse", "HEAD").stdout.strip()
        print("Release refs: " + ", ".join(refs), flush=True)
        if candidate_identity(checkout) != commit or git(source, "rev-parse", "HEAD").stdout.strip() != head or git(source, "status", "--porcelain", "--untracked-files=no").stdout:
            raise ReleaseError("source/candidate changed before publication")
        state = {"version": version, "source": head, "commit": commit, "remote": remote,
                 "refs": expected, "artifacts": artifacts, "evidence": str(evidence), "status": "push-attempted"}
        write_state(state_path, state)
        publication.outcome = "unknown (push attempted; publication may still be in flight)"
        try:
            result = git(checkout, "push", "--atomic", remote, *(f"{ref}:{ref}" for ref in refs), check=False)
        except (OSError, KeyboardInterrupt) as error:
            publication.outcome = publish_outcome(checkout, remote, expected, interrupted=True)
            state["status"] = "unknown"
            write_state(state_path, state)
            raise ReleaseError("push interrupted") from error
        if result.returncode:
            publication.outcome = publish_outcome(checkout, remote, expected)
            state["status"] = "not-published" if publication.outcome.startswith("not published") else "unknown"
            write_state(state_path, state)
            raise ReleaseError(f"push failed\n{result.stderr}")
        publication.outcome = "published (atomic push completed)"
        state["status"] = "published-unverified"
        write_state(state_path, state)
        gate(checkout, "published", version, dict(artifact_environment(Path(temporary), artifacts), CONTEXTY_CHECK_REPORT_DIR=str(evidence / "published")))
        state["status"] = "verified"
        write_state(state_path, state)
        print(f"Release {version} published and verified: {commit}", flush=True)


def main():
    publication = PublicationState()
    try:
        if len(sys.argv) != 3:
            raise ReleaseError("usage: release.sh {patch|break} 'module directories' | release.sh recover VERSION")
        release(*sys.argv[1:], publication)
    except (ReleaseError, OSError, ValueError, EOFError, KeyboardInterrupt) as error:
        print(f"Release failed: {error}\nPublication outcome: {publication.outcome}"
              "\nOriginal checkout preserved; temporary checkout cleaned. Recovery: scripts/release.sh recover VERSION (never republishes).", file=sys.stderr)
        return 1
    return 0


def interrupt(signum, frame):
    raise KeyboardInterrupt(f"signal {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    sys.exit(main())
