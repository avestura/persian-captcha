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
run: ## Run the service with the demo site keys
	go run ./cmd/captchad -config captcha.demo.yaml

.PHONY: demo
demo: ## Run the demo site (needs `make run` in another terminal)
	go run ./cmd/demo

.PHONY: demo-up
demo-up: ## Run the service and the demo site in containers
	docker compose -f docker-compose.demo.yml up --build

.PHONY: preview
preview: ## Dump sample challenge artwork to ./preview
	go run ./cmd/preview -out ./preview

.PHONY: site
site: ## Serve the marketing site in site/ on :4173
	cd site && (python3 -m http.server 4173 || python -m http.server 4173)

# The site's interactive previews are the real challenge components driven by
# artwork and specs generated once, from a fixed seed. Refresh them when the
# renderer or a challenge's spec changes; `make web` then rebuilds the bundle
# that reads them.
.PHONY: site-fixtures
site-fixtures: preview ## Refresh the challenge fixtures used by the site
	cp preview/slider_jigsaw-board.png preview/slider_jigsaw-piece.png 	   preview/rotate-disc.png preview/click_order-board.png 	   preview/drag_drop-board.png preview/drag_drop-piece[0-2].png 	   site/assets/preview/
	for kind in slider_jigsaw rotate click_order drag_drop accessible; do 		cp preview/$$kind.spec.json preview/$$kind.state.json site/assets/preview/; 	done

.PHONY: fonts
fonts: ## Download the Persian webfont into web/assets/fonts
	./scripts/fetch-fonts.sh

.PHONY: docker
docker: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) -t persian-captcha:$(VERSION) .

.PHONY: clean
clean:
	rm -rf bin preview
