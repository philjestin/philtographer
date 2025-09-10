.PHONY: build run test clean

APP := philtographer          # binary name
MAIN := ./cmd/rg              # <-- set this to your main package dir

build:
	go build -o bin/$(APP) $(MAIN)

run:
	go run $(MAIN) $(ARGS)

test:
	go test ./...

clean:
	rm -rf bin
