GO      := go
GOLANGCI_LINT_CACHE ?= $(shell python3 -c 'import tempfile; print(tempfile.gettempdir() + "/contexty-golangci-cache")')
GOLANGCI_LINT_VERSION := $(shell python3 -c 'import json; print(json.load(open("scripts/checks.json"))["toolchain"]["linter"])')
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT_RUN := env GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run --allow-parallel-runners
MODULES := $(shell python3 scripts/check.py --modules release --modules-format plain)

.PHONY: lint fix test test-fast check check-plan check-linux test-acceptance test-release validate bench bench-guardrails bench-hotpath fuzz cover release-patch release-break

lint:
	@python3 scripts/check.py --profile lint

fix:
	@if [ -f "go.work" ]; then $(GO) work sync; fi
	@for dir in $(MODULES); do \
		echo "fix & tidy - $$dir"; \
		(cd "$$dir" && $(GO) fix ./... && $(GO) mod tidy) || exit 1; \
		(cd "$$dir" && $(GOLANGCI_LINT_RUN) --fix ./...) || exit 1; \
	done

test-fast:
	@python3 scripts/check.py --profile fast

test:
	@python3 scripts/check.py --profile test

check:
	@python3 scripts/check.py --profile check

check-plan:
	@python3 scripts/check.py --profile check --plan

check-linux:
	@python3 scripts/check_linux.py

test-acceptance:
	@python3 scripts/check.py --profile test --shard acceptance

test-release:
	@python3 scripts/check.py --profile test --shard release-fixtures

validate: check

bench:
	@for dir in $(MODULES); do \
		echo "bench - $$dir"; \
		(cd "$$dir" && $(GO) test -bench=. -benchmem -run=^$$ ./...) || exit 1; \
	done

bench-guardrails:
	@python3 scripts/check.py --profile test --shard bench-guardrails

fuzz:
	@for dir in $(MODULES); do \
		echo "fuzz - $$dir"; \
		(cd "$$dir" && \
			for pkg in $$($(GO) list -tags=fuzz ./...); do \
				if $(GO) test -tags=fuzz -list . "$$pkg" 2>/dev/null | grep -q '^Fuzz'; then \
					$(GO) test -tags=fuzz -fuzz=. -fuzztime=30s "$$pkg" || exit 1; \
				fi; \
			done \
		) || exit 1; \
	done

cover:
	@for dir in $(MODULES); do \
		echo "cover - $$dir"; \
		(cd "$$dir" && $(GO) test -coverprofile=coverage.out ./... && $(GO) tool cover -func=coverage.out) || exit 1; \
	done

release-patch:
	@bash ./scripts/release.sh patch "$(MODULES)"

release-break:
	@bash ./scripts/release.sh break "$(MODULES)"
