# Generic Health Sample Source Skill

## Scope
Extract clinical airway-care guidance from pages under `/airway/`. This guide is intentionally generic so new national sources can copy the pattern.

## Always Keep
- Article titles, clinical preambles, equipment lists, procedure steps, monitoring instructions, documentation requirements, discharge guidance, and source metadata.
- Warning cards, alert boxes, risk callouts, and contraindication notes even when they are visually styled as cards.
- Tables containing pressures, limits, timings, equipment checks, or other clinical parameters.
- Instructional media references when they explain a clinical action.

## Always Drop
- Cookie messages, headers, footers, contact/about pages, search widgets, local menus, social/share tools, and unrelated teaser cards.

## Critical Safety Rules
- Never rewrite numeric limits, pressure values, or stop conditions.
- Treat “stop”, “do not continue”, “call for help”, and oxygen desaturation guidance as critical.
- Escalate missing warning boxes or parameter tables.

## Known Layout Patterns
- This source may use generated CSS classes and nested cards. Ignore CSS and rely on semantic text, table structure, and source scope.
- Useful content may appear in `aside`, `table`, `figure`, `h2`, `p`, `ul`, and `ol` blocks.

## Reviewer Feedback History
- Keep every clinical warning card and every parameter table; drop only navigation and promotional boilerplate.
