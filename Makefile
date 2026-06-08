# Top-level convenience Makefile.
# Application-specific build lives in apps/dblock/Makefile.

DBLOCK_VERSION ?= 0.5.0
DBLOCK_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: help build test acceptance deb deb-arm64 test-deb clean

help:
	@echo "Top-level targets:"
	@echo "  build         - build the dblock binary (cd apps/dblock && make build)"
	@echo "  test          - run unit + acceptance tests in Docker"
	@echo "  acceptance    - run acceptance tests in Docker (alias)"
	@echo "  deb           - build dist/dblock_<version>_amd64.deb via nfpm"
	@echo "  deb-arm64     - build the arm64 .deb"
	@echo "  test-deb      - smoke-test the .deb in a clean debian:bookworm container"
	@echo "  clean         - remove dist/ and built artefacts"

build:
	$(MAKE) -C apps/dblock build

test acceptance:
	tests/acceptance/run-in-docker.sh

deb: build
	@mkdir -p dist
	DBLOCK_VERSION=$(DBLOCK_VERSION) DBLOCK_COMMIT=$(DBLOCK_COMMIT) \
		nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/

deb-arm64: build
	@mkdir -p dist
	DBLOCK_VERSION=$(DBLOCK_VERSION) DBLOCK_COMMIT=$(DBLOCK_COMMIT) \
		nfpm pkg --packager deb --config packaging/nfpm.yaml \
		--target dist/ --arch arm64

test-deb:
	packaging/test-deb.sh

clean:
	rm -rf dist
	$(MAKE) -C apps/dblock clean
