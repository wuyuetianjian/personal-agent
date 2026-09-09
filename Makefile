VERSION ?= 1.0.0

ifeq ($(OS),Windows_NT)
COMMIT ?= $(shell git rev-parse --short HEAD 2>NUL)
BUILD_DATE ?= $(shell powershell -NoProfile -Command "[DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')")
else
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
endif

LDFLAGS := -X agent/internal/version.Version=$(VERSION) \
           -X agent/internal/version.Commit=$(COMMIT) \
           -X agent/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build test smoke release-build checksums release-check clean

build:
	mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/pachat ./cmd/pachat
	cp configs/config.example.yaml bin/config.example.yaml

test:
	go test ./...

smoke:
	go run ./cmd/pachat run --config configs/config.example.yaml --task "smoke test"

release-build:
	./scripts/build-release.sh "$(VERSION)" "$(COMMIT)" "$(BUILD_DATE)"

checksums:
	./scripts/checksums.sh

release-check:
	go run ./cmd/pachat release check

clean:
	rm -rf bin dist
