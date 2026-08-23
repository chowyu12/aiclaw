.PHONY: all build run dev test clean deps lint help

WAILS ?= $(shell go env GOPATH)/bin/wails
DESKTOP_DIR := desktop

all: build

build:
	cd $(DESKTOP_DIR) && $(WAILS) build

run: build
	open $(DESKTOP_DIR)/build/bin/AIClaw.app

dev:
	cd $(DESKTOP_DIR) && $(WAILS) dev

test:
	go test ./...
	cd $(DESKTOP_DIR) && go test ./...
	cd $(DESKTOP_DIR)/frontend && npm test
	cd $(DESKTOP_DIR)/frontend && npm run build

deps:
	go mod tidy
	cd $(DESKTOP_DIR) && go mod tidy

clean:
	rm -rf $(DESKTOP_DIR)/build/bin $(DESKTOP_DIR)/frontend/dist

lint:
	golangci-lint run ./...

help:
	@echo "Targets: build, run, dev, test, lint, deps, clean"
	@echo "AIClaw is a Wails desktop application; all data is local in ~/.aiclaw."
