.PHONY: all
all: build codesign

.PHONY: codesign
codesign:
	codesign --entitlements linux.entitlements -s - ./bin/linux

.PHONY: build
build:
	go build -o bin/linux ./cmd/linux
