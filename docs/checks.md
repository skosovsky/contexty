# Local, CI and release check contract

`scripts/checks.json` is the versioned authority for toolchain pins, test and release
module inventories, consumer peers, lane commands, prerequisites and profiles.
`python3 scripts/check.py --profile check --plan` prints the machine-readable plan.
GitHub Actions supplies Linux and Docker, then invokes `make check`.

- `make test-fast`: explicitly reduced development cycle; excluded lanes are SKIP.
- `make test`: required fresh race suites, adapter integration, consumer semantics,
  acceptance, allocation guardrails and Python gate/release fixtures.
- `make lint`: config verification, formatter diff verification and the pinned linter.
- `make check` / `make validate`: complete prerelease profile including generation
  drift and examples. Independent failures are collected; dependent lanes BLOCKED.
- `make check-linux`: same registry and runner in the pinned Linux Go container,
  using the host Docker daemon for actual PostgreSQL/Redis containers.

Every selected mandatory lane must PASS. Missing Docker, a wrong toolchain,
network failure, required skipped tests or missing matching tests cannot yield a
successful full gate. Reports record status, duration, command, source digest,
versions and resolved module graphs. Shard results and the fast profile are not
full-gate evidence. Linux results are required before release; macOS results carry
only their recorded platform scope.

All Go commands run with GOWORK=off. Adapter tests execute in their own modules.
Source consumers use isolated explicit replacements; candidate consumers use an
immutable local artifact proxy. Public baseline and release verification force
the public Go proxy and checksum database, with no local replacements. The
v0.12.0 baseline checks only its documented old subset; it never establishes the
new candidate contract. Supported prompty is pinned to an exact tag and SHA.

The checked-in `.golangci.yml` is the shared linter baseline and its digest is
verified by the registry. It uses `exhaustruct_v5`, matching golangci-lint 2.14.0.
The local module-prefix setting and local development replacement allowance are
repository-specific. Test exclusions cover fixture readability (including partial
structs, repeated assertion setup and ignored cleanup errors); examples and `integration/chat/cmd/recipe` allow
`fmt` for readable executable output. The exclusions remain visible in the config;
no external mutable config is fetched during checks.

Live-provider checks require host credentials and explicit provider wiring, and
are manual. Extended fuzz exploration and performance measurements are separate
from the mandatory allocation guardrails. They are listed in the registry rather
than implied by a full prerelease PASS. No provider SDK is introduced into core.
