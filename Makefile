BINARY   := mrinspect
BUILD_DIR := ./bin

.PHONY: build test lint lint-lane-ids vuln clean docker

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/mrinspect

test:
	go test -race ./...

test-integration:
	go test -tags integration ./...

lint:
	golangci-lint run --max-issues-per-linter=0 --max-same-issues=0

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...

lint-lane-ids:
	test -d internal/lane && test -d cmd
	@offenders="$$(grep -rnE '\.(ID|Id)\s*==\s*"' --include='*.go' internal/ cmd/ | grep -v '_test.go' || true)"; \
		if [ -n "$$offenders" ]; then printf '%s\n' "$$offenders"; exit 1; fi

clean:
	rm -rf $(BUILD_DIR)

docker:
	docker build -t $(BINARY):latest .
