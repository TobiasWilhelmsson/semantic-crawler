# Saving and exporting content

SQLite is the crawler's working store: it retains source snapshots, candidates, reviews, rules and recovery state. Export files are portable content, not backups. Evaluation reports are separate synthetic or provider experiments, not content exports.

## Local export

Exports read an existing database without migrating it and need no model credentials or network requests. Choose the source explicitly:

```sh
make export SOURCE=my-source FORMAT=jsonl OUTPUT=exports/my-source.jsonl
make export SOURCE=my-source FORMAT=json OUTPUT=exports/my-source.json
make export SOURCE=my-source FORMAT=markdown OUTPUT=exports/my-source-markdown
make export SOURCE=my-source FORMAT=text OUTPUT=exports/my-source-text
```

JSON contains a versioned dataset envelope. JSONL has one versioned document per line. Markdown and text create directories containing one file per page and `manifest.json`. Existing destinations are refused; choose a new path for another export. Parent directories must be real directories rather than symlinks.

The source workspace's **Export content** control downloads JSON/JSONL or a ZIP for Markdown/text. **Download this candidate** selects a particular saved version. Downloads never approve or deliver candidates.

By default, exports select the latest approved version of each URL. A newer unapproved version is indicated in metadata. This is approved history, not a guarantee that the page still exists or is current. An empty selection exports zero documents; empty JSONL has no lines. The CLI reports the document count, and downloads include `X-Document-Count`.

Unapproved local content requires an explicit option:

```sh
go run ./cmd/semantic-crawler -action export -source my-source \
  -format markdown -output exports/my-source-drafts -include-unreviewed
```

This chooses the latest approved or active unreviewed candidate for each URL. Draft Markdown/text is visibly labelled, and every format retains status. `-candidate-id 12` selects a specific version; rejected/superseded versions require explicit candidate selection and `-include-unreviewed`. Remote delivery remains approved-only.

## Content profiles

`complete` is the default: all selected sections are retained, including selected warnings, metadata, tables and media. It does not restore material excluded by extraction or review. Markdown/text converts media to links and captions without downloading assets or executing embeds; the manifest records conversion warnings. Plain text loses rich formatting. Use JSON/JSONL when exact selected HTML and section relationships matter.

`legacy` retains saved inclusion behavior. Markdown uses the candidate's saved Markdown; text follows its saved `parse_mode` flags. JSON/JSONL still contains all sections and adds `legacy_parse_mode` metadata. Choose `PROFILE=legacy` in Make or `-content-profile legacy` in the CLI. Existing candidate records are never rewritten by export.

## Document v1

Each structured document has `schema_version: "semantic-crawler/document-v1"` and these fields:

- `source`: slug and display name; `url` and `title`: source page identity.
- `candidate_id`, `page_version_id`, optional `run_id`: local workspace references, not globally unique IDs.
- `status`, optional `captured_at`, `approved_at`, `approved_by`: capture/review provenance; unavailable older metadata is omitted rather than invented.
- Optional `content_hash` and `extraction`: model identifier, prompt version, source-instruction hash and, when available, extraction identity.
- `newer_unapproved_version`: whether a newer unapproved candidate exists for this URL.
- `sections`: ordered selected blocks with `block_id`, `role`, `severity`, `html`, and `locator`.

Selected HTML is untrusted source material with the crawler's URL normalization. It is not an executable/sanitized browser document and is not a byte-for-byte raw-page archive. Ordinary exports omit full-page snapshots, raw model responses, private reviewer instructions and credentials. The database remains the source for local diagnostics and backup.

JSON dataset envelopes use `semantic-crawler/dataset-v1`; Markdown/text manifests use `semantic-crawler/export-v1`. Consumers should reject unsupported major versions and tolerate documented additive metadata. Export accuracy describes fidelity to the captured source, not independent verification of its claims.

## Versioned HTTP delivery

The review page lets you choose **Legacy receiver format** (the default) or **Versioned document v1**. Document v1 sends the same structured document as a candidate export. Only approved candidates can be delivered, and the existing endpoint allowlist and redirect refusal apply. Audit records retain the selected schema and the SHA-256 hash of the actual JSON bytes sent. Failed delivery may still have reached the receiver; deduplication remains the receiver's responsibility.

## Re-extraction and old data

Read-only exports do not migrate storage or regenerate saved Markdown. An explicitly requested scan upgrades writable storage additively and compares both content and extraction identity. Older pages without an identity are reprocessed on the next scan. Changing instructions, scope, applicable reviewer rules or content-affecting provider/model settings also causes re-extraction. Unchanged equivalent settings retain zero-model-call reuse. API key changes alone do not invalidate extraction.

Choose **Re-extract unchanged pages** in the scan form or add `-force-reextract` to a CLI scan to deliberately rerun selection. Startup and exporting never launch this work automatically. New candidates require review; old approvals are preserved.

The extraction schema no longer asks the model to set `parse_mode`; that field remains readable in older saved sections and compatibility exports. Reviewer-learning report version v22 separates the changed prompting/schema behavior from old checkpoints; use a fresh report directory. Old benchmark figures describe their recorded evaluator and are not silently recomputed under new semantics.

## Conversion and local filesystem limits

Markdown preserves ordinary tables and marks deletions, insertions and struck text explicitly. Superscripts/subscripts use `^(...)` and `_(...)` notation. Complex table spans or nested layouts may lose visual relationships; the manifest records warnings and retains the selected HTML. Prefer structured JSON/JSONL when those relationships are essential.

Ordered lists retain supported starting numbers in Markdown. Per-item resets, reversed lists and plain-text lists use explicit decimal labels with a warning; custom marker styling is not reproduced. MathML uses source-provided alternative text when present, otherwise an explicit unsupported-formula placeholder. In either case the warning points to the original selected HTML in structured exports or the bundle manifest; flattened formula text is never presented as an equivalent expression.

Local exports refuse existing destinations, path traversal and symlink parents. Use an output directory you control: these checks do not protect against a hostile local process replacing parent directories concurrently. Browser downloads do not accept a server-side output path.
