# Everything here is a convenience wrapper around go and npm. Nothing in the
# project requires make: `go build ./cmd/captchad` produces a working binary
# on its own, because the browser bundles are committed.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the service binary
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/captchad ./cmd/captchad

.PHONY: test
test: ## Run the Go test suite
	go test ./...

.PHONY: check
check: ## Vet, check formatting, and test
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	go test ./...
	cd web && npm run check

.PHONY: web
web: ## Rebuild the browser bundles (commit the result)
	cd web && npm install && npm run build

.PHONY: watch
watch: ## Rebuild the browser bundles on change
	cd web && npm run watch

.PHONY: packages
packages: ## Build the React and Vue wrappers
	cd packages && npm install && npm run build && npm run types

.PHONY: run
run: ## Run the service against the example configuration
	go run ./cmd/captchad -config captcha.example.yaml

.PHONY: demo
demo: ## Run the demo site (needs `make run` in another terminal)
	go run ./cmd/demo

.PHONY: preview
preview: ## Dump sample challenge artwork to ./preview
	go run ./cmd/preview -out ./preview

.PHONY: fonts
fonts: ## Download the Persian webfont into web/assets/fonts
	./scripts/fetch-fonts.sh

.PHONY: docker
docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t persian-captcha:$(VERSION) .

.PHONY: clean
clean:
	rm -rf bin preview
