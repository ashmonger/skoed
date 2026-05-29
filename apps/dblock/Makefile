BIN     := dblock
CGO_ENABLED := 0
LDFLAGS := -s -w

.PHONY: build test clean

build:
	CGO_ENABLED=$(CGO_ENABLED) go build -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/dblock/

test:
	go test ./...

clean:
	rm -f $(BIN)
