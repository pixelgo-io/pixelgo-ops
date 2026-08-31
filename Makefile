# pixelgo-ops
#
# SKELETON.

BINARY  := pixelgo-ops
PREFIX  ?= /usr/local

.PHONY: build install clean test cross

build:
	go build -o $(BINARY) ./cmd/pixelgo-ops

install: build
	install -m 755 $(BINARY) $(PREFIX)/bin/$(BINARY)

# binaries for both systems
cross:
	GOOS=linux   GOARCH=amd64 go build -o dist/$(BINARY)-linux-amd64   ./cmd/pixelgo-ops
	GOOS=windows GOARCH=amd64 go build -o dist/$(BINARY)-windows-amd64.exe ./cmd/pixelgo-ops

test:
	go test ./...

clean:
	rm -f $(BINARY)
	rm -rf dist/
