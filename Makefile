GO ?= go
BIN := bin/dispatch

.PHONY: test race bench lint build run tidy compose integration ci

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

bench:
	$(GO) test -bench=. -benchmem -run='^$$' ./internal/matching

lint:
	golangci-lint run

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags='-s -w' -o $(BIN) ./cmd/dispatch

run: build
	./$(BIN)

tidy:
	$(GO) mod tidy

compose:
	docker compose up --build

integration:
	$(GO) test -race -tags integration ./...

ci: test race lint
