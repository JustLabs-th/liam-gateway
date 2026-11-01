# Phase 1 API compatibility

The gateway implements a strict subset of OpenAI Chat Completions. It is not a transparent proxy for every provider feature.

| Capability | OpenAI adapter | Anthropic adapter |
|---|---|---|
| Text messages / system instructions | Yes | Yes; system messages become top-level system text |
| Single nonstream completion | Yes | Converted to Chat Completions response |
| SSE streaming | Yes | Converted to Chat Completions chunks |
| Function tool definitions / assistant calls / tool results | Yes | Converted to tool_use / tool_result |
| Streamed function arguments | Yes | Converted, including empty argument objects |
| max_tokens | Yes, subject to provider/model support | Yes |
| temperature / top_p | Forwarded | Converted; temperature must be <= 1 |
| stop | Forwarded | Converted to stop_sequences |
| tool_choice | Forwarded | auto / none / required / named function |
| stream_options.include_usage | Upstream usage requested for accounting | Usage chunk emitted when requested |
| /v1/models | Lists allowed gateway aliases | Same catalog |

Required fields: `model` (gateway alias), `messages` (nonempty array). Message roles are system, user, assistant or tool. Content is a string or null for assistant tool-call messages. Tools must be of type function. max_tokens defaults to min(1024, configured model output cap).

Unknown fields, image/audio content arrays, multiple completions, structured response_format, parallel_tool_calls, logprobs, provider-native reasoning/thinking, Responses API, embeddings, native Anthropic Messages endpoints and multimodal endpoints are outside Phase 1. Unknown request fields are rejected, not silently stripped. Unsupported Anthropic response blocks/events terminate conversion and mark the request incomplete.

Tool-call request arguments must be valid JSON strings. Tool-result messages require tool_call_id and text content. Streaming tool-call deltas follow OpenAI's indexed concatenation convention.

Client responses use the gateway model alias. Raw upstream payloads remain available in authorized request capture. Stream headers are `text/event-stream`, `Cache-Control: no-store`, `X-Accel-Buffering: no`; infrastructure in front of the gateway must permit streaming. The gateway always asks OpenAI for final streaming usage to support accounting, so OpenAI-compatible clients should tolerate a final usage-only chunk.

Error bodies are `{"error":{"type":"gateway_error","code":"...","message":"..."}}`. Provider error bodies are captured but are not returned verbatim. Admission statuses include 400 invalid/unsupported request, 401 invalid key, 402 budget exceeded, 403 disallowed model, 413 request cap, 429 quota exceeded, 502 provider/response failure and 503 infrastructure unavailable.

Errors after SSE output begins cannot replace the HTTP status. The stream closes, and request state records incomplete/interrupted. Clients must treat missing terminal `[DONE]` as failure.

`/v1/models` and `/v1/chat/completions` bodies are captured; headers are deliberately restricted to safe metadata. Admin operations use a separate audit trail so freshly issued credentials and login cookies are never copied into payload logs.

Live verification requires account-specific provider model IDs/credentials, and OIDC requires an organization-specific issuer/client. The supplied config uses explicit placeholder model IDs with zero example prices rather than assuming access or pricing.
