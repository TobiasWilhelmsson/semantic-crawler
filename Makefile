GO ?= go
REQUIRED_GO := $(shell sed -n 's/^go //p' go.mod)
.DEFAULT_GOAL := help
SOURCE ?= generic-health-sample
MODEL_MODE ?= fixture
ADDR ?= 127.0.0.1:8091
ifeq ($(origin DB),undefined)
DB := data/semantic-$(value SOURCE).db
endif
REPORT_ROOT ?= artifacts/semantic-crawler
BASELINE ?= tests/fixtures/generic-health-sample/baseline.json
MAX_PAGES ?= 0
MAX_PAGE_BYTES ?= 8388608
URL ?=
PAGES ?= 10
DEPTH ?= 2
FETCH_DELAY ?= 750ms
# Read data settings without evaluating Make functions, then quote them for the shell.
# Command-line variables are normally exported and expanded by Make as well.
unexport URL PAGES DEPTH FETCH_DELAY ADDR MAX_PAGE_BYTES DB SOURCE MODEL_MODE REPORT_ROOT BASELINE MAX_PAGES
unexport VERSION REVISION
shell_quote = '$(subst ','"'"',$(1))'
ifneq ($(origin DB),file)
WEBSITE_DB_ARGS = -db $(call shell_quote,$(value DB))
endif
WEBSITE_OPTIONS = -scan-pages $(call shell_quote,$(value PAGES)) -scan-depth $(call shell_quote,$(value DEPTH)) -fetch-delay $(call shell_quote,$(value FETCH_DELAY)) -max-page-bytes $(call shell_quote,$(value MAX_PAGE_BYTES)) -addr $(call shell_quote,$(value ADDR)) $(WEBSITE_DB_ARGS)
CLI = $(GO) run ./cmd/semantic-crawler
SOURCE_ARGS = -source $(call shell_quote,$(value SOURCE)) -db $(call shell_quote,$(value DB))
BASELINE_ARGS = -source $(call shell_quote,$(value SOURCE)) -baseline $(call shell_quote,$(value BASELINE)) -benchmark-url-contains "" -benchmark-max-pages $(call shell_quote,$(value MAX_PAGES))

.PHONY: help setup doctor start website build test race vet lint vulncheck demo review eval benchmark proof crawl check preflight compatibility reviewer-learning reviewer-learning-live reviewer-learning-live-resume reviewer-learning-status reviewer-learning-resume prompt-diagnostics selector-health proof-readiness webhook semantic-demo semantic-demo-ui semantic-ui semantic-help semantic-proof semantic-benchmark-large semantic-reviewer-learning-fixture semantic-model-compatibility semantic-model-preflight semantic-prompt-diagnostics semantic-selector-health semantic-proof-readiness

help:
	@printf '%s\n' \
	  'Semantic Crawler' '' \
	  'Getting started' \
	  '  make setup                 Check prerequisites, download dependencies, create .env if missing.' \
	  '  make start                 Build and start the local UI. Stop with Ctrl+C.' \
	  '  make doctor                Check Go, CGO, and C compiler availability.' '' \
	  'Scans and review' \
	  '  make example.com           Start the UI and scan a website with your configured model.' \
	  '  make website URL=https://example.com/docs/  Scan a specific path.' \
	  '  make export SOURCE=name FORMAT=jsonl OUTPUT=exports/result.jsonl  Export approved content.' \
	  '  make demo                  Create candidates using the bundled offline sample.' \
	  '  make review                Alias for make start.' \
	  '  make crawl SOURCE=my-source  Crawl a configured source with your model provider.' \
	  '  make check SOURCE=my-source  Check a source for changed pages.' \
	  '  make webhook               Start the local approved-content receiver.' '' \
	  'Evaluation and development' \
	  '  make eval                  Run the synthetic offline evaluation.' \
	  '  make benchmark proof       Generate offline comparison reports.' \
	  '  make reviewer-learning     Run the offline reviewer-learning benchmark.' \
	  '  make preflight compatibility  Check configured model protocol support.' \
	  '  make test race vet lint build  Run local code checks.' \
	  '  make fmt-check mod-check     Check formatting and module integrity without editing files.' \
	  '  make release-check           Verify a source archive with fresh offline workflows.' \
	  '  make dist VERSION=0.1.0      Package a clean committed tree locally; uploads nothing.' \
	  '  make vulncheck             Query the Go vulnerability database.' '' \
	  'Run these commands from the project directory.' \
	  'Example: make start SOURCE=my-source ADDR=127.0.0.1:8093' \
	  'Offline sample scans need no API key. Live scans use the provider configured in .env.'

