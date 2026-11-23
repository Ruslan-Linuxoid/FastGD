DC   := docker compose
DRUN := $(DC) run --rm -T -u "$$(id -u):$$(id -g)" dev

VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)

TESTFLAGS ?=
E2EFLAGS  ?=

.PHONY: image build test e2e

image:
	$(DC) build --quiet dev

build: image ## bin/fastgd, a static binary
	$(DRUN) env CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o bin/fastgd ./cmd/fastgd

test: image ## the tests of every package, with -race
	$(DRUN) go test -race -timeout 30m $(TESTFLAGS) ./...

e2e: build ## the daemon with a real FUSE mount against an emulator of Drive
	$(DRUN) go test -c -tags e2e -o bin/e2e.test ./e2e
	$(DC) run --rm -T e2e bin/e2e.test -test.timeout 60m $(E2EFLAGS)
