# Semantic Next Source Onboarding

For a quick first scan, configure your model provider in `.env`, then run `make website.com` with your site's hostname. The command creates general content instructions and a separate source database, starts the UI, and scans up to 10 pages at depth 2. Use `make website URL=https://docs.example.com/guide/ PAGES=25 DEPTH=2` to choose a path and limits. Repeated commands reuse the source and preserve its instructions. Follow the printed scan URL to watch progress and review output. Refine the generated instructions before expanding the scan.

Start `make start`, open **Sources**, and choose **Add source**. Enter a unique slug, a seed URL, allowed host/path, scope, and crawl limits. Write source instructions describing which headings, warnings, tables, media, and metadata to retain and which unrelated navigation to exclude.

The wizard writes a JSON config under `configs/sources/` and a Markdown instruction file under `configs/sources/source-skills/`. Configure `.env`, open the source, and choose **Start scan → Configured model**. Watch the page counts and failures, then open its review queue. You can switch sources without restarting the UI. CLI commands remain available in the source's configuration details.

Review representative pages before increasing the crawl limit. Save repeatable feedback as source rules and isolated corrections as URL rules. `make check SOURCE=my-source` detects later changes; new output still requires review.

A selector baseline is optional and used for benchmark comparisons. It is not required for ordinary source discovery or review.
