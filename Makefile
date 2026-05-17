BINARY   := mrinspect
BUILD_DIR := ./bin

.PHONY: build test lint clean docker

build:
	go build -o $(BUILD_DIR)/$(BINARY) ./cmd/mrinspect

test:
	go test ./...

test-integration:
	go test -tags integration ./...

lint:
	golangci-lint run

clean:
	rm -rf $(BUILD_DIR)

docker:
	docker build -t $(BINARY):latest .
