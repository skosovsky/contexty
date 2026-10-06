#!/usr/bin/env python3
"""AAA release fixtures; every origin is a temporary local bare repository."""
import hashlib
import os
from pathlib import Path
import shutil
import shlex
import signal
import time
import subprocess
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parent
ENV = dict(os.environ, GIT_OPTIONAL_LOCKS="0", GOWORK="off", GOCACHE="/private/tmp/contexty-go-cache")


def command(args, cwd, **kwargs):
    return subprocess.run(args, cwd=cwd, env=ENV, text=True, capture_output=True, check=True, **kwargs)


class Fixture:
    def __init__(self, base, *, modules=True, baseline=False):
        self.repo = base / "repo"
        self.origin = base / "origin.git"
        self.temp = base / "temporary"
        self.repo.mkdir()
        self.temp.mkdir()
        command(["git", "init", "--bare", str(self.origin)], self.repo)
        self.git("init", "--initial-branch=main")
        self.git("config", "user.name", "Fixture")
        self.git("config", "commit.gpgsign", "false")
        self.git("config", "tag.gpgsign", "false")
        self.git("config", "user.email", "fixture@example.invalid")
        self.git("remote", "add", "origin", str(self.origin))
        (self.repo / "go.mod").write_text("module example.invalid/contexty\n\ngo 1.23\n")
        (self.repo / "scripts").mkdir()
        if baseline:
            script = command(["git", "show", "2a1a7ab7fb919c436e210c2804dd615e2d80626a:scripts/release.sh"], SOURCE.parent).stdout
            (self.repo / "scripts/release.sh").write_text(script)
        else:
            for name in ("release.sh", "release.py"):
                shutil.copyfile(SOURCE / name, self.repo / "scripts" / name)
        if modules:
            (self.repo / "sub").mkdir()
            (self.repo / "sub/go.mod").write_text("module example.invalid/contexty/sub\n\ngo 1.23\n\nrequire example.invalid/contexty v0.0.0\nreplace example.invalid/contexty => ..\n")
        self.git("add", ".")
        self.git("commit", "-m", "fixture")
        self.git("push", "origin", "main")

    def git(self, *args):
        return command(["git", *args], self.repo).stdout.strip()

    def snapshot(self):
        return (self.git("rev-parse", "HEAD"), self.git("branch", "--show-current"),
                self.git("tag", "--list"), self.git("diff", "--binary", "HEAD"),
                hashlib.sha256((self.repo / ".git/index").read_bytes()).hexdigest(),
                {str(p.relative_to(self.repo)): p.read_bytes() for p in self.repo.rglob("*")
                 if p.is_file() and ".git" not in p.relative_to(self.repo).parts})

    def execute(self, modules=". ./sub"):
        return subprocess.run(["bash", "scripts/release.sh", "patch", modules], cwd=self.repo,
                              env=dict(getattr(self, "env", ENV), TMPDIR=str(self.temp)), input="y\n", text=True, capture_output=True)

    def tags(self):
        return {line.split()[1]: line.split()[0] for line in self.git("ls-remote", "--refs", "origin", "refs/tags/*").splitlines()}

    def reject(self):
        hook = self.origin / "hooks/pre-receive"
        hook.write_text("#!/bin/sh\necho 'fixture rejecting push' >&2\nexit 1\n")
        hook.chmod(0o755)
        return hook


class ReleaseTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="contexty-release-fixture-")
        self.addCleanup(self.directory.cleanup)
        self.base = Path(self.directory.name)

    def fixture(self, **kwargs):
        return Fixture(self.base, **kwargs)

    def assert_preserved(self, fixture, before):
        self.assertEqual(before, fixture.snapshot())
        self.assertEqual([], list(fixture.temp.iterdir()))

    def test_success_isolates_untracked_and_exact_refs(self):
        # Arrange: two modules, unrelated local tag and private untracked file.
        fixture = self.fixture()
        fixture.git("tag", "scratch-local")
        (fixture.repo / "private-untracked.txt").write_text("harmless fixture data\n")
        before = fixture.snapshot()
        main = fixture.git("ls-remote", "origin", "refs/heads/main")
        # Act.
        result = fixture.execute()
        # Assert: exact release refs, rewritten modules, no unrelated content/ref.
        self.assertEqual(0, result.returncode, result.stdout + result.stderr)
        tags = fixture.tags()
        self.assertEqual({"refs/tags/v0.0.1", "refs/tags/sub/v0.0.1"}, set(tags))
        self.assertEqual(1, len(set(tags.values())))
        self.assertEqual(main, fixture.git("ls-remote", "origin", "refs/heads/main"))
        files = command(["git", "--git-dir", str(fixture.origin), "ls-tree", "--name-only", "v0.0.1"], fixture.repo).stdout
        self.assertNotIn("private-untracked.txt", files)
        mod = command(["git", "--git-dir", str(fixture.origin), "show", "v0.0.1:sub/go.mod"], fixture.repo).stdout
        self.assertIn("require example.invalid/contexty v0.0.1", mod)
        self.assertNotIn("replace", mod)
        self.assert_preserved(fixture, before)

    def test_rejected_push_cleanup_and_retry(self):
        # Arrange: origin refuses all refs atomically.
        fixture = self.fixture()
        hook = fixture.reject()
        before = fixture.snapshot()
        # Act.
        rejected = fixture.execute()
        # Assert: observed not-published, unchanged source, cleanup, retry succeeds.
        self.assertNotEqual(0, rejected.returncode)
        self.assertIn("not published", rejected.stderr)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)
        hook.unlink()
        retried = fixture.execute()
        self.assertEqual(0, retried.returncode, retried.stderr)
        self.assertEqual({"refs/tags/v0.0.1", "refs/tags/sub/v0.0.1"}, set(fixture.tags()))
        self.assert_preserved(fixture, before)

    def test_preparation_failure(self):
        # Arrange: committed malformed submodule cannot be prepared.
        fixture = self.fixture()
        (fixture.repo / "sub/go.mod").write_text("not a valid module\n")
        fixture.git("add", "sub/go.mod")
        fixture.git("commit", "-m", "invalid fixture")
        before = fixture.snapshot()
        # Act / Assert: fail before publication, preserve all source state.
        result = fixture.execute()
        self.assertNotEqual(0, result.returncode)
        self.assertIn("not published", result.stderr)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)

    def test_tag_failure(self):
        # Arrange: source already has a local candidate tag, absent from origin.
        fixture = self.fixture()
        fixture.git("tag", "sub/v0.0.1")
        before = fixture.snapshot()
        # Act / Assert: partial preparation tags exist only in disposable clone.
        result = fixture.execute()
        self.assertNotEqual(0, result.returncode)
        self.assertIn("not published", result.stderr)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)

    def test_dirty_index_and_worktree_rejected(self):
        # Arrange: staged tracked edits and additional unstaged edits.
        fixture = self.fixture()
        (fixture.repo / "go.mod").write_text("module staged.invalid/contexty\n\ngo 1.23\n")
        fixture.git("add", "go.mod")
        (fixture.repo / "go.mod").write_text("module unstaged.invalid/contexty\n\ngo 1.23\n")
        before = fixture.snapshot()
        # Act / Assert: source changes/index are not swept into release.
        result = fixture.execute()
        self.assertNotEqual(0, result.returncode)
        self.assertIn("tracked changes", result.stderr)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)

    def test_invalid_module_traversal(self):
        # Arrange: module list attempts to escape the clone.
        fixture = self.fixture()
        before = fixture.snapshot()
        # Act / Assert: reject before preparing or publishing.
        result = fixture.execute(". ../outside")
        self.assertNotEqual(0, result.returncode)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)

    def test_clean_root_only(self):
        # Arrange: a clean single-module checkout.
        fixture = self.fixture(modules=False)
        before = fixture.snapshot()
        # Act / Assert: root-only exact publication and source preservation.
        result = fixture.execute(".")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual({"refs/tags/v0.0.1"}, set(fixture.tags()))
        self.assert_preserved(fixture, before)


    def test_atomic_unsupported_fails_without_partial_tags(self):
        # Arrange: remote deliberately does not advertise atomic push support.
        fixture = self.fixture()
        command(["git", "--git-dir", str(fixture.origin), "config", "receive.advertiseAtomic", "false"], fixture.repo)
        before = fixture.snapshot()
        # Act / Assert: no fallback publishes any subset of the release refs.
        result = fixture.execute()
        self.assertNotEqual(0, result.returncode)
        self.assertIn("not published", result.stderr)
        self.assertEqual({}, fixture.tags())
        self.assert_preserved(fixture, before)

    def test_published_version_selection(self):
        # Arrange: published root tag and unrelated local version-like tag.
        fixture = self.fixture()
        fixture.git("tag", "-a", "v0.2.7", "-m", "prior release")
        fixture.git("push", "origin", "refs/tags/v0.2.7")
        fixture.git("tag", "v99.0.0")
        before = fixture.snapshot()
        # Act / Assert: use published root version only; no unrelated tag leaks.
        result = fixture.execute()
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertEqual({"refs/tags/v0.2.7", "refs/tags/v0.2.8", "refs/tags/sub/v0.2.8"}, set(fixture.tags()))
        self.assert_preserved(fixture, before)


    def push_failure_after_publication(self, unknown):
        # Arrange: actual local push succeeds, client loses its response.
        fixture = self.fixture()
        binary = shlex.quote(shutil.which("git"))
        wrapper_dir = self.base / "bin"
        wrapper_dir.mkdir()
        marker = shlex.quote(str(self.base / "pushed"))
        wrapper = wrapper_dir / "git"
        wrapper.write_text(
            f'#!/bin/sh\nif [ "$1" = push ]; then\n {binary} "$@" || exit $?\n touch {marker}\n echo "fixture lost push response" >&2\n exit 9\nfi\n'
            + (f'if [ "$1" = ls-remote ] && [ -f {marker} ]; then exit 8; fi\n' if unknown else '')
            + f'exec {binary} "$@"\n')
        wrapper.chmod(0o755)
        fixture.env = dict(ENV, PATH=str(wrapper_dir) + os.pathsep + ENV["PATH"])
        before = fixture.snapshot()
        # Act.
        result = fixture.execute()
        # Assert: failure status with observed published/unknown outcome, no deletion.
        self.assertNotEqual(0, result.returncode)
        expected = "unknown (remote refs could not be observed)" if unknown else "published (all exact refs observed despite push error)"
        self.assertIn(expected, result.stderr)
        self.assertEqual({"refs/tags/v0.0.1", "refs/tags/sub/v0.0.1"}, set(fixture.tags()))
        self.assert_preserved(fixture, before)

    def test_lost_push_response_observed_publication(self):
        self.push_failure_after_publication(False)

    def test_lost_push_response_unknown_outcome(self):
        self.push_failure_after_publication(True)


    def test_interrupted_push_with_transaction_in_flight(self):
        # Arrange: gate the actual remote transaction after objects arrive.
        fixture = self.fixture()
        binary = shlex.quote(shutil.which("git"))
        wrapper_dir = self.base / "bin"
        wrapper_dir.mkdir()
        wrapper = wrapper_dir / "git"
        wrapper.write_text(f'#!/bin/sh\nif [ "$1" = push ]; then\n {binary} "$@"\n exit $?\nfi\nexec {binary} "$@"\n')
        wrapper.chmod(0o755)
        entered, allow, done = (self.base / name for name in ("entered", "allow", "done"))
        hook = fixture.origin / "hooks/pre-receive"
        hook.write_text(f'#!/bin/sh\ntouch {shlex.quote(str(entered))}\nwhile [ ! -f {shlex.quote(str(allow))} ]; do sleep 0.02; done\nexit 0\n')
        hook.chmod(0o755)
        post = fixture.origin / "hooks/post-receive"
        post.write_text(f'#!/bin/sh\ntouch {shlex.quote(str(done))}\n')
        post.chmod(0o755)
        before = fixture.snapshot()
        env = dict(ENV, PATH=str(wrapper_dir) + os.pathsep + ENV["PATH"], TMPDIR=str(fixture.temp))
        process = subprocess.Popen(["bash", "scripts/release.sh", "patch", ". ./sub"], cwd=fixture.repo,
                                   env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                   stderr=subprocess.PIPE, text=True)
        process.stdin.write("y\n")
        process.stdin.flush()
        try:
            deadline = time.monotonic() + 15
            while not entered.exists() and time.monotonic() < deadline:
                time.sleep(0.02)
            self.assertTrue(entered.exists(), "remote transaction did not enter hook")
            # Act: interrupt client while remote publication is still gated.
            process.send_signal(signal.SIGTERM)
            out, err = process.communicate(timeout=15)
            # Assert: missing refs are unknown, not evidence of failed publication.
            self.assertNotEqual(0, process.returncode, out)
            self.assertIn("Publication outcome: unknown", err)
            self.assertNotIn("not published", err)
            self.assertEqual({}, fixture.tags())
        finally:
            allow.touch()
            if process.poll() is None:
                process.kill()
                process.communicate(timeout=15)
        deadline = time.monotonic() + 15
        while not done.exists() and time.monotonic() < deadline:
            time.sleep(0.02)
        self.assertTrue(done.exists(), "remote transaction did not finish")
        self.assertEqual({"refs/tags/v0.0.1", "refs/tags/sub/v0.0.1"}, set(fixture.tags()))
        self.assert_preserved(fixture, before)

    @unittest.skipUnless(os.environ.get("CONTEXTY_VERIFY_REVIEW_SHA") == "1" and os.uname().sysname == "Darwin", "opt-in review SHA reproduction uses BSD sed")
    def test_review_sha_reproduces_untracked_and_tag_defects(self):
        # Arrange: actual old script from the immutable review SHA.
        fixture = self.fixture(modules=False, baseline=True)
        fixture.git("tag", "scratch-local")
        (fixture.repo / "private-untracked.txt").write_text("harmless fixture data\n")
        # Act: old release against local origin.
        result = fixture.execute(".")
        # Assert: behavioral defects, not API compile incompatibility.
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertIn("refs/tags/scratch-local", fixture.tags())
        files = command(["git", "--git-dir", str(fixture.origin), "ls-tree", "--name-only", "v0.0.1"], fixture.repo).stdout
        self.assertIn("private-untracked.txt", files)

    @unittest.skipUnless(os.environ.get("CONTEXTY_VERIFY_REVIEW_SHA") == "1" and os.uname().sysname == "Darwin", "opt-in review SHA reproduction uses BSD sed")
    def test_review_sha_reproduces_detached_failure(self):
        # Arrange: old script + rejecting local bare origin.
        fixture = self.fixture(modules=False, baseline=True)
        fixture.reject()
        # Act.
        result = fixture.execute(".")
        # Assert: original detached-HEAD and leftover-tag defect reproduced.
        self.assertNotEqual(0, result.returncode)
        self.assertEqual("", fixture.git("branch", "--show-current"))
        self.assertIn("v0.0.1", fixture.git("tag", "--list"))
        self.assertEqual({}, fixture.tags())


if __name__ == "__main__":
    unittest.main(verbosity=2)
