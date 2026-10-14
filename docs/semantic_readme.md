# Semantic Readme

The crawler separates scoped discovery, source snapshots, block selection, human review, and approved delivery. It stores content hashes and immutable page versions; unchanged pages skip model calls. Reviewers can save versioned source or URL rules and inspect previous approved output.

Use `make setup` and `make start`, then choose Offline sample in the UI. `make demo` and `make eval` remain available for CLI workflows. See the project README for dependencies and explicit live-model configuration. `go run ./cmd/semantic-crawler -h` lists all CLI actions.

Originally designed for public-sector organisations, the project runs independently
of any consuming application's code or database. Storage is local SQLite.
[Model providers](providers.md) explains operator-owned API keys and compatible
endpoints. [Saving and exporting content](output-formats.md) describes local
portable exports and approved-only HTTP delivery.
