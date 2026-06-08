# Top-level convenience Makefile.
# Application-specific build lives in apps/dblock/Makefile.

DBLOCK_VERSION ?= 0.5.0
DBLOCK_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: help build build-ui openapi-sync test acceptance acceptance-clean deb deb-arm64 test-deb clean

help:
	@echo "Top-level targets:"
	@echo "  build             - build the dblock binary (cd apps/dblock && make build)"
	@echo "  build-ui          - rebuild the SPA + stage it under apps/dblock/internal/api/static"
	@echo "  openapi-sync      - stage the OpenAPI spec for the binary's embedded swagger-ui"
	@echo "  test              - run unit + acceptance tests in Docker"
	@echo "  acceptance        - run acceptance tests in Docker (alias)"
	@echo "  acceptance-clean  - remove the Docker go-mod/go-build cache volumes (M5.9.3)"
	@echo "  deb               - build dist/dblock_<version>_amd64.deb via nfpm"
	@echo "  deb-arm64         - build the arm64 .deb"
	@echo "  test-deb          - smoke-test the .deb in a clean debian:bookworm container"
	@echo "  clean             - remove dist/ and built artefacts"

build:
	$(MAKE) -C apps/dblock build

build-ui:
	$(MAKE) -C apps/dblock build-ui

openapi-sync:
	$(MAKE) -C apps/dblock openapi-sync

test acceptance:
	tests/acceptance/run-in-docker.sh

# M5.9.3 — wipe the persistent go-mod + go-build cache volumes used by
# tests/acceptance/run-in-docker.sh. Idempotent; harmless when the
# volumes already don't exist.
acceptance-clean:
	docker volume rm dblock-gomod-cache dblock-gobuild-cache || true

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
