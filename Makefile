TAILWIND_VERSION ?= v4.3.3
PDFJS_VERSION ?= 6.3.289
PDFJS_DIR := web/static/vendor/pdfjs
HTMX_VERSION ?= 2.0.4
HTMX_DIR := web/static/vendor/htmx
TAILWIND ?= bin/tailwindcss
UNAME_S := $(shell uname -s | tr A-Z a-z)
UNAME_M := $(shell uname -m | sed -e 's/x86_64/x64/' -e 's/aarch64/arm64/')
TAILWIND_OS := $(subst darwin,macos,$(UNAME_S))

.PHONY: all build css css-watch dev test lint clean vendor-pdfjs vendor-htmx

all: build

$(TAILWIND):
	mkdir -p bin
	curl -sSL -o $@ https://github.com/tailwindlabs/tailwindcss/releases/download/$(TAILWIND_VERSION)/tailwindcss-$(TAILWIND_OS)-$(UNAME_M)
	chmod +x $@

css: $(TAILWIND)
	$(TAILWIND) -i web/static/css/input.css -o web/static/css/app.css --minify

css-watch: $(TAILWIND)
	$(TAILWIND) -i web/static/css/input.css -o web/static/css/app.css --watch

build: css
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/manage ./cmd/manage

dev: css
	MANAGE_DEV=1 go run ./cmd/manage

test:
	go test ./...

lint:
	go vet ./...
	gofmt -l .

clean:
	rm -rf bin/manage web/static/css/app.css

# pdf.js renders the first page of PDFs chosen in upload dialogs. The files
# are committed under $(PDFJS_DIR); this target refreshes them, checking the
# npm integrity hash.
vendor-pdfjs:
	mkdir -p bin $(PDFJS_DIR)
	curl -sSL -o bin/pdfjs.tgz https://registry.npmjs.org/pdfjs-dist/-/pdfjs-dist-$(PDFJS_VERSION).tgz
	expected=$$(curl -sSL https://registry.npmjs.org/pdfjs-dist/$(PDFJS_VERSION) | python3 -c 'import json,sys; print(json.load(sys.stdin)["dist"]["integrity"])'); \
	actual="sha512-$$(openssl dgst -sha512 -binary bin/pdfjs.tgz | openssl base64 -A)"; \
	[ "$$expected" = "$$actual" ] || { echo "pdfjs-dist integrity mismatch"; exit 1; }
	tar -xzf bin/pdfjs.tgz -C bin package/build/pdf.min.mjs package/build/pdf.worker.min.mjs package/LICENSE
	cp bin/package/build/pdf.min.mjs bin/package/build/pdf.worker.min.mjs bin/package/LICENSE $(PDFJS_DIR)/
	echo "$(PDFJS_VERSION)" > $(PDFJS_DIR)/VERSION
	rm -rf bin/package bin/pdfjs.tgz

# htmx swaps slot cards in place on the volunteer pages. The file is
# committed under $(HTMX_DIR); this target refreshes it, checking the npm
# integrity hash.
vendor-htmx:
	mkdir -p bin $(HTMX_DIR)
	curl -sSL -o bin/htmx.tgz https://registry.npmjs.org/htmx.org/-/htmx.org-$(HTMX_VERSION).tgz
	expected=$$(curl -sSL https://registry.npmjs.org/htmx.org/$(HTMX_VERSION) | python3 -c 'import json,sys; print(json.load(sys.stdin)["dist"]["integrity"])'); \
	actual="sha512-$$(openssl dgst -sha512 -binary bin/htmx.tgz | openssl base64 -A)"; \
	[ "$$expected" = "$$actual" ] || { echo "htmx.org integrity mismatch"; exit 1; }
	tar -xzf bin/htmx.tgz -C bin package/dist/htmx.min.js package/LICENSE
	cp bin/package/dist/htmx.min.js bin/package/LICENSE $(HTMX_DIR)/
	echo "$(HTMX_VERSION)" > $(HTMX_DIR)/VERSION
	rm -rf bin/package bin/htmx.tgz
