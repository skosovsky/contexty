GO      := go
GOLANGCI_LINT_CACHE ?= /private/tmp/contexty-golangci-cache
GOLANGCI_LINT_VERSION := v2.14.0
GOLANGCI_LINT ?= $(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
GOLANGCI_LINT_RUN := env GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) $(GOLANGCI_LINT) run --allow-parallel-runners
MODULES := $(shell find . -type d \( -name ".*" -not -name "." -o -name "vendor" \) -prune -o -type f -name "go.mod" -exec dirname {} \;)
ACCEPTANCE_TESTS := ^(TestAcceptance_|TestApplyMergePolicy_|TestDropHeadStrategy_AtomicityOptOut$$|TestBudgetPipeline_RejectsStrategyOrphans$$|TestObserver_|TestArchitecture_|TestStateless)

.PHONY: lint fix test test-acceptance test-release validate bench bench-guardrails bench-hotpath fuzz cover release-patch release-break

lint:
	@for dir in $(MODULES); do \
		echo "golangci-lint - $$dir"; \
		(cd "$$dir" && $(GOLANGCI_LINT_RUN) ./...) || exit 1; \
	done

fix:
	@if [ -f "go.work" ]; then $(GO) work sync; fi
	@for dir in $(MODULES); do \
		echo "fix & tidy - $$dir"; \
		(cd "$$dir" && $(GO) fix ./... && $(GO) mod tidy) || exit 1; \
		(cd "$$dir" && $(GOLANGCI_LINT_RUN) --fix ./...) || exit 1; \
	done

test:
	@for dir in $(MODULES); do \
		echo "test - $$dir"; \
		(cd "$$dir" && $(GO) test -v -race ./...) || exit 1; \
	done

test-acceptance:
	@echo "test-acceptance - root"
	@tests=$$($(GO) test -list '^TestAcceptance_' .) || exit 1; \
		if ! echo "$$tests" | grep -q '^TestAcceptance_'; then \
			echo "No acceptance tests found" >&2; exit 1; \
		fi
	@$(GO) test -v -race -run='$(ACCEPTANCE_TESTS)' ./...

test-release:
	@python3 scripts/test_release.py

validate: lint test-acceptance bench-guardrails test test-release

bench:
	@for dir in $(MODULES); do \
		echo "bench - $$dir"; \
		(cd "$$dir" && $(GO) test -bench=. -benchmem -run=^$$ ./...) || exit 1; \
	done

bench-guardrails:
	@echo "bench-guardrails - root"
	@$(GO) test -bench=. -benchmem -run='^TestBenchGuardrails_' ./...

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

release-patch: validate
	@chmod +x ./scripts/release.sh
	@./scripts/release.sh patch "$(MODULES)"

release-break: validate
	@chmod +x ./scripts/release.sh
	@./scripts/release.sh break "$(MODULES)"
