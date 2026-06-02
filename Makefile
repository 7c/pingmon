# PingMon — Makefile
# Builds the Go server into bin/pingmon (git-ignored).

SERVER_DIR := server
BIN_DIR    := bin
BIN        := $(BIN_DIR)/pingmon

.DEFAULT_GOAL := build

.PHONY: build run test race vet fmt tidy clean help

## build: compile the server into bin/pingmon
build:
	@mkdir -p $(BIN_DIR)
	go -C $(SERVER_DIR) build -o $(CURDIR)/$(BIN) .
	@echo "built $(BIN)"

## run: build and run the server (requires root for ICMP)
run: build
	sudo ./$(BIN)

## test: run the server test suite
test:
	go -C $(SERVER_DIR) test ./...

## race: run the test suite with the race detector
race:
	go -C $(SERVER_DIR) test -race ./...

## vet: run go vet
vet:
	go -C $(SERVER_DIR) vet ./...

## fmt: format all Go sources
fmt:
	gofmt -w $(SERVER_DIR)

## tidy: tidy module dependencies
tidy:
	go -C $(SERVER_DIR) mod tidy

## clean: remove build artifacts
clean:
	rm -rf $(BIN_DIR)

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed -e 's/## //'
