.PHONY: build test fmt lint e2e install clean

VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o hand .

test:
	go test -race ./...

fmt:
	gofmt -w .

lint:
	gofmt -l .
	go vet ./...
	golangci-lint run

e2e:
	go test -tags=e2e -timeout=10m ./tests/e2e/...

install: build
	cp hand $(GOPATH)/bin/ 2>/dev/null || cp hand ~/.local/bin/

clean:
	rm -f hand
