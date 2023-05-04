.PHONY: all
all: build codesign

.PHONY: codesign
codesign:
	codesign --entitlements linux.entitlements -s - ./bin/linux || true

.PHONY: build
build:
	GOOS=darwin go build -o bin/linux ./cmd/linux

test-build: build
	cp bin/linux ~/mac/tmp
.PHONY: test-build

.PHONY: build-release
build-release: compile-release codesign

.PHONY: compile-release
compile-release:
	go build -ldflags "-X main.Version=$$VERSION" -o bin/linux ./cmd/linux

.PHONY: cli-release
cli-release: build-release
	gon -log-level=info ./gon.hcl
	mv output/linux.zip output/linux-$$VERSION-$$(go env GOARCH).zip

.PHONY: os-release
os-release: binaries
	cd os && sudo make release VERSION=$$VERSION PLATFORM=$$(go env GOARCH)

binaries: os/isle-guest os/isle-helper os/containerd-clog-logger
.PHONY: binaries

test-release: clean os/isle-guest os/isle-helper
	cd os && sudo make release VERSION=0.0.0 PLATFORM=$$(go env GOARCH)
	mv os/os-v0.0.0-$$(go env GOARCH).tar.gz ~/mac/tmp

clean:
	rm -rf os/isle-guest os/isle-helper

.PHONY: release
release: cli-release os-release

os/isle-guest:
	GOOS=linux CGO_ENABLED=0 go build -o os/isle-guest ./cmd/isle-guest

.PHONY: os/isle-guest

os/containerd-clog-logger:
	GOOS=linux CGO_ENABLED=0 go build -o os/containerd-clog-logger ./pkg/clog/containerd-clog-logger

test-guest: os/isle-guest
	cp os/isle-guest ~/mac/tmp

os/isle-helper:
	GOOS=linux CGO_ENABLED=0 go build -o os/isle-helper ./cmd/isle-helper

.PHONY: os/isle-helper

test-helper: os/isle-helper
	cp os/isle-helper ~/mac/tmp
