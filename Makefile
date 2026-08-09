.PHONY: build test fmt lint e2e contract install clean

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" .

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	@output=$$(gofmt -l .); if [ -n "$$output" ]; then echo "Files not formatted:"; echo "$$output"; exit 1; fi
	go vet ./...
	golangci-lint run
	go run ./tools/commentlint .

e2e:
	go test -tags=e2e -timeout=10m ./tests/e2e/...

# Runs against the real herdr, treehouse and gh, skipping whichever is absent.
contract:
	go test -tags=contract -count=1 -timeout=10m ./tests/contract/...

install: build
	cp hand $(GOPATH)/bin/ 2>/dev/null || cp hand ~/.local/bin/

clean:
	rm -f hand
