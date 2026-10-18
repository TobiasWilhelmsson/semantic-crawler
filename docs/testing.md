# Testing and extraction quality

Ordinary tests verify software behavior without depending on a live LLM. A model
can follow the protocol and still omit important content: schema validation is
not evidence of semantic accuracy.

## Software checks

```sh
make test race vet lint build
make fmt-check mod-check
make vulncheck
```

Unit tests cover scoping, blocks, hashes, schemas, persistence, decisions and
exports. Integration tests use real local HTTP servers and SQLite databases to
exercise CLI/UI startup, cancellation, retries, CSRF, transactions and recovery.
Provider protocol tests return controlled model responses: valid, malformed,
incomplete, failed and cancelled. They verify the crawler's response to those
inputs, not a provider's ability to produce them.

For example, if selected blocks contain a fee and its exception, tests can prove
both survive storage and complete export. They cannot prove that a real model
will select the exception on an unfamiliar page. The same distinction applies to
warnings, sentence order, table relationships and source authority.

Go dependencies, pinned Staticcheck installation and vulnerability-database
lookups can require network access. These checks never need an LLM key. CI uses
no model credentials, paid endpoints or captured third-party data.

CI runs tests/race/vet/build on Linux and macOS. Linux additionally runs pinned
lint, vulnerability scanning and the extracted-archive walkthrough. `make
release-check` runs that complete local path from a clean committed tree. See
[releasing](releasing.md) for tools, metadata and failure logs.

## Synthetic fixture workflows

The bundled fixture model makes deterministic selections from four synthetic
HTML pages. These workflows exercise discovery, evaluation, review correction,
approved delivery and reports. They are not live-model accuracy benchmarks.

Use a fresh report root for each experiment:

```sh
make eval benchmark proof reviewer-learning REPORT_ROOT=artifacts/my-new-experiment
```

`eval` compares the actual crawl pipeline with synthetic gold content and checks
unchanged-page reuse. `benchmark` compares selected content with a selector
baseline. `proof` and reviewer-learning include automated feedback or simulated
corrections. Read each stage's metrics separately; final parity does not mean
autonomous model parity. Token F1 does not score grammar or sentence order.

Reviewer-learning v23 checkpoints bind the saved baseline selection, source
instructions, model policy, extraction version and evaluation options. Resume with
the same inputs and report root:

```sh
make reviewer-learning-status REPORT_ROOT=artifacts/my-new-experiment
make reviewer-learning-resume REPORT_ROOT=artifacts/my-new-experiment
```

Changed inputs or old report versions (including v22) need a fresh directory.
Keep input files stable during a run; identity checks cannot freeze changing live
websites.

Ordinary reports are published after completion; reviewer-learning writes
incremental checkpoints. Use local filesystems with working advisory locks.

## Provider checks and real-model evaluations

`make preflight SOURCE=my-source` sends synthetic routing/extraction prompts to
the configured provider. It can cost money and demonstrates protocol/schema
compatibility only. `make compatibility` additionally probes model listing and
general chat features that some providers do not expose.

A separately requested real-model evaluation should identify the exact provider
and served model where available, page population, independently prepared
references, costs, retries and interventions. Retain first-pass results separately
from prompted and corrected results. Do not claim human superiority, factual
truth or sentence-level correctness from token agreement.

[Recorded benchmark methodology](benchmark_results.md) describes the retained
aggregate results; [dated verification](verification.md) records actual code and
platform checks. Live quality and native Windows/WSL execution remain unverified.
