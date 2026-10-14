# Verification and release limits

## First source release, 14 October 2026

The source candidate was reviewed as a complete file inventory before its first
upload. It contains the independent Go module, CLI, local UI, tests, synthetic
fixtures, sample configuration, public guides, contribution templates and MIT
licence. It excludes local environment files, databases, raw responses, generated
reports, captured third-party datasets, private instructions, work plans and
private development history.

The final preparation revision passed 366 top-level tests with one intentional
subprocess-helper skip, the race suite, vet, pinned Staticcheck, formatting,
module tidiness/integrity and build. Linux and macOS preparation CI also passed.
The extracted source archive passed the complete Go test suite without `.git`.
These are pre-publication results; the public workflow verifies public revisions
independently through [Checks](https://github.com/TobiasWilhelmsson/semantic-crawler/actions/workflows/checks.yml).

The source-release walkthrough exercises setup/build, fresh offline demo,
evaluation, benchmark, parity proof and reviewer learning. Its local HTTP smoke
checks four discovered pages, an unchanged second scan, approval of three
candidates, all seven served guides and all four approved export formats. No
live model provider or third-party website is contacted.

A focused browser walkthrough covered desktop and 390-pixel mobile layouts,
keyboard navigation, scan/review routes, source selection, cancellation and
export. Guide code blocks scroll within their container at mobile width.
Screenshots in the README show only the synthetic sample. This is not a
comprehensive accessibility audit.

The dependency inventory identifies licences for all 38 dependency modules
imported by the checked macOS build. No dependency code is vendored. See
[dependency licences](dependencies.md).

## Reproduce the checks

From the project directory with the documented prerequisites installed:

```sh
make fmt-check mod-check test race vet lint build
make vulncheck
make release-check
```

`release-check` requires a clean committed Git tree. It packages that tree,
extracts it without Git metadata and runs isolated synthetic workflows with
fresh databases and report roots. Dependency and tool downloads may need network
access; model credentials are not required. Successful scratch runs are removed;
failed runs retain a printed diagnostic directory. See [releasing](releasing.md)
and [testing](testing.md).

## Dependency audit

Pre-publication vulnerability scanning found no known vulnerabilities in called
code or imported packages. The module-level advisory
[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) concerns unused OpenPGP packages
in `golang.org/x/crypto`; those packages are absent from the imported dependency
graph. Other packages from that module remain in use. Vulnerability results are
time-specific: run `make vulncheck` for current results.

## Limits

- Synthetic fixture scores establish pipeline behavior, not live-model accuracy.
  The recorded GLM prototype benchmark has separate methodology and intervention
  qualifications in [benchmark evidence](benchmark_results.md).
- Native Windows/WSL execution, authenticated shared hosting and live-provider
  extraction quality remain unverified. Windows compilation checks do not
  establish runtime support.
- The UI serves a trusted local operator. It has no account authentication or
  tenant separation. See [security boundaries](../SECURITY.md).
- Crawling is HTML-only, without JavaScript rendering, PDF extraction or robots
  handling. Source fidelity does not independently establish factual truth or
  grant redistribution rights.
- SQLite transactions roll back rejected review actions; conflicting decisions
  can return a locking error. Keep database paths stable while processes run.
  Network filesystems, hard-link aliases and replacement of an open database are
  outside the verified locking conditions.
- A failed delivery can already have reached its receiver. Authentication and
  duplicate handling belong to the receiving deployment.
- Canonical report JSON is replaced atomically. Derived report files are separate
  writes and can be refreshed by a same-input resume. Keep source content stable
  within an experiment; resume identity does not freeze remote pages or fixture
  file contents.

The checks are bounded evidence. They do not establish defect-free behavior.
