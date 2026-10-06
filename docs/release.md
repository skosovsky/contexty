# Release tooling contract

Task 24 stage 2 fixes F01/F02 and adopts D44. Run release from repository root
with Bash, Git, Go and Python 3.9+ on macOS or Linux. Python handles module rewriting
through `go mod edit`; BSD-specific sed is not used.

`make release-patch` / `make release-break` run `make validate` first. Direct
script execution is a low-level publication command and requires the caller to
have completed those gates. The script asks confirmation of the computed version.

Release uses committed HEAD in an isolated temporary clone. Tracked changes are
rejected; untracked files are never copied into the release. The original branch,
HEAD, index, worktree and local tags remain unchanged on success and failure.
The temporary clone receives the source repository's effective user identity and
commit/tag signing settings. Signing errors fail before publication.
Only explicitly selected tracked go.mod files are staged after removing local
sibling replacements and updating v0.0.0 sibling dependencies. Root and selected
submodule tags point to the same release commit. No source branch is pushed.

Version selection uses published root vMAJOR.MINOR.PATCH tags from origin. A
pre-existing local candidate tag in the isolated clone is an explicit preparation
error. Root must be included in the module list; module directories are relative,
tracked, unique and cannot contain whitespace or traverse outside the checkout.
Module paths must share the root module namespace.

Publication uses exact tag refspecs and `git push --atomic`. A remote without
atomic support fails safely; there is no partial-push fallback. Cleanup removes
only this invocation's temporary clone, including its temporary local tags. It
never deletes remote refs or changes the original checkout.

On push failure, the tool queries exact remote refs. If all match the exact prepared local tag object IDs (including signed tags),
it reports publication observed despite the push error; if none exist after a
completed failed push it reports not published; after interruption absence remains
unknown because a remote transaction may still be running; mixed/conflicting or unavailable observations are unknown.
The exit status remains failure. For unknown outcomes inspect the reported refs
before retrying. Do not blindly remove tags or assume the publication failed.
Failure before push is reported as not published. A retry after a rejected push
uses the same version because the source and origin were preserved.

Fixtures run only against temporary local bare origins and verify success,
untracked/tag isolation, multiple modules, rejecting push, preparation/tag errors,
retry, exact refs and cleanup. Running the fixtures never publishes this library.

Run `make test-release` for current behavior. On macOS,
`CONTEXTY_VERIFY_REVIEW_SHA=1 python3 scripts/test_release.py` additionally
reproduces F01/F02 from review SHA (requires that commit in local history).
