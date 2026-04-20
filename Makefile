#
# Makefile
#
# Simple makefile to build binary.
#
# @copyright 2023 Wayfair Tech – Incubator, 2025 Schuberg Philis

VENDOR_DIR = vendor

.PHONY: get-deps
get-deps: $(VENDOR_DIR)

$(VENDOR_DIR):
	go mod download

.PHONY: build
build: $(VENDOR_DIR)
	GOOS=linux CGO_ENABLED=0 go build -a -ldflags '-extldflags "-static"' -o telefonistka .

.PHONY: clean
clean:
	rm -f telefonistka

.PHONY: test
test: $(VENDOR_DIR)
	TEMPLATES_PATH=../../../templates/ go test -v -timeout 30s ./...

