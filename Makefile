.PHONY: build test fmt lint e2e contract contract-live install clean

VERSION ?= dev
CHANNEL ?= dev
COMMIT ?=
DISTRIBUTION ?= source
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.channel=$(CHANNEL) -X main.commit=$(COMMIT) -X main.distribution=$(DISTRIBUTION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" .

test:
	go test -tags=test -race ./...

fmt:
	gofmt -w .

lint:
	@output=$$(gofmt -l .); if [ -n "$$output" ]; then echo "Files not formatted:"; echo "$$output"; exit 1; fi
	go vet ./...
	golangci-lint run
	go run ./tools/commentlint .

e2e:
	go test -tags=e2e,test -timeout=10m ./tests/e2e/...

contract:
	go test -tags=contract -count=1 -timeout=10m ./tests/contract/...

contract-live:
	go test -tags=contract,contractlive -count=1 -timeout=10m ./tests/contract/...

install: build
	cp hand $(GOPATH)/bin/ 2>/dev/null || cp hand ~/.local/bin/

clean:
	rm -f hand
