.PHONY:
build:
	bash build.sh

.PHONY:
release: build
	tar -C release -czf os-$$VERSION-$$PLATFORM.tar.gz .
