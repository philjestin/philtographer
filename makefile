.PHONY: build run test clean

APP := philtographer
MAIN := ./cmd/rg

build:
	go build -o bin/$(APP) $(MAIN)

run:
	go run $(MAIN) $(ARGS)

test:
	go test ./...

clean:
	rm -rf bin
