.PHONY: build test clean fmt vet check install

VERSION ?= $(shell git describe --tags --always --dirty)
LDFLAGS = -ldflags "-X main.version=$(VERSION)"

build:
	go build $(LDFLAGS) -o clipx ./cmd/clipx

install:
	go install $(LDFLAGS) ./cmd/clipx

test:
	go test -v ./...

test-coverage:
	go test -cover ./...

fmt:
	go fmt ./...

vet:
	go vet ./...

check: fmt vet test

clean:
	go clean ./...
	rm -f clipx coverage.out coverage.html

test-race:
	go test -race ./...

coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated: coverage.html"
