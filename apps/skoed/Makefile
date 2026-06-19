BIN     := skoed
CGO_ENABLED := 0
GIT_VERSION := $(shell git describe --tags --always 2>/dev/null || echo dev)
GIT_COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(GIT_VERSION) -X main.commit=$(GIT_COMMIT)

# Web UI lives at repo-root web/ and is embedded into the binary via
# go:embed at internal/api/static/dist/. `make build-ui` copies the freshly
# built Vite output into that slot; `make build` does the full pipeline.
WEB_DIR     := ../../web
EMBED_DIR   := internal/api/static/dist

# M4.5: the OpenAPI spec lives outside this module (at repo root). Copy
# it into the swaggerui assets dir so go:embed can pick it up. The
# target file is .gitignored.
OPENAPI_SRC := ../../specs/technical/management-api.openapi.yaml
OPENAPI_DST := internal/api/swaggerui/assets/openapi.yaml

.PHONY: build build-ui ui-deps ui-build openapi-sync test clean

build: build-ui openapi-sync
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/skoed/

# Stage the OpenAPI doc inside the swaggerui assets dir for go:embed.
openapi-sync:
	cp $(OPENAPI_SRC) $(OPENAPI_DST)

# Builds the SPA and stages it for the Go embed slot. Idempotent.
build-ui: ui-build
	rm -rf $(EMBED_DIR)
	mkdir -p $(EMBED_DIR)
	cp -r $(WEB_DIR)/dist/* $(EMBED_DIR)/

ui-build: ui-deps
	cd $(WEB_DIR) && npm run build

ui-deps:
	test -d $(WEB_DIR)/node_modules || (cd $(WEB_DIR) && npm install --no-audit --no-fund)

test:
	go test ./...

clean:
	rm -f $(BIN)
	rm -rf $(EMBED_DIR)
	rm -rf $(WEB_DIR)/dist
