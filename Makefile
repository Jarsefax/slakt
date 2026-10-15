.DEFAULT_GOAL := build

VERSION ?=
BUILD_VERSION = $(if $(VERSION),$(VERSION),$(shell bash scripts/version.sh))
RELEASE_VERSION = $(if $(VERSION),$(VERSION),$(shell cat cmd/slkt/VERSION))
GO ?= go
BUMP ?= patch
LDFLAGS = -s -w -X main.version=$(BUILD_VERSION)

.PHONY: build test race vet release check-version prepare-update

prepare-update:
	bash scripts/prepare-update.sh $(BUMP)

check-version:
	VERSION='$(VERSION)' bash scripts/check-version.sh

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/slkt ./cmd/slkt

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

release: check-version
	mkdir -p dist/slkt_linux_amd64/docs
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '-s -w -X main.version=$(RELEASE_VERSION)' -o dist/slkt_linux_amd64/slkt ./cmd/slkt
	VERSION='$(RELEASE_VERSION)' bash scripts/check-version.sh
	cp README.md dist/slkt_linux_amd64/
	cp docs/slack-app.md docs/slack-app-manifest.json docs/plan.md docs/implementation-handoff.md docs/THIRD_PARTY_NOTICES.md dist/slkt_linux_amd64/docs/
	tar -czf dist/slkt_$(RELEASE_VERSION)_linux_amd64.tar.gz -C dist/slkt_linux_amd64 slkt README.md docs
	cd dist && sha256sum slkt_$(RELEASE_VERSION)_linux_amd64.tar.gz > slkt_$(RELEASE_VERSION)_linux_amd64.tar.gz.sha256
