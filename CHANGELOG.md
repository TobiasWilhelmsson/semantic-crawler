# Changelog

## Unreleased

- Decode declared HTML encodings before extraction, with size limits on both
  received bytes and decoded UTF-8. Preserve successful empty HTTP responses.
- Resolve relative links and assets using the page's first HTML base URL while
  continuing to enforce crawl scope.
- Preserve source order when the model returns selections in a different order,
  and detect meaningful whitespace changes in preformatted content.
- Preserve nested superscript/subscript notation inside deleted, inserted and
  struck text in readable exports.
- Show source-storage errors explicitly instead of presenting a broken database
  as an empty source. Keep failed scan finalization visible and allow retrying
  the status write without repeating extraction.
- Preserve substantive reference content alongside print/share text and use
  source-neutral benchmark feedback for notices, tables and media.
- Improve readability of scan management, checkpoint configuration and UI code,
  with explicit duration units and a shared finalization timeout.

Extraction identity v3 reprocesses previously cached pages on the next requested
scan; existing approvals remain intact. Reviewer-learning v23 requires a fresh
report directory for older checkpoints. Recorded benchmark results are unchanged.

## 0.1.0 — 2026-10-14

First independent source release under the MIT licence.

- Scoped HTML crawling with configurable instructions, page/depth limits,
  change detection and extraction identity.
- Operator-owned OpenAI, Gemini, DeepSeek, Qwen, Z.ai and compatible endpoints,
  with local schema validation and bounded request handling.
- Local scan progress, cancellation, source switching and snapshot comparison.
- Human review, versioned instructions, retry recovery and approved-only delivery.
- SQLite snapshot/candidate history and read-only JSON, JSONL, Markdown and text
  exports. Versioned document v1 preserves selected HTML and provenance; legacy
  receiver behavior remains available.
- Synthetic offline sample, evaluation and reviewer-learning workflows with
  explicit distinctions between fixture checks and live-model quality.
- Source-release inventory, contributor/security guidance and portable verification.
- `-version` diagnostics, validated source-archive metadata and checksum packaging;
  a release check exercises the archive without Git metadata or provider keys.

Storage upgrades are additive when writable storage is opened; old approvals are
retained. Export does not migrate storage. Extraction-policy changes take effect
on a requested scan; older pages without extraction identity are reprocessed.
Reviewer-learning v22 needs a fresh directory for older checkpoints.

Known limits: HTML only; no JavaScript rendering, PDF extraction, robots handling,
account authentication or delivery deduplication. Windows/WSL and live provider
quality remain unverified. See [verification](docs/verification.md).
