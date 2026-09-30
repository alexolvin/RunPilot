GO ?= go
TMPDIR ?= /tmp
STATICCHECK ?= $(shell command -v staticcheck 2>/dev/null || echo "go run honnef.co/go/tools/cmd/staticcheck@latest")

.PHONY: check vet staticcheck test build lint lint-literals lint-web lint-hosts vendor-web icons screenshots visualcheck

check: vet staticcheck test lint

vet:
	CGO_ENABLED=0 $(GO) vet ./...

staticcheck:
	CGO_ENABLED=0 $(STATICCHECK) ./...

# -race требует cgo, поэтому CGO_ENABLED=0 (статика) не применяется к тестам.
test:
	$(GO) test -race ./...

# R2 (v2 раздел «Запрет хардкода»): три линтера, любая находка — FAIL.
lint: lint-literals lint-web lint-hosts

lint-literals:
	$(GO) run ./tools/lintlits

lint-web:
	$(GO) run ./tools/lintweb

lint-hosts:
	$(GO) run ./tools/linthosts

build:
	CGO_ENABLED=0 $(GO) build -o runpilot ./cmd/runpilot

# W3: фиксированные ESM/шрифты/иконки из npm → web/ + хеши (web/vendor/VERSIONS.txt).
vendor-web:
	$(GO) run ./tools/vendorweb

# W3: web/icons/*.svg → web/app/components/icon.js (инлайн-разметка для иконок).
icons:
	$(GO) run ./tools/genicons

# W3: скриншоты комбинаций 19.2 + автопроверки 19.3 → docs/evidence/<STAGE>/screens.
# Поднимает стенд (runpilot-stand) сам, снимает headless Chrome (go-rod), пишет
# manifest.json. Первый запуск кладёт golden-базу; далее — сравнение (≤ diff-max).
# Использование: make screenshots STAGE=W3 [RUNPILOT_STAND_TOKEN=...] [CHROME=...]
screenshots:
	$(GO) build -o $(TMPDIR)/runpilot-stand ./cmd/runpilot-stand
	$(GO) build -o $(TMPDIR)/runpilot-screenshots ./tools/screenshots
	$(TMPDIR)/runpilot-screenshots -stage $(STAGE) \
		-out docs/evidence/$(STAGE)/screens \
		-golden test/visual/golden \
		-stand $(TMPDIR)/runpilot-stand \
		-token $(or $(RUNPILOT_STAND_TOKEN),runpilot-stand-test-token) \
		-chrome $(or $(CHROME),/usr/bin/google-chrome)

# W3: проверка VISUAL.md (R4/19.4) — все файлы manifest, 12 отметок/строка,
# commit = manifest и предок HEAD. Использование: make visualcheck STAGE=W3
visualcheck:
	$(GO) run ./tools/visualcheck -stage $(STAGE) -evidence docs/evidence
