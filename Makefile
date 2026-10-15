.PHONY: test lint run sim

test:
	go test -race ./...

lint:
	golangci-lint run

run:
	go run ./cmd/dispatch

sim:
	go run ./cmd/sim -drivers 25 -trips 40
