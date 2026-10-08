# Repository verification

Make invokes standard Go commands and golangci-lint directly. Modules are discovered
from go.mod files, excluding hidden directories and vendor. All commands use
GOWORK=off. There is no aggregate check target or tool version validation target.

| Command | Scope |
|---|---|
| `make modules` | List all discovered development modules. |
| `make test` | Fresh ordinary race tests across all modules. |
| `make test-integration` | Files with integration build tag; execute TestIntegration… functions only. |
| `make test-e2e` | Files with e2e build tag; execute TestE2E… functions only. |
| `make test-live` | Files with live build tag; execute TestLive… functions only, including paid calls. |
| `make lint` | Formatting diff and lint without rewriting files. |
| `make fix` | Go fix, formatting and lint fixes; modifies files. |
| `make fuzz` | Every discovered fuzz function separately, 30 seconds per function. |
| `make bench` / `make cover` | Benchmarks / per-module coverage. |

Build tags alone do not exclude ordinary test files. Profile targets combine the tag
with a matching test-name prefix so a module without such tests executes none.
Use the same convention for new tests; each profile runs directly through Go too:

```sh
GOWORK=off go test -race -tags=integration -run '^TestIntegration' ./...
GOWORK=off go test -race -tags=e2e -run '^TestE2E' ./...
```

Recipes use tools from PATH and explicitly propagate command failures.
Tool versions are pinned in CI, not enforced by Make. CI and source release gates
run lint, fresh unit tests, integration and e2e sequentially.

## Contexty profiles

All four modules are discovered, tested and published: core, PostgreSQL, Redis and
integration/chat. Store modules and the chat consumer retain development replacements
for core; GOWORK=off makes these dependencies explicit. Prompty is pinned to its
published v0.15.0; no adjacent checkout is required.

PostgreSQL, Redis standalone and Redis Cluster use integration-tagged tests. They
start isolated containers and clean them with t.Cleanup. Docker daemon availability
is mandatory; selected tests fail rather than skip when prerequisites are missing.
The published v0.12.0 compatibility subset runs in an isolated module with no replaces;
it does not prove the current source contract.

The offline chat consumer uses e2e for persistence/restore and terminal history CAS,
including cancellation and concurrent writers. Mapping and focused adversarial cases
remain ordinary tests. Its recipe is also executed by an e2e test.
Contract acceptance and allocation guardrails remain ordinary root tests. Benchmarks
and extended fuzz campaigns are separate. Live evaluation unit tests use fake host
adapters and incur no API charges; no paid provider test is currently supplied.

The tooling package tests the actual Bash release against disposable bare repositories.
It belongs to the root module and adds no runtime executable or module.
CI pins Go 1.27.2 and golangci-lint 2.14.0; local commands use tools from PATH.
No Python or PDF runtime is required for verification or release. The historical
scripts/reproduce_review.py remains an optional archived-review reproduction tool.
