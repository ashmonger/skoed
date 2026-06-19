# Top-level convenience Makefile.
# Application-specific build lives in apps/skoed/Makefile.

SKOED_VERSION ?= 0.5.0
SKOED_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

.PHONY: help build build-ui build-extension openapi-sync dev test acceptance acceptance-clean deb deb-arm64 apk apk-arm64 test-deb helm-lint clean

help:
	@echo "Top-level targets:"
	@echo "  build             - build the skoed binary (cd apps/skoed && make build)"
	@echo "  build-ui          - rebuild the SPA + stage it under apps/skoed/internal/api/static"
	@echo "  build-extension   - build browser extension zips (dist/skoed-firefox.zip + dist/skoed-chrome.zip)"
	@echo "  openapi-sync      - stage the OpenAPI spec for the binary's embedded swagger-ui"
	@echo "  dev               - M5.9.2 SPA hot-reload loop: skoed daemon + vite dev with HMR"
	@echo "  test              - run unit + acceptance tests in Docker"
	@echo "  acceptance        - run acceptance tests in Docker (alias)"
	@echo "  acceptance-clean  - remove the Docker go-mod/go-build cache volumes (M5.9.3)"
	@echo "  deb               - build dist/skoed_<version>_amd64.deb via nfpm"
	@echo "  deb-arm64         - build the arm64 .deb"
	@echo "  apk               - build dist/skoed_<version>_amd64.apk via nfpm"
	@echo "  apk-arm64         - build the arm64 .apk"
	@echo "  test-deb          - smoke-test the .deb in a clean debian:bookworm container"
	@echo "  helm-lint         - lint the charts/skoed Helm chart"
	@echo "  clean             - remove dist/ and built artefacts"

build:
	$(MAKE) -C apps/skoed build

build-ui:
	$(MAKE) -C apps/skoed build-ui

build-extension:
	bash web/extension/build.sh

openapi-sync:
	$(MAKE) -C apps/skoed openapi-sync

# M5.9.2: developer-loop. Runs skoed + vite dev together with API
# proxying so .vue edits hot-reload in the browser without rebuilding
# the Go binary. See specs/technical/make-dev.md.
dev:
	scripts/dev.sh

test acceptance:
	tests/acceptance/run-in-docker.sh

# M5.9.3 — wipe the persistent go-mod + go-build cache volumes used by
# tests/acceptance/run-in-docker.sh. Idempotent; harmless when the
# volumes already don't exist.
acceptance-clean:
	docker volume rm skoed-gomod-cache skoed-gobuild-cache || true

deb: build
	@mkdir -p dist
	SKOED_VERSION=$(SKOED_VERSION) SKOED_COMMIT=$(SKOED_COMMIT) \
		nfpm pkg --packager deb --config packaging/nfpm.yaml --target dist/

deb-arm64: build
	@mkdir -p dist
	SKOED_VERSION=$(SKOED_VERSION) SKOED_COMMIT=$(SKOED_COMMIT) \
		nfpm pkg --packager deb --config packaging/nfpm.yaml \
		--target dist/ --arch arm64

apk: build
	@mkdir -p dist
	SKOED_VERSION=$(SKOED_VERSION) SKOED_COMMIT=$(SKOED_COMMIT) \
		nfpm pkg --packager apk --config packaging/nfpm.yaml --target dist/

apk-arm64: build
	@mkdir -p dist
	SKOED_VERSION=$(SKOED_VERSION) SKOED_COMMIT=$(SKOED_COMMIT) \
		nfpm pkg --packager apk --config packaging/nfpm.yaml \
		--target dist/ --arch arm64

test-deb:
	packaging/test-deb.sh

helm-lint:
	helm lint charts/skoed

clean:
	rm -rf dist
	$(MAKE) -C apps/skoed clean

	rm -rf dist
	$(MAKE) -C apps/skoed clean
