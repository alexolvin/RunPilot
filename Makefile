GO ?= go
TMPDIR ?= /tmp
STATICCHECK ?= $(shell command -v staticcheck 2>/dev/null || echo "go run honnef.co/go/tools/cmd/staticcheck@latest")

.PHONY: check vet staticcheck test build lint lint-literals lint-web lint-hosts node-upgrade-line vendor-web icons screenshots visualcheck e2e-e4 release

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

# node-upgrade-line — самодостаточная строка обновления УЗЛА (копируется и
# запускается на узле). Читает sha256sum УСТАНОВЛЕННОГО бинарника координатора
# (не вручную!). Вывод: SHA256 из 64 hex-символов + аргумент хоста координатора;
# при любом отклонении (не 64 / не hex) — FAIL, строка не печатается.
COORD_BIN       ?= /home/user/.local/bin/runpilot
COORD_HOST      ?= $(shell hostname)
UPGRADE_NODE_SH ?= $(abspath deploy/upgrade-node.sh)

node-upgrade-line:
	@sha=$$(sha256sum "$(COORD_BIN)" | awk '{print $$1}'); \
	n=$$(printf '%s' "$$sha" | wc -c); \
	if [ "$$n" -ne 64 ]; then \
	  echo "FAIL: sha256sum '$(COORD_BIN)' = $$n символов (нужно 64 hex): '$$sha'" >&2; \
	  exit 1; \
	fi; \
	case "$$sha" in *[!0-9a-f]*) echo "FAIL: не hex: '$$sha'" >&2; exit 1;; esac; \
	printf 'scp %s:%s /tmp/upgrade-node.sh \\\n  && bash /tmp/upgrade-node.sh %s %s\n' \
	  "$(COORD_HOST)" "$(UPGRADE_NODE_SH)" "$$sha" "$(COORD_HOST)"

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

# W15: учёт версий — git-тег vMAJOR.MINOR.PATCH (единственный источник).
# make release BUMP=patch|minor|major [MESSAGE="..."]
release:
	@bump="$(BUMP)"; \
	if [ -z "$$bump" ]; then echo "нужен BUMP=patch|minor|major" >&2; exit 1; fi; \
	case "$$bump" in patch|minor|major) ;; *) echo "BUMP: patch|minor|major" >&2; exit 1;; esac; \
	cur=$$(git describe --tags --abbrev=0 2>/dev/null || echo ""); \
	if [ -z "$$cur" ]; then \
	  case "$$bump" in \
	    patch) new="v0.1.0";; \
	    minor) new="v0.1.0";; \
	    major) new="v1.0.0";; \
	  esac; \
	else \
	  cur=$${cur#v}; \
	  IFS=. read -r maj min pat <<< "$$cur"; \
	  case "$$bump" in \
	    patch) pat=$$((pat+1));; \
	    minor) min=$$((min+1)); pat=0;; \
	    major) maj=$$((maj+1)); min=0; pat=0;; \
	  esac; \
	  new="v$$maj.$$min.$$pat"; \
	fi; \
	echo "Текущая: ${cur:-—}, новая: $$new"; \
	git tag -a "$$new" -m "${MESSAGE:-Release $$new}" && \
	echo "Тег $$new создан. Бинарник подберёт версию из buildinfo при сборке."

# Э4 e2e: 2×fakellm (по 1 слоту), 6×mockcoder × 3 хода. Стенд — координатор
# со встроенным узлом (tmux-сокет e2e, не default). Бинарники в STAND_DIR.
# Использование: make e2e-e4 [STAND_DIR=/tmp/runpilot-stand]
E2E_STAND_DIR ?= /tmp/runpilot-stand
e2e-e4:
	CGO_ENABLED=0 $(GO) build -o $(E2E_STAND_DIR)/runpilot ./cmd/runpilot
	CGO_ENABLED=0 $(GO) build -o $(E2E_STAND_DIR)/runpilot-fakellm ./cmd/runpilot-fakellm
	CGO_ENABLED=0 $(GO) build -o $(E2E_STAND_DIR)/runpilot-mockcoder ./cmd/runpilot-mockcoder
	STAND_DIR=$(E2E_STAND_DIR) python3 test/e2e/e4_harness.py
