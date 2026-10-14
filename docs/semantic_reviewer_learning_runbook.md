# Semantic Reviewer-Learning Runbook

Run `make benchmark` before `make reviewer-learning` for the bundled offline sample. Inspect reports under `artifacts/semantic-crawler/`. Use `make reviewer-learning-status` to inspect progress and `make reviewer-learning-resume` to continue an interrupted run.

For live work, first configure `.env`, select the source, and run `make compatibility SOURCE=my-source` and `make preflight SOURCE=my-source`. Provide an audited baseline to `make reviewer-learning-live`. These commands make network requests and may spend model tokens.

Resume a live run with `make reviewer-learning-live-resume SOURCE=my-source BASELINE=path/to/baseline.json REPORT_ROOT=artifacts/my-run`. Resume an offline run with `make reviewer-learning-resume REPORT_ROOT=artifacts/my-run`. Keep the source, baseline, model endpoint/name, source instructions, and quality settings the same; incompatible or older reports require a fresh directory. Keep source content stable throughout an experiment. The stored input identity does not freeze live pages or fixture file contents.

A long benchmark writes checkpoints, a prompt transcript, learned rules, and page details. Treat final approvals, model passes, and simulated edits as separate evidence. Do not use the offline synthetic sample's results as a live quality claim.
