# Semantic Reviewer Handbook

- Use `Approve` when the selected blocks match the source and scope.
- Use **Edit and approve** to make an explicit structured correction.
- Use **Retry with instruction** when the model should select different blocks. Name the missing or unwanted content clearly.
- Use **Reject** for unsuitable output, or **Escalate** when another reviewer should decide.

Compare headings, body text, warnings, tables, media, and metadata with the stored source snapshot. Preserve source facts and consequential warnings. Approval is a human decision; a benchmark score does not make it for you.

Default to a URL rule for a correction specific to one page. Use a source rule for a repeatable layout lesson. Avoid promoting broad removal instructions from one example. Saved rules appear under **Rules**, have versions, and can be disabled.

Examples:

- URL instruction: “Keep the warning box immediately after the second heading.”
- Source instruction: “Keep captions with their tables; remove share controls.”

Retries create auditable candidate revisions. The UI shows the previously approved version when one exists. Only approved candidates expose delivery actions, and delivery endpoints must be allowlisted.

A failed extraction retry stays queued and visible in the review queue. Open it and use **Resume extraction** to retry with the existing instruction. Queued work cannot be approved before extraction succeeds.

For live sources, start the review UI with `MODEL_MODE=openai`. The fixture model is only for synthetic demonstrations.

## Save portable content

After approval, use **Export content** from the source workspace or **Download this candidate** on a review page. See [Saving and exporting content](output-formats.md) for formats, provenance and conversion limits. For live retries, configure your own endpoint/key using [Model providers](providers.md).
