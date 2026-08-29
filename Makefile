.PHONY: build test fmt lint e2e contract contract-live install clean vendorhash

VERSION ?= dev
CHANNEL ?= dev
COMMIT ?=
DISTRIBUTION ?= source
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.channel=$(CHANNEL) -X main.commit=$(COMMIT) -X main.distribution=$(DISTRIBUTION)

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" .

test:
	# 30m covers the measured 4x Windows runner variance while keeping a real hang actionable.
	SECONDHAND_HOME=$$(mktemp -d) go test -tags=test -race -timeout=30m ./...

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

# FAKE_VENDOR_HASH is nixpkgs' lib.fakeHash: a well-formed sha256 SRI hash that
# no real fixed-output derivation will ever produce, so the build below is
# guaranteed to fail and report the real hash in its "got:" line.
FAKE_VENDOR_HASH := sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=

vendorhash:
	@backup=$$(mktemp); cp flake.nix "$$backup"; \
	sed -i 's|vendorHash = "[^"]*"|vendorHash = "$(FAKE_VENDOR_HASH)"|' flake.nix; \
	log=$$(mktemp); \
	nix build .#default --no-link >"$$log" 2>&1; \
	got=$$(grep -oP 'got:\s+\Ksha256-\S+' "$$log" | tail -1); \
	if [ -z "$$got" ]; then \
		echo "could not compute vendorHash; nix build did not fail on a hash mismatch:" >&2; \
		cat "$$log" >&2; \
		cp "$$backup" flake.nix; rm -f "$$backup" "$$log"; exit 1; \
	fi; \
	sed -i "s|vendorHash = \"$(FAKE_VENDOR_HASH)\"|vendorHash = \"$$got\"|" flake.nix; \
	rm -f "$$backup" "$$log"; \
	echo "flake.nix vendorHash updated to $$got"

install: build
	cp hand $(GOPATH)/bin/ 2>/dev/null || cp hand ~/.local/bin/

clean:
	rm -f hand
