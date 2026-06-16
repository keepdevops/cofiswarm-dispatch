ROLE := dispatch
.PHONY: build test test-standalone-layout test-sse-gate
build:
	go build -o bin/cofiswarm-dispatch ./cmd/cofiswarm-dispatch
test: build test-standalone-layout test-sse-gate
test-standalone-layout:
	./test/scripts/assert-layout.sh $(ROLE)
test-sse-gate:
	@./test/scripts/test-sse-gate.sh
