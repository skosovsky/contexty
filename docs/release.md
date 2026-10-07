# Release tooling contract

Run `make release-patch` / `make release-break` from repository root with Bash,
Git, the registry-pinned Go toolchain and Python 3.9+. Both invoke the same release
entrypoint; it enforces the full gate itself, including direct script invocations.
No library runtime dependency on peer build tooling is introduced.

The release inventory in `scripts/checks.json` is authoritative. The release uses
committed HEAD in an isolated temporary clone. Tracked changes are rejected;
untracked files are never copied. The original branch, HEAD, index, worktree and
local tags remain unchanged. Source identity and tracked cleanliness are checked
again after the gate and immediately before publication.

Preparation removes own-module sibling replacements and pins every own-module
requirement to the candidate version. Only the tracked release go.mod files are
staged; root and all public submodule tags target the same immutable candidate
commit. Identity and commit/tag signing settings are copied from the caller;
signing failure blocks publication. No source branch is pushed.

The candidate proxy consists of deterministic module ZIP/go.mod artifacts built
from tracked bytes, excluding nested modules, vendor content and symlinks. Go
validates those artifacts and computes their module/checksum hashes. The shared
`check` profile runs against that committed candidate with an isolated artifact
cache and exact candidate version. Candidate artifacts bypass the public checksum
service only for own module paths; public peers retain checksum verification.
Candidate worktree/index/HEAD or proxy mutations invalidate the successful check.
Reports are retained outside the temporary checkout under
`.git/contexty-release-evidence/VERSION/`.

Versions derive from current published root vMAJOR.MINOR.PATCH tags. Local
candidate tag conflicts fail preparation. Publication uses exact tag refspecs and
`git push --atomic`; lack of atomic support never triggers a fallback. Cleanup
removes only the temporary clone and never changes or deletes remote tags.

Before push, `.git/contexty-release-state.json` records source/candidate SHA,
exact tag object IDs, version, candidate module hashes and evidence location.
After publication the shared `published` profile verifies the exact public version
with GOWORK=off, no local replacements, public checksum service, bounded proxy
retries, module hashes matching the candidate and consumer smoke. Failed public
verification returns failure even when all tags were published.

On push failure, exact remote refs are observed. All matching object IDs mean
publication occurred despite client failure; none after a completed failed push
means not published; interruption, mixed/conflicting or unavailable observations
remain unknown. Unknown or published-but-unverified state blocks a new release.
Never rewrite or remove published tags to recover.

Run `scripts/release.sh recover VERSION` to observe the saved exact refs and rerun
public verification from their immutable released commit. Recovery never pushes
or mutates tags. Missing/conflicting refs remain an explicit failure requiring
remote inspection. A definitively rejected push can safely retry the normal
make release target with the still-free version.

`make test-release` exercises temporary local bare origins only: gate failures,
source/candidate mutation, original-checkout preservation, preparation/signing
boundaries, exact atomic refs, push rejection/interruption/lost response and
postpublication recovery. Fixture gate doubles test release orchestration; they
do not claim public checksum or real consumer coverage. The real shared gate
provides those checks. Optional immutable old-SHA reproduction fixtures remain
separate historical checks.
