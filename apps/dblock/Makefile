BIN     := dblock
CGO_ENABLED := 0
LDFLAGS := -s -w

# Web UI lives at repo-root web/ and is embedded into the binary via
# go:embed at internal/api/static/dist/. `make build-ui` copies the freshly
# built Vite output into that slot; `make build` does the full pipeline.
WEB_DIR     := ../../web
EMBED_DIR   := internal/api/static/dist

.PHONY: build build-ui ui-deps ui-build test clean

build: build-ui
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/dblock/

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
