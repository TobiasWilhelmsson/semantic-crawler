# Semantic Reviewer-Learning Benchmark

`make reviewer-learning` runs a deterministic benchmark against the bundled synthetic selector baseline. It records first-pass model metrics, simulated reviewer turns, accumulated rules, and the final approval method. It can resume with `make reviewer-learning-resume` and expose progress with `make reviewer-learning-status`.

A fixture run spends no model tokens. Its metrics show pipeline and review-simulation behavior. Final approved parity may depend on audited simulated edits; it does not establish autonomous correctness.

The ordinary benchmark's **Simulated baseline correction** aggregate compares the provided baseline with itself. It represents the result of hypothetical perfect correction; no human edit is performed for that aggregate. Use the first-pass and after-feedback metrics to assess actual model output.

For a real source, supply an independently audited selector baseline with `BASELINE=path/to/baseline.json`, configure the model endpoint, then use `make reviewer-learning-live SOURCE=my-source BASELINE=path/to/baseline.json`. This fetches live pages and spends model tokens. Inspect `approval_method`, `strict_model_pass_pages`, model-success fields, and `proof_usable` before interpreting the result. Keep baseline and source versions with the report.

Selector baselines use a `urls` list. Each URL has an `identifier` list containing `name`, `container`, `role`, `type`, and `parse_mode`; the bundled baseline provides an example. New sources do not need a baseline for ordinary crawling and human review.
