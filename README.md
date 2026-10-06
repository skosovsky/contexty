# contexty

A Go library for typed LLM context: durable message identity, prompt/checkpoint
projections, token budgets, resources, lineage and optimistic checkpoint storage.
The host supplies its own metadata, tokenizer, policies and callbacks. Tool
execution, model clients, retries and agent loops stay in the host.

## Installation

This contract makes a clear break in explicit target composition, selection and export,
budget configuration, compile-only selectors,
deferred callback results and transformation status. See the
[consumer migration guide](docs/migration.md) before updating stored state
or callers; old checkpoint/OCC namespaces may need explicit host migration.

```bash
go get github.com/skosovsky/contexty
```

Requires Go 1.27.1+.

`make lint` runs the pinned golangci-lint release through Go; no separate global
linter installation is required. CI uses the same release.

## Quick start

```sh
go run ./examples/quickstart
```

The [complete example](examples/quickstart/main.go) handles every error, gives
static messages explicit IDs and current events a durable TurnID, supplies an
explicit estimator, and commits with the loaded OCC version. It prints separate
prompt-safe and raw checkpoint content. The bundled memory backend and structural
estimator are reference implementations; hosts choose durability and tokenization.

Compile does not commit state. Retry the same logical turn with the same TurnID;
assign a new TurnID to a new event. Nil System/History/Memory inherits stored data;
a nonnil empty slice clears that segment for this compile. Tools is request-only.
Returned result DTOs are owned mutable copies; ConversationState is a private
immutable view with defensive getters.

## Documentation

- [API guide and examples](docs/api-guide.md): messages, tools, deferred blocks, views, targets and wire contracts.
- [Glossary](docs/glossary.md), [migration](docs/migration.md), [contracts](docs/contracts.md).
- [Budget and retention](docs/retention-budget.md), [checkpoint stores](docs/checkpoint-store.md).
- [Projections](docs/context-projections.md), [output policy](docs/output-policy.md), [opaque state](docs/opaque-state.md).
- [Resources](docs/resource-content.md), [blob retention](docs/blob-retention.md), [prefix diagnostics](docs/prefix-diagnostics.md).
- [Release and recovery](docs/release.md), [ownership/concurrency matrix](docs/remediation-contracts.md#ownership-and-concurrency-matrix).
- [Review decisions](docs/remediation-decisions.md) and [measurements](docs/remediation-evidence/stage6/measurements.md).

## Development

```sh
make validate GOLANGCI_LINT=golangci-lint
```

Use pinned golangci-lint 2.14.0 and Docker for isolated Redis/Postgres integration
tests. Validate runs lint, acceptance, benchmark guardrails, race suites in all
three modules and local Git release fixtures. No production endpoint is needed.
