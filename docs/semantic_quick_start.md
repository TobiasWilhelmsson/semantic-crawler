# Semantic Crawler Quick Start

Run these commands from the project root:

```sh
make setup
make start
```

Open **Start scan** and choose **Offline sample**. Watch progress under **Scans**, then open **Review this scan**. Cancelling a scan retains saved candidates; leaving the page does not cancel it.

Open [the local UI](http://127.0.0.1:8091/semantic). Setup preserves an existing `.env`; start builds the current code and keeps the UI in the foreground. Stop with Ctrl+C. Use `make help` for commands and `make doctor` for prerequisite checks. `make review` remains a startup alias; `make demo` and `make eval` are optional offline CLI workflows.

Open a candidate under **Review queue**. The stored source snapshot is on the left; selected blocks are on the right. Click a block to compare it with the source. Approve correct output or retry with a concrete instruction.

The offline fixture model makes no API calls. It tests the same kind of approved, source-correct output workflow without measuring a live model's quality. A saved `SkillRule` affects later retries or pages within its scope.

For your own source, configure `.env`, create its config using **Sources → Add source**, and choose **Start scan → Configured model**. Check the boundary and page limit before starting. The source picker changes the scan history, review queue, and instructions together without restarting the server.

With a configured provider, `make website.com` creates or reuses a source and immediately starts a scan alongside the UI. Replace the hostname with your site. Open the scan URL printed in the terminal; the defaults are 10 pages, depth 2, and a 750 ms fetch delay. For a narrower boundary, use `make website URL=https://docs.example.com/guide/ PAGES=25 DEPTH=2`. Use the final URL because redirects outside the source's host/path boundary are refused. Repeating the command preserves instructions and review history. The UI stays running after completion; Ctrl+C stops it. See [Model providers](providers.md) for settings and [Source onboarding](semantic_next_source_onboarding.md) for scope details.

The equivalent CLI commands are:

```sh
make crawl SOURCE=my-source
make start SOURCE=my-source MODEL_MODE=openai
```

Bring your own API key in the ignored local `.env`. Follow [Model providers](providers.md)
for OpenAI, Gemini, DeepSeek, Qwen, Z.ai and custom compatible endpoints, then
restart the UI. `MODEL_MODE=openai` is the shared protocol name for these providers.

`make preflight SOURCE=my-source` sends synthetic prompts to that provider and
may incur charges. It checks compatibility, not accuracy.

After approval, use **Export content** or:

```sh
make export SOURCE=my-source FORMAT=jsonl OUTPUT=exports/my-source.jsonl
```

See [Saving and exporting content](output-formats.md) for JSON, JSONL, Markdown,
text, complete/legacy profiles and the versioned document contract.

Live crawling fetches HTML without JavaScript rendering, PDF extraction or
`robots.txt` handling. Use sites you are entitled to process and appropriate
crawl limits. The synthetic example has no publicly accessible website;
use its offline mode. Scans preserve captured source content rather than
independently verifying its factual claims.