doctor:
	@command -v "$(GO)" >/dev/null 2>&1 || { printf '%s\n' 'Go is missing. Install Go $(REQUIRED_GO) or newer, then run make setup.' >&2; exit 1; }
	@$(GO) env GOVERSION
	@cgo_enabled="$$($(GO) env CGO_ENABLED)" || exit 1; \
	  if [ "$$cgo_enabled" != 1 ]; then \
	    printf '%s\n' 'SQLite requires CGO. Install a C compiler and run CGO_ENABLED=1 make setup.' >&2; exit 1; \
	  fi
	@compiler="$$($(GO) env CC)" || exit 1; \
	  compiler_binary="$${compiler%% *}"; \
	  command -v "$$compiler_binary" >/dev/null 2>&1 || { \
	    printf '%s\n' "C compiler not found: $$compiler_binary" 'On macOS: xcode-select --install. On Linux: install your distribution C compiler.' >&2; exit 1; \
	  }; \
	  printf 'C compiler found: %s\n' "$$compiler_binary"

setup: doctor
	$(GO) mod download
	@if [ ! -e .env ] && [ ! -L .env ]; then \
	  (umask 077; cp .env.example .env) || exit 1; \
	  printf '%s\n' 'Created .env. The offline sample is ready; add provider settings only for live scans.'; \
	else \
	  printf '%s\n' 'Preserved existing .env.'; \
	fi
	@printf '%s\n' 'Setup complete. Run make start, then choose Start scan → Offline sample.'

start: build
	@printf '%s\n' $(call shell_quote,UI: http://$(value ADDR)/semantic) 'Keep this terminal open. Press Ctrl+C to stop.'
	./bin/semantic-crawler -action serve $(SOURCE_ARGS) -model-mode $(call shell_quote,$(value MODEL_MODE)) -addr $(call shell_quote,$(value ADDR)) -report-dir $(call shell_quote,$(value REPORT_ROOT))

website: build
	./bin/semantic-crawler -action crawl-site -website $(call shell_quote,$(value URL)) $(WEBSITE_OPTIONS)

.DEFAULT:
	@case $(call shell_quote,$@) in \
	  *.*|http://*|https://*) ;; \
	  *) printf '%s\n' 'Unknown command. Use make help, or make example.com to scan a website.' >&2; exit 2 ;; \
	 esac
	@$(MAKE) --no-print-directory build
	./bin/semantic-crawler -action crawl-site -website $(call shell_quote,$@) $(WEBSITE_OPTIONS)

build: doctor
	mkdir -p bin
	@build_flags="$$(sh scripts/build-flags.sh $(call shell_quote,$(value VERSION)) $(call shell_quote,$(value REVISION)))" || exit 1; \
	  $(GO) build -ldflags "$$build_flags" -o bin/semantic-crawler ./cmd/semantic-crawler

.PHONY: fmt-check mod-check dist release-check
fmt-check:
	sh scripts/fmt-check.sh

mod-check:
	$(GO) mod tidy -diff
	$(GO) mod verify

dist:
	sh scripts/package-source.sh $(call shell_quote,$(value VERSION))

release-check:
	GO=$(call shell_quote,$(GO)) sh scripts/release-check.sh

test:
	$(GO) test -count=1 ./...

race:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

lint:
	$(GO) run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./...

vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...

demo:
	$(CLI) -action start-demo $(SOURCE_ARGS) -addr $(call shell_quote,$(value ADDR))

review: start

eval:
	$(CLI) -action eval -source $(call shell_quote,$(value SOURCE)) -model-mode fixture -report-dir $(call shell_quote,$(value REPORT_ROOT)/generic-eval)

