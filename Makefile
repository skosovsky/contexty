GO      := go
GOLANGCI_LINT_CACHE ?= /private/tmp/contexty-golangci-cache
GOLANGCI_LINT_RUN := env GOLANGCI_LINT_CACHE=$(GOLANGCI_LINT_CACHE) golangci-lint run --allow-parallel-runners
MODULES := $(shell find . -type d \( -name ".*" -not -name "." -o -name "vendor" \) -prune -o -type f -name "go.mod" -exec dirname {} \;)

.PHONY: lint fix test test-dod validate bench bench-guardrails bench-hotpath fuzz cover release-patch release-break

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

test-dod:
	@echo "test-dod - root"
	@$(GO) test -v -race -run='TestDoD_|TestApplyMergePolicy_|TestDropHeadStrategy_AtomicityOptOut|TestBudgetPipeline_RepairsStrategyOrphans|TestObserver_|TestArchitecture_|TestStateless' ./...

validate: lint test-dod bench-guardrails test

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

release-patch: lint test
	@chmod +x ./scripts/release.sh
	@./scripts/release.sh patch "$(MODULES)"

release-break: lint test
	@chmod +x ./scripts/release.sh
	@./scripts/release.sh break "$(MODULES)"
