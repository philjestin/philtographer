.PHONY: build run test

build:
	go build -o bin/rg ./cmd/rg

run:
	go run ./cmd/rg -root .

test:
	go test ./...
