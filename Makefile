.PHONY: all
all: build codesign

.PHONY: codesign
codesign:
	codesign --entitlements linux.entitlements -s - ./bin/linux || true

.PHONY: build
build:
	go build -o bin/linux ./cmd/linux

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
os-release: os/isle-guest os/isle-helper
	cd os && sudo make release VERSION=$$VERSION PLATFORM=$$(go env GOARCH)

.PHONY: release
release: cli-release os-release

os/isle-guest:
	GOOS=linux CGO_ENABLED=0 go build -o os/isle-guest ./cmd/isle-guest

os/isle-helper:
	GOOS=linux CGO_ENABLED=0 go build -o os/isle-helper ./cmd/isle-helper
