# Preparing a source release

A release contains the independent crawler, its tests, synthetic fixtures,
bundled example configuration, public guides, contribution files and MIT licence.
It must not contain operator data, source credentials, real captured datasets,
private endpoints or inherited application history.

The bundled `generic-health-sample` configuration and instructions are synthetic
examples. Other default source configs and instructions are ignored by Git.
Ignoring a file does not remove it if it was previously tracked: review both
`git ls-files` and the staged diff. Do not force-add a whole source directory.

## Publication inventory

Review these files before publication:

- `cmd/`, `internal/`, `go.mod`, `go.sum`, `Makefile` and release scripts.
- `tests/fixtures/`: synthetic inputs only, with no captured third-party pages.
- `.env.example` and the explicitly bundled config and source instructions.
- README, public guides, synthetic UI screenshots, licence and contributor files.
- `.github/`: workflows and templates that require no provider credentials.

Review source snapshots and history separately. A clean working tree or a secret
scanner result cannot establish that everything in an archive is appropriate
for publication. Local `.env`, databases, exports and reports are excluded;
planning/spec documents are excluded by `.gitattributes`.

The public repository is [TobiasWilhelmsson/semantic-crawler](https://github.com/TobiasWilhelmsson/semantic-crawler).
Its first commit starts from the reviewed source tree with fresh history.
Operator data and private preparation history are excluded.

## Publication checklist

- Choose the release version and review the changes since the preceding release.
- Review the complete public file inventory and dependency licences.
- Verify the source archive from a clean checkout and without `.git` after extraction.
- Confirm Linux and macOS CI for the exact release revision; record other platforms
  and live-provider quality as unverified unless actually exercised.
- Enable GitHub private vulnerability reporting and check the **Security → Report a
  vulnerability** route. Update `SECURITY.md` with the real destination.
- Configure required checks and confirm a fork pull request can run without secrets.
- Publish only with the maintainer's explicit instruction; tag the verified revision
  and upload the source archive together with its checksum.

Preparing an archive is a local operation. It does not create a Git tag, a GitHub
repository or a release.

## Local packaging and walkthrough

From a clean committed tree with Git, Go, Make, a C compiler, `tar`, `gzip` and
either `sha256sum` (Linux) or `shasum` (macOS):

```sh
make release-check
make dist VERSION=0.1.0
```

`0.1.0` is the first source release. `dist`
produces `dist/semantic-crawler-0.1.0-source.tar.gz` and its `.sha256` file.
It refuses dirty tracked changes, unsafe version values, existing destinations
and a symlinked `dist` directory. It uses `git archive` from HEAD, applies export
exclusions and adds root `VERSION` and `REVISION` files. Untracked files are not
packaged; review them separately if they were intended for the release.

Verify the downloaded bytes before extraction:

```sh
cd dist
shasum -a 256 -c semantic-crawler-0.1.0-source.tar.gz.sha256
# On Linux, sha256sum -c accepts the same checksum file.
tar -xzf semantic-crawler-0.1.0-source.tar.gz
cd semantic-crawler-0.1.0
make setup build
./bin/semantic-crawler -version
make start
```

The checksum covers archive bytes. It is not a signature or a claim of
bit-for-bit reproducible binaries. The source package requires its support
files; it is not a standalone binary distribution.

`make build` uses the package metadata without `.git`. An ordinary development
build identifies itself as `dev` with available Go VCS build information, or
`unknown` when revision information is unavailable. `-version` exits before
loading source files, environment files or a database. Explicit `VERSION` and
`REVISION` build overrides are validated before they become linker arguments.

`release-check` runs formatting, module verification, tests, race, vet, pinned
lint and build, then creates and extracts a temporary source package. It follows
setup, fresh synthetic demo/evaluation/benchmark/proof/reviewer-learning, an
unchanged second scan, local review approval, all served guides and all four
approved export formats. It removes inherited provider/source settings, uses
isolated databases/report roots and makes no live-provider or third-party crawl
requests. Go/tool installation can still require network access.

Owned servers are stopped on completion. Successful scratch packages are removed;
on failure, a printed temporary directory retains concise logs and isolated data.
CI's `--skip-code-checks` mode runs only the archive/offline stage because code
checks already run in the platform matrix. It is not a substitute for those checks.

## Continuous verification

The workflow uses pinned Node 24 checkout/setup-go actions, `contents: read`, no
persisted checkout credentials, and no privileged pull-request trigger. Linux
(`ubuntu-24.04`) and macOS (`macos-15`) run tests, race, vet, build, formatting and
module checks. Linux also runs lint, vulnerability scanning and the archive
walkthrough. Weekly grouped dependency proposals are reviewed manually.

The presence of a job is not evidence that it passed: use the exact revision's
run results. Fork pull requests and the private reporting route must be checked when repository
settings change. Required checks are `test (ubuntu-24.04)`, `test (macos-15)` and
`offline-release`.