benchmark:
	$(CLI) -action benchmark $(BASELINE_ARGS) -model-mode fixture -report-dir $(call shell_quote,$(value REPORT_ROOT))

proof:
	$(CLI) -action prove-human-parity $(BASELINE_ARGS) -model-mode fixture -report-dir $(call shell_quote,$(value REPORT_ROOT)/human-parity)

crawl:
	$(CLI) -action run $(SOURCE_ARGS) -mode live -model-mode openai -fetch-delay 750ms -max-page-bytes $(call shell_quote,$(value MAX_PAGE_BYTES)) -addr $(call shell_quote,$(value ADDR))

check:
	$(CLI) -action check $(SOURCE_ARGS) -mode live -model-mode openai -fetch-delay 750ms -max-page-bytes $(call shell_quote,$(value MAX_PAGE_BYTES)) -addr $(call shell_quote,$(value ADDR))

preflight:
	$(CLI) -action model-preflight -source $(call shell_quote,$(value SOURCE)) -model-mode openai -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-model-preflight)

compatibility:
	$(CLI) -action model-compatibility -source $(call shell_quote,$(value SOURCE)) -model-mode openai -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-model-compatibility)

reviewer-learning:
	$(CLI) -action reviewer-learning-benchmark $(BASELINE_ARGS) -model-mode fixture -reviewer-learning-mode deep -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-reviewer-learning-deep)

reviewer-learning-live:
	$(CLI) -action reviewer-learning-benchmark $(BASELINE_ARGS) -model-mode openai -benchmark-live -fetch-delay 750ms -reviewer-learning-mode deep -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-reviewer-learning-deep)

reviewer-learning-live-resume:
	$(CLI) -action reviewer-learning-benchmark $(BASELINE_ARGS) -model-mode openai -benchmark-live -fetch-delay 750ms -reviewer-learning-mode deep -reviewer-learning-resume -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-reviewer-learning-deep)

reviewer-learning-status:
	$(CLI) -action reviewer-learning-status -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-reviewer-learning-deep)

reviewer-learning-resume:
	$(CLI) -action reviewer-learning-benchmark $(BASELINE_ARGS) -model-mode $(call shell_quote,$(value MODEL_MODE)) -reviewer-learning-mode deep -reviewer-learning-resume -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-reviewer-learning-deep)

prompt-diagnostics:
	$(CLI) -action prompt-diagnostics $(BASELINE_ARGS) -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-prompt-diagnostics)

selector-health:
	$(CLI) -action selector-health $(BASELINE_ARGS) -fetch-delay 750ms -report-dir $(call shell_quote,$(value REPORT_ROOT)/sample-selector-health)

proof-readiness:
	$(CLI) -action proof-readiness -report-dir $(call shell_quote,$(value REPORT_ROOT))

webhook:
	$(CLI) -action mock-webhook -addr 127.0.0.1:8092

# Compatibility aliases for existing command examples.
semantic-help: help
semantic-demo: demo
semantic-demo-ui semantic-ui: review
semantic-proof: proof
semantic-benchmark-large: benchmark
semantic-reviewer-learning-fixture: reviewer-learning
semantic-model-compatibility: compatibility
semantic-model-preflight: preflight
semantic-prompt-diagnostics: prompt-diagnostics
semantic-selector-health: selector-health
semantic-proof-readiness: proof-readiness

# Portable content exports require an explicit source; the sample is never implicit.
FORMAT ?= jsonl
ifeq ($(origin OUTPUT),undefined)
OUTPUT := exports/$(value SOURCE).jsonl
endif
PROFILE ?= complete
unexport FORMAT OUTPUT PROFILE
.PHONY: export
export:
ifeq ($(origin SOURCE),file)
	$(error Set SOURCE explicitly, for example make export SOURCE=my-source)
endif
	$(CLI) -action export $(SOURCE_ARGS) -format $(call shell_quote,$(value FORMAT)) -output $(call shell_quote,$(value OUTPUT)) -content-profile $(call shell_quote,$(value PROFILE))
