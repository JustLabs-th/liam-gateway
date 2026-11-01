import { test } from "node:test";
import assert from "node:assert/strict";
import {
  applyChunk,
  chatRequest,
  consumeCompletion,
  emptyCompletion,
  eventText,
  capturedConversation,
} from "../../src/lib/inference.ts";
import type { CaptureEvent } from "../../src/lib/api.ts";
function event(stage: string, payload: string, id = 1): CaptureEvent {
  return {
    id,
    stage,
    attempt_id: null,
    payload_text: payload,
    payload_base64: Buffer.from(payload).toString("base64"),
    created_at: new Date().toISOString(),
  };
}
test("stream decoder handles fragmented UTF-8 and CRLF, including usage", async () => {
  const text =
    'data: {"choices":[{"delta":{"role":"assistant"}}]}\r\n\r\ndata: {"choices":[{"delta":{"content":"สวัสดี"}}]}\r\n\r\ndata: {"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":4}}\r\n\r\ndata: [DONE]\r\n\r\n';
  const bytes = new TextEncoder().encode(text);
  const response = new Response(
    new ReadableStream({
      start(c) {
        for (let i = 0; i < bytes.length; i += 3) c.enqueue(bytes.slice(i, i + 3));
        c.close();
      },
    }),
    { headers: { "content-type": "text/event-stream" } },
  );
  const result = await consumeCompletion(response, () => {});
  assert.equal(result.content, "สวัสดี");
  assert.deepEqual(result.usage, { prompt_tokens: 10, completion_tokens: 4 });
});
test("a partial stream is an error, with its emitted answer preserved", async () => {
  let partial = "";
  const r = new Response('data: {"choices":[{"delta":{"content":"partial"}}]}\n\n', {
    headers: { "content-type": "text/event-stream" },
  });
  await assert.rejects(
    consumeCompletion(r, (v) => {
      partial = v.content;
    }),
    /before completion/,
  );
  assert.equal(partial, "partial");
});
test("tool name and JSON arguments are assembled across deltas", () => {
  let state = applyChunk(emptyCompletion(), {
    choices: [
      {
        delta: {
          tool_calls: [{ index: 0, id: "t1", function: { name: "weather", arguments: "" } }],
        },
      },
    ],
  });
  state = applyChunk(state, {
    choices: [{ delta: { tool_calls: [{ index: 0, function: { arguments: '{"city":' } }] } }],
  });
  state = applyChunk(state, {
    choices: [{ delta: { tool_calls: [{ index: 0, function: { arguments: '"Bangkok"}' } }] } }],
  });
  assert.equal(state.tools[0].function.name, "weather");
  assert.deepEqual(JSON.parse(state.tools[0].function.arguments), { city: "Bangkok" });
});
test("captured SSE is reconstructed as conversation and replay input", () => {
  const events = [
    event("client_request", '{"model":"fast","messages":[{"role":"user","content":"Hi"}]}'),
    event("client_response_event", 'data: {"choices":[{"delta":{"content":"Hello"}}]}\n\n', 2),
    event("client_response_event", "data: [DONE]\n\n", 3),
  ];
  const c = capturedConversation(events);
  assert.equal(c.request?.model, "fast");
  assert.equal(c.messages[1].content, "Hello");
  assert.equal(c.error, null);
});
test("capture bytes split inside a Unicode code point are decoded losslessly", () => {
  const bytes = Buffer.from("สวัสดี");
  const first = event("upstream_response", "");
  first.payload_base64 = bytes.subarray(0, 2).toString("base64");
  const last = event("upstream_response", "", 2);
  last.payload_base64 = bytes.subarray(2).toString("base64");
  assert.equal(eventText([first, last], "upstream_response"), "สวัสดี");
});
test("invalid imported form and tool shapes cannot reach rendering", () => {
  for (const text of [
    '{"model":"fast","messages":[{"role":"user","content":{}}]}',
    '{"model":"fast","messages":[{"role":"assistant","tool_calls":[null]}]}',
    '{"model":"fast","messages":[{"role":"user","content":"Hi"}],"max_tokens":{}}',
  ])
    assert.equal(chatRequest(text), null);
});
test("gateway errors surface a useful message", async () => {
  const response = new Response('{"error":{"message":"Monthly budget cannot cover request"}}', {
    status: 402,
  });
  await assert.rejects(
    consumeCompletion(response, () => {}),
    /Monthly budget/,
  );
});
