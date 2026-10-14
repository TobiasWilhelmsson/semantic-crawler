# Benchmark evidence and methodology

These are selected recorded results, checked against their canonical JSON reports on 13 October 2026. Only aggregate metrics and provenance are included here. Captured website pages, selector datasets, prompts, model response dumps, credentials, and private endpoints are not part of this project.

## GLM provider run, 8–9 July 2026

The report records model mode `glm`, provider-returned token usage, and implementation version `semantic-reviewer-learning-deep-v20-review-ready-continues-to-stall`. It does not record enough provider metadata to identify the exact served model revision.

The run evaluated 57 pages with valid baselines out of 164 planned pages. Its recorded status is `stopped_after_stable_streak`; it is an adaptive stopped run, not completion of all 164 pages. The report belongs to the prototype implementation used in July and has not been rerun against the standalone release. It is a selected run, not an average across every experiment.

The project author confirms that people crawled the source pages and selected selectors for the content to retain. Those human selections form the reference against which the benchmark compares the crawler's output page by page. The retained report does not record the number of people who prepared the selectors.

| Stage | Pages in aggregate | Mean page text F1 | Mean page critical-content recall |
| --- | ---: | ---: | ---: |
| First pass | 57 | 98.8% | 72.3% |
| With existing rules | 57 | 98.8% | 83.4% |
| After new reviewer prompts | 17 | 98.0% | 95.7% |
| Final approved output | 57 | 99.3% | 100% |

The page records account for the final approval methods:

| Recorded final method | Pages |
| --- | ---: |
| Model first pass | 34 |
| Model with existing rules | 6 |
| Model after new reviewer prompts | 8 |
| Simulated audited source-block selection | 3 |
| Simulated baseline replacement after the prompt loop stalled | 6 |

The first three methods total 48 model passes. The recorded quality threshold is 0.98, with additional content-category checks; a pass is not exact parity. The remaining nine pages used benchmark-controlled correction. All stages compare against the report's normalized semantic baseline.

The benchmark generates feedback from its baseline and accumulates instructions across pages. First pass means before feedback on that particular page; it is not a zero-shot evaluation of an independent, previously unseen page set. The 17 prompted pages are a subset, so their aggregate cannot be interpreted as a whole-run improvement over the 57-page first pass. Final corrections are simulated operations, not independently observed human review.

The saved source report has SHA-256:

```text
479b48b0bf449bc47539dc465dc058b663ce41fcf24cd28da8672f8c9f4b3a33
```

This hash identifies the preserved JSON report; it does not independently validate the source baseline or make the underlying dataset available for public reproduction. No runtime dependency on the original repository is introduced.

## Standalone synthetic evaluation, 13 October 2026

The bundled `generic-health-sample` consists of four synthetic pages, including a navigation page and three content pages. `make eval` runs the actual crawl pipeline with a deterministic fixture model and compares it with bundled gold sections.

| Metric | Recorded value |
| --- | ---: |
| Expected URLs captured | 4 / 4 |
| Mean page first-pass text F1 | 98.5% |
| Mean page critical-content recall | 100% |
| Mean page table recall | 75% |
| Mean page media recall | 100% |
| Model calls on the unchanged second crawl | 0 |
| Final approved parity after simulated review | 100% |

The table mismatch is present in the baseline comparison even though the extracted candidate retains the source table. The final parity score includes simulated corrections and retries. It is separate from the first-pass score.

Reproduce the workflow from the project directory with installed dependencies and a fresh report directory:

```sh
make eval REPORT_ROOT=artifacts/my-benchmark
```

The report is written to `artifacts/my-benchmark/generic-eval/evaluation.json`. The verification run used `artifacts/quality-review-20261013-scww6za9/generic-eval/evaluation.json`; generated reports remain excluded from publication.

## Interpreting the metrics

Text precision and recall compare sets of normalized text tokens with the baseline, and text F1 is their harmonic mean. Aggregate scores are means of page-level metrics under the report's eligibility rules. Token agreement does not guarantee correct ordering, metadata, complete table-cell associations, or exact section boundaries.

The comparison is performed page by page and includes section-role, content-type, and Markdown inclusion (`parse_mode`) checks alongside content-category recall. The extraction pipeline retains selected source HTML rather than generating replacement prose. Preserving source sentences is distinct from measuring their grammar or order; text F1 does not score sentence structure. The retained benchmark evidence does not document a human-reviewer count or independently observed page-by-page review of the extracted output.

Critical-content, table, and media recall are separate category checks. Their aggregate values follow the evaluator's conventions, including a score of one when a page has no baseline content in that category. A 100% category average must therefore be read with the dataset and category coverage in mind.

Agreement with a human-prepared baseline is useful extraction evidence. It does not establish superiority to humans, clinical correctness, or measured time savings. Final reviewed scores include intervention; they are not autonomous model accuracy. Different datasets, implementations, normalization rules, and stage populations should not be ranked as equivalent experiments.

For benchmark commands and baseline requirements, see [reviewer-learning evaluation](semantic_reviewer_learning_benchmark.md). For software tests versus model quality, see [testing](testing.md). For dated code and workflow checks, see [local verification](verification.md).
