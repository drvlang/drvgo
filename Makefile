.PHONY: build test integration
build:
	go build -o bin/drvgo ./cmd/drvgo
test:
	go test ./...
	go vet ./...
integration:
	go test -tags integration -v ./internal/export

.PHONY: dynamic
dynamic:
	./tests/dynamic.sh

# CIBench lives in the shared drvkit checkout. Pass additional limits in CIBENCH_ARGS.
DRVKIT ?= ../drvkit
CIBENCH_OUT ?= /tmp/drvgo-cibench
.PHONY: cibench
cibench:
	package=$$(nix build --impure --no-link --print-out-paths -f "$(DRVKIT)/cibench") && \
	    "$$package/bin/cibench" go --builder "$(CURDIR)" --out "$(CIBENCH_OUT)" $(CIBENCH_ARGS)
