# Model providers

Offline sample scans need no provider or API key. For live scans, run `make setup`,
edit the ignored `.env`, choose a provider/model and restart the UI. The crawler
uses OpenAI-style Chat Completions for routing and block selection.

## Provider configuration

Bring your own LLM API key. Choose **OpenAI**, **Google Gemini**, **DeepSeek**, **Alibaba Qwen**, **Z.ai GLM**, or a **custom OpenAI-compatible endpoint** in `.env`; the same setup works with website shortcuts, UI scans, retries, and CLI commands. Keep credentials in this ignored local file. After changing provider settings, restart the UI to apply them.

**OpenAI** — supply your OpenAI API key and a model that supports Chat Completions with structured output:

```dotenv
SEMANTIC_LLM_PROVIDER=openai
SEMANTIC_LLM_BASE_URL=
SEMANTIC_LLM_MODEL=gpt-4.1-mini
SEMANTIC_LLM_API_KEY=your-openai-api-key
```

The preset uses `https://api.openai.com/v1`. The example model supports the required API features; choose another supported model available to your account if preferred. See [OpenAI's model documentation](https://developers.openai.com/api/docs/models/gpt-4.1-mini). The OpenAI preset uses `max_completion_tokens` and omits fixed temperature settings for compatibility with reasoning models.

**Google Gemini** — supply your Gemini API key and model:

```dotenv
SEMANTIC_LLM_PROVIDER=gemini
SEMANTIC_LLM_BASE_URL=
SEMANTIC_LLM_MODEL=gemini-3.8-flash
SEMANTIC_LLM_API_KEY=your-gemini-api-key
```

The preset uses `https://generativelanguage.googleapis.com/v1beta/openai`, following [Google's documented OpenAI-compatible API and structured-output examples](https://ai.google.dev/gemini-api/docs/openai#structured-output).

**DeepSeek, Alibaba Qwen and Z.ai GLM** — use your own provider API key:

| Provider | Example model | Default API base URL | Official usage/pricing |
| --- | --- | --- | --- |
| `deepseek` | `deepseek-flash` | `https://api.deepseek.com` | [DeepSeek](https://api-docs.deepseek.com/quick_start/pricing/) |
| `qwen` | `qwen-flash` | `https://dashscope-intl.aliyuncs.com/compatible-mode/v1` | [Alibaba Cloud](https://www.alibabacloud.com/help/en/model-studio/qwen-flash) |
| `zai` | `glm-4.7-flash` | `https://api.z.ai/api/paas/v4` | [Z.ai](https://docs.z.ai/guides/overview/pricing) |

For example:

```dotenv
SEMANTIC_LLM_PROVIDER=qwen
SEMANTIC_LLM_BASE_URL=
SEMANTIC_LLM_MODEL=qwen-flash
SEMANTIC_LLM_API_KEY=your-alibaba-model-studio-api-key
```

For DeepSeek or GLM, change the provider, model and key to the matching row.
The key must match Qwen's region/workspace; override the base URL for another
workspace using [Alibaba's endpoint reference](https://www.alibabacloud.com/help/en/model-studio/base-url).
Use general model API endpoints rather than coding-plan endpoints.

These presets request JSON Object output, include the schema in the prompt and
validate every response locally. They disable thinking using the configured
provider's request parameters. JSON mode does not enforce the schema on the
server. Other models can require different settings; check them with preflight.
See the [DeepSeek Chat API](https://api-docs.deepseek.com/api/create-chat-completion/),
[Qwen JSON guide](https://www.alibabacloud.com/help/en/model-studio/qwen-structured-output)
and [Z.ai JSON guide](https://docs.z.ai/guides/capabilities/struct-output).

**Custom endpoint** — for a hosted provider, an API gateway, or a self-hosted model service:

```dotenv
SEMANTIC_LLM_PROVIDER=custom
SEMANTIC_LLM_BASE_URL=https://your-model-service.example/v1
SEMANTIC_LLM_MODEL=your-model-name
SEMANTIC_LLM_API_KEY=your-api-key
```

Use the API base URL, including its path prefix; the crawler appends `/chat/completions` and sends the key in an `Authorization: Bearer` header. The service must support OpenAI-style Chat Completions with strict JSON Schema structured output. Native APIs with a different request format or authentication scheme need a compatible gateway. Self-hosted services that ignore authentication can use a placeholder API key.

An explicit `SEMANTIC_LLM_BASE_URL` overrides any preset, so clear an old value when switching to an official endpoint. Leaving `SEMANTIC_LLM_PROVIDER` unset preserves the original custom-endpoint configuration. `.env.example` defaults to `custom` with no endpoint or credentials; offline workflows still need no API key. Provider/model support varies: run `make preflight` before a larger scan. It sends synthetic prompts to your provider and can incur API charges. The examples above are setup guidance, not live-provider accuracy results.

Model content is validated locally against the requested schema before it can become a candidate. Missing or null required fields, unknown fields, incomplete completion reasons, and ambiguous multiple choices fail extraction. A valid explicit empty selection is allowed. `make preflight` probes both routing and extraction schemas with synthetic content; it does not measure accuracy on your source.

Default files fill variables that are not already exported in your shell. `.env.semantic-crawler`, when present, takes precedence over `.env`. An explicit `-env-file path/to/file` replaces matching exported values and fails if the file is missing or malformed.

Optional `SEMANTIC_LLM_TIMEOUT` and `SEMANTIC_LLM_RETRY_DELAY` values must be positive durations such as `2m` and `5s`. `SEMANTIC_LLM_RETRIES` is a nonnegative retry count, and `SEMANTIC_LLM_MAX_TOKENS` is a positive output-token limit. `.env.example` includes these settings.

## Compatibility, cost and privacy

Provider examples and endpoints were checked against the linked official docs on
14 October 2026. Account access, model names and prices can change; consult the
provider's current documentation. Protocol fixtures verify request formatting,
response validation and error handling. Live compatibility and extraction quality
of these examples have not been established by this release's offline checks.

A processed page normally requires routing and extraction requests; retries add
usage. Unchanged successfully processed pages skip model calls when extraction
settings are equivalent. A changed scope, instruction, applicable review rule or
model policy triggers re-extraction on the next requested scan.

Live requests send source blocks, scope, instructions and active reviewer rules
to your chosen endpoint. Configure that endpoint according to the information
being processed. Never put credentials in source instructions or source JSON.
Model and delivery redirects are refused; configure the final endpoint directly.

## Configuration problems

- Missing model configuration: set provider, model and key; custom providers also
  require a base URL. The UI can start offline without them.
- Switching providers: clear a previous explicit base URL and restart the UI.
- Structured output rejected: use preflight with a small token/cost allowance.
  Local validation rejects invalid content even if the endpoint accepts the request.
- Failed scan or retry: fix the endpoint/settings, then rerun the scan or choose
  **Resume extraction** on the queued candidate. Existing approvals are preserved.

See [Quick start](semantic_quick_start.md) and [Saving and exporting content](output-formats.md).
