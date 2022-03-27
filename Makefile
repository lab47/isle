.PHONY: all
all: build

.PHONY: codesign
codesign:
	codesign --entitlements linux.entitlements -s - ./bin/linux || true

.PHONY: build
build: codesign
	go build -o bin/linux ./cmd/linux

.PHONY: build-release
build-release: compile-release codesign

.PHONY: compile-release
compile-release:
	go build -ldflags "-X main.Version=$$VERSION" -o bin/linux ./cmd/linux

.PHONY: package-os
package-os:
	tar -C release -czvf output/os-$$VERSION-$$(go env GOARCH).tar.gz .

.PHONY: release
release: package-os build-release
	gon -log-level=info ./gon.hcl
	mv output/linux.zip output/linux-$$VERSION-$$(go env GOARCH).zip
