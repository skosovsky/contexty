.PHONY: test lint fix tidy bench fuzz cover

# Import path patterns for every module in go.work. A bare ./... from the repo root only matches the main module.
MODULE_PKGS := $(shell go list -m -f '{{.Path}}/...')


test:
	@go test -race -count=1 $(MODULE_PKGS)


lint:
	@go list -m -f '{{.Dir}}' | while IFS= read -r dir; do \
		echo "$$dir"; \
		(cd "$$dir" && golangci-lint run ./...) || exit 1; \
	done


fix:
	@go fix $(MODULE_PKGS)
	@go list -m -f '{{.Dir}}' | while IFS= read -r dir; do \
		(cd "$$dir" && go mod tidy) || exit 1; \
	done
	@go work sync
	@go list -m -f '{{.Dir}}' | while IFS= read -r dir; do \
		(cd "$$dir" && golangci-lint fmt ./... && golangci-lint run --fix ./...) || exit 1; \
	done


bench:
	@go test -bench=. -benchmem $(MODULE_PKGS)

cover:
	@go test -coverprofile=coverage.out -covermode=atomic $(MODULE_PKGS)
	@go tool cover -func=coverage.out

fuzz:
	@go list -m -f '{{.Dir}}' | while IFS= read -r dir; do \
		if ! grep -r --include='*_test.go' -l 'func Fuzz' "$$dir" >/dev/null 2>&1; then \
			continue; \
		fi; \
		echo "$$dir"; \
		( cd "$$dir" && \
			for pkg in $$(go list ./...); do \
				if go test -list . "$$pkg" 2>/dev/null | grep -q '^Fuzz'; then \
					echo "    $$pkg"; \
					go test -fuzz=. -fuzztime=30s "$$pkg" || exit 1; \
				fi; \
			done ) || exit 1; \
	done
