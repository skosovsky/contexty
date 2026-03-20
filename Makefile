.PHONY: test lint bench fuzz cover

test:
	@go test -race -count=1 ./...

lint:
	@mkdir -p "$(CURDIR)/.cache/go-build" "$(CURDIR)/.cache/golangci-lint"
	@env GOFLAGS=-buildvcs=false GOCACHE="$(CURDIR)/.cache/go-build" GOLANGCI_LINT_CACHE="$(CURDIR)/.cache/golangci-lint" golangci-lint run ./...

bench:
	@go test -bench=. -benchmem ./...

fuzz:
	@go test -fuzz=. -fuzztime=30s .

cover:
	@go test -coverprofile=coverage.out -covermode=atomic ./...
	@go tool cover -func=coverage.out
