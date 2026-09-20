.PHONY: all build run dev test clean deps lint help

ELECTRON_DIR := electron
RENDERER_DIR := renderer

all: build

# Compiles the shell, the interface and the Go core for this platform.
build:
	cd $(ELECTRON_DIR) && npm run build
	cd $(RENDERER_DIR) && npm run build
	cd $(ELECTRON_DIR) && node tools/build-core.mjs $$(node -p process.platform)

# Runs the application from the working tree.
dev: build
	cd $(ELECTRON_DIR) && npx electron .

run: dev

# Produces an installable package for this platform.
package:
	cd $(ELECTRON_DIR) && npm run package

test:
	go test ./...
	cd $(ELECTRON_DIR) && npm run typecheck && npm test
	cd $(RENDERER_DIR) && npm test && npm run build

deps:
	go mod tidy
	cd $(ELECTRON_DIR) && npm install
	cd $(RENDERER_DIR) && npm install

clean:
	rm -rf $(ELECTRON_DIR)/dist $(ELECTRON_DIR)/core $(ELECTRON_DIR)/release $(RENDERER_DIR)/dist/assets

lint:
	golangci-lint run ./...

help:
	@echo "Targets: build, dev, package, test, lint, deps, clean"
	@echo "AIClaw is an Electron shell over a Go core; all data is local in ~/.aiclaw."
