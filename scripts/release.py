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


class ReleaseError(Exception):
    pass


class PublicationState:
    def __init__(self):
        self.outcome = "not published (failed before push)"


def run(args, cwd, *, capture=True, check=True):
    # Read-only source queries must not refresh or write its index.
    env = dict(os.environ, GIT_OPTIONAL_LOCKS="0")
    result = subprocess.run(args, cwd=cwd, env=env, text=True,
                            stdout=subprocess.PIPE if capture else None,
                            stderr=subprocess.PIPE if capture else None)
    if check and result.returncode:
        raise ReleaseError(f"{' '.join(args)} failed: {result.stderr or result.returncode}")
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


def rewrite_modules(checkout, modules, version):
    parsed = []
    for directory in modules:
        file = checkout / directory / "go.mod"
        if not file.is_file() or file.is_symlink() or not file.resolve().is_relative_to(checkout.resolve()):
            raise ReleaseError(f"invalid module file: {directory}/go.mod")
        git(checkout, "ls-files", "--error-unmatch", f"{directory}/go.mod")
        parsed.append((file, json.loads(run(["go", "mod", "edit", "-json", str(file)], checkout).stdout)))
    root = next(data["Module"]["Path"] for file, data in parsed if file.parent == checkout)
    belongs = lambda name: name == root or name.startswith(root + "/")
    for file, data in parsed:
        if not belongs(data["Module"]["Path"]):
            raise ReleaseError(f"module outside root namespace: {data['Module']['Path']}")
        edits = []
        for requirement in data.get("Require") or []:
            if belongs(requirement["Path"]) and requirement["Version"] == "v0.0.0":
                edits.append(f"-require={requirement['Path']}@{version}")
        for replacement in data.get("Replace") or []:
            old = replacement["Old"]
            if belongs(old["Path"]):
                suffix = "@" + old["Version"] if old.get("Version") else ""
                edits.append(f"-dropreplace={old['Path']}{suffix}")
        run(["go", "mod", "edit", *edits, "-fmt", str(file)], checkout)
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


def release(kind, argument, publication):
    if kind not in ("patch", "break"):
        raise ReleaseError("usage: release.sh {patch|break} 'module directories'")
    modules = module_paths(argument)
    source = Path.cwd().resolve()
    if not (source / "go.mod").is_file():
        raise ReleaseError("run from repository root containing go.mod")
    if Path(git(source, "rev-parse", "--show-toplevel").stdout.strip()).resolve() != source:
        raise ReleaseError("run from repository root")
    if git(source, "status", "--porcelain", "--untracked-files=no").stdout:
        raise ReleaseError("tracked changes present; commit or stash before release")
    remote = git(source, "remote", "get-url", "--push", "origin").stdout.strip()
    # Resolve relative local origins before entering the temporary checkout.
    if ":" not in remote and not Path(remote).is_absolute():
        remote = str((source / remote).resolve())
    tags = published_refs(source, remote, ["refs/tags/v*"])
    old, version = next_version(kind, tags)
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
        files = rewrite_modules(checkout, modules, version)
        git(checkout, "add", "--", *files)
        if git(checkout, "diff", "--cached", "--quiet", check=False).returncode:
            git(checkout, "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", f"chore: release {version}")
        tags = [version, *(f"{directory}/{version}" for directory in modules if directory != ".")]
        refs = ["refs/tags/" + tag for tag in tags]
        signed_tags = git(checkout, "config", "--bool", "--get", "tag.gpgsign", check=False).stdout.strip() == "true"
        for tag in tags:
            message = ["-m", f"release {version}"] if signed_tags else []
            git(checkout, "tag", *message, tag)
        expected = {ref: git(checkout, "rev-parse", ref).stdout.strip() for ref in refs}
        commit = git(checkout, "rev-parse", "HEAD").stdout.strip()
        print("Release refs: " + ", ".join(refs), flush=True)
        publication.outcome = "unknown (push attempted; publication may still be in flight)"
        try:
            result = git(checkout, "push", "--atomic", "origin", *(f"{ref}:{ref}" for ref in refs), check=False)
        except (OSError, KeyboardInterrupt) as error:
            publication.outcome = publish_outcome(checkout, remote, expected, interrupted=True)
            raise ReleaseError("push interrupted") from error
        if result.returncode:
            publication.outcome = publish_outcome(checkout, remote, expected)
            raise ReleaseError(f"push failed\n{result.stderr}")
        publication.outcome = "published (atomic push completed)"
        print(f"Release {version} published: {commit}", flush=True)


def main():
    publication = PublicationState()
    try:
        if len(sys.argv) != 3:
            raise ReleaseError("usage: release.sh {patch|break} 'module directories'")
        release(*sys.argv[1:], publication)
    except (ReleaseError, OSError, ValueError, EOFError, KeyboardInterrupt) as error:
        print(f"Release failed: {error}\nPublication outcome: {publication.outcome}"
              "\nOriginal checkout preserved; temporary checkout cleaned.", file=sys.stderr)
        return 1
    return 0


def interrupt(signum, frame):
    raise KeyboardInterrupt(f"signal {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    sys.exit(main())
