# Contributing

Small, demonstrated improvements are welcome. Describe the user-visible problem
and preserve source content, approval history and existing export compatibility.

## Local setup

Install Go 1.26.9 or newer, Make and a C compiler (SQLite uses CGO), then run:

```sh
make setup
make test race vet lint build
make fmt-check mod-check
make start
```

Choose **Offline sample** in the UI. No model key is needed for tests or synthetic
workflows. See [testing](docs/testing.md) for controlled provider responses, fresh
report directories and the limits of fixture scores.

## Package map

- `cmd/semantic-crawler`: CLI, provider configuration, startup and export commands.
- `internal/semantic`: crawling, model contracts, persistence, review, exports,
  evaluation and local UI.
- `internal/models`: persisted data structures used by the standalone store.
- `tests/fixtures`: synthetic source HTML and reference selections.
- `configs/sources`: bundled example; generated operator configs stay local.
- `docs`: public user guides and dated verification evidence.
- `scripts`: source packaging and release verification.

## Changes and verification

Use `gofmt` and run checks appropriate to the change. A behavior fix should have
a regression that reproduces the failure before the fix. Exercise recovery and
error paths when they are affected. Avoid tests that merely repeat prose or
assert private implementation details. Keep model calls outside transactions.

Use a new report directory when checking offline workflows:

```sh
make eval benchmark proof reviewer-learning REPORT_ROOT=artifacts/my-change-check
```

Explain any storage, export schema, CLI or configuration compatibility change.
Do not silently regenerate old benchmark results under a new evaluator. Full
source-release validation is described in [releasing](docs/releasing.md).

## Safe examples and reports

Provide a minimal sanitized reproduction. Never attach API keys, `.env` files,
database dumps, private instructions, or captured sensitive pages. Replace
private hostnames and personal data with synthetic values. Do not contribute
captured third-party datasets without established redistribution rights.

Generated source configs and instruction files are ignored. To add a new public
example, review its synthetic content, add explicit exceptions to `.gitignore`
and `.gitattributes`,
and include it in the distribution regression and publication inventory. Do not
force-add the whole directory.

For a security issue, follow [SECURITY.md](SECURITY.md) rather than opening a public
issue. Normal bug reports should include version, OS/architecture, command,
expected/actual behavior and a small reproduction. Pull requests should explain
the behavior changed, verification and compatibility implications.
