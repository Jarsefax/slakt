VERSION ?= 0.1.0-dev
GO ?= go
LDFLAGS = -s -w -X main.version=$(VERSION)

.PHONY: build test race vet release

build:
	mkdir -p bin
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o bin/slkt ./cmd/slkt

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

release:
	mkdir -p dist/slkt_linux_amd64/docs
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/slkt_linux_amd64/slkt ./cmd/slkt
	cp README.md dist/slkt_linux_amd64/
	cp docs/slack-app.md docs/slack-app-manifest.json docs/plan.md docs/implementation-handoff.md docs/THIRD_PARTY_NOTICES.md dist/slkt_linux_amd64/docs/
	tar -czf dist/slkt_$(VERSION)_linux_amd64.tar.gz -C dist/slkt_linux_amd64 slkt README.md docs
	cd dist && sha256sum slkt_$(VERSION)_linux_amd64.tar.gz > slkt_$(VERSION)_linux_amd64.tar.gz.sha256
