# Infrastructure migration verification

Validated working tree on local main, based on 912c041. Reference:
[ragy 91e3ff2cdc87c49b5ad9d1cf0afa23d35832521e](https://github.com/skosovsky/ragy/tree/91e3ff2cdc87c49b5ad9d1cf0afa23d35832521e).

## Changes and deviations

- Makefile and scripts/release.sh are byte-identical to the pinned reference.
  All four modules share automatic hidden/vendor-excluding discovery, GOWORK=off,
  standard Make commands and the same source release gate.
- CI keeps the reference actions and Go 1.27.2 / golangci-lint 2.14.0 pins, downloads
  all module dependencies and caches all go.sum files. The ragy PDF/Python step is
  replaced by a Docker prerequisite. Only main and root version tags trigger CI;
  contents: read and persist-credentials: false are retained.
- The chat module has a development core replacement and published prompty v0.15.0.
  PostgreSQL/Redis integration and chat e2e tests use matching build tags and name
  prefixes. The Docker SDK is promoted from indirect to direct test dependency to
  ping the explicitly selected daemon without testcontainers fallback.
- Lint keeps the reference common rules, removes ragy-specific type/path exceptions,
  and uses contexty's local prefix. The sole contexty type exception is Message,
  whose constructors/codecs intentionally fill optional host fields incrementally.
  The runnable recipe has one explained console-output suppression. Private consumer
  helpers are refactored and literals made explicit; public APIs are preserved.
- Release fixtures live in the root tooling test package rather than a separate
  tooling module. The old published baseline is a separate Go integration fixture.
  Python release/consumer entrypoints and legacy Make gates are removed. Historical
  review evidence and the optional reproduce_review.py are preserved.

## Verification results

| Check | macOS arm64, Go 1.27.1 | Linux amd64, Go 1.27.2 |
|---|---|---|
| make lint | PASS, all four modules, 0 issues | PASS, all four modules, 0 issues |
| make test | PASS, fresh race tests | PASS |
| make test-integration | PASS | PASS |
| make test-e2e | PASS | PASS |
| Bash release fixtures | PASS, Bash 5.3.15 | PASS |

Additional macOS checks: release fixtures on the shebang-selected /bin/bash 3.2.57
(PASS), actionlint 1.7.7, bash -n, golangci-lint config verify,
Git diff whitespace checks and lint with integration/e2e build tags: PASS.
The only t.Skip in the repository is a fuzz-input filter outside required profiles;
required test profiles contain no prerequisite skips.

Integration runs real PostgreSQL, Redis standalone and Redis Cluster containers;
network-dependent published baseline failures propagate. E2e runs persistence/restore,
terminal history with cancellation/retry, simultaneous CAS and the offline recipe.
No live-provider or paid campaign was executed.

Final Linux timings: core race suite 40.199s, release/tooling fixtures 192.198s,
published baseline 23.007s, PostgreSQL integration 2.993s, Redis standalone/Cluster
integration 14.697s, chat e2e 3.401s. The complete sequential Linux run exited 0.

Docker negative check: setting DOCKER_HOST=unix:///tmp/contexty-no-docker.sock
makes TestIntegrationStore fail immediately with a Docker prerequisite error;
it does not skip or silently select another daemon.

The 19 release test functions exercise the actual Bash script against temporary bare
repositories: exact source main / manifest-only candidate tags, nested module paths,
caller HEAD/index/untracked preservation, each required gate failure, collisions,
atomic push refusal, changed candidates, ambiguous delivery, interrupted preparation,
confirmation refusal and inspect/resume/finish. A separate Make fixture checks module
discovery outside adapters and hidden/vendor exclusions. No production remote is used.

Linux uses an isolated /tmp copy, the available golang:1.27.1-bookworm image with
GOTOOLCHAIN=go1.27.2, and golangci-lint built with Go 1.27.2. The Docker socket is
mounted; TESTCONTAINERS_HOST_OVERRIDE=host.docker.internal makes published container
ports reachable from the verification container. The requested 1.27.2 image tag is
unavailable, but the exact 1.27.2 toolchain resolves successfully. This run is under
Docker Desktop amd64 emulation, rather than a native Linux host or GitHub Actions.

## Publication boundary

Production release, push and history repair were not performed. Remote main still
points at cdda9b30fcdf163a2265578592a12e1762f193e5. Local main contains its amended
replacement and therefore is not a fast-forward continuation of remote main.
The release script rejects this situation before publication; history must be
resolved separately before a future production release. All discovered modules,
including integration/chat, receive prepared candidate tags in the new protocol.
