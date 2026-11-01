import { test, expect } from "@playwright/test";
test("Playground → conversation inspector → timing → replay → analytics", async ({
  page,
  request,
}, info) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const created = await request.post("/admin/api/keys", {
    headers: { "X-Gateway-Admin": "1" },
    data: {
      name: `playground-${Date.now()}`,
      allowed_models: ["fast", "quality"],
      rpm: 60,
      concurrency: 3,
      monthly_budget_microusd: 0,
    },
  });
  expect(created.ok()).toBeTruthy();
  const { token, key } = await created.json();
  try {
    await page.goto("/admin");
    await expect(page.getByRole("heading", { name: "Your gateway, at a glance." })).toBeVisible();
    await page.getByRole("button", { name: "Playground", exact: true }).click();
    await page.getByLabel("Gateway API key", { exact: true }).fill(token);
    await page
      .getByRole("textbox", { name: "User message", exact: true })
      .fill("สวัสดี from Playground");
    await page.getByRole("button", { name: "Run request", exact: false }).click();
    await expect(page.locator(".playground-output .message-text")).toContainText("Hello OpenAI");
    await expect(page.locator(".playground-output .status")).toHaveText("completed");
    await expect(page.locator(".usage-note")).toContainText("reported");
    await page.getByText("cURL request", { exact: true }).click();
    const curl = await page.locator(".curl-example pre").innerText();
    expect(curl).toContain("$GATEWAY_API_KEY");
    expect(curl).not.toContain(token);
    expect(curl).toContain("\\\n");
    await page.getByRole("button", { name: "Inspect request", exact: false }).click();
    const inspector = page.getByRole("dialog");
    await expect(inspector).toBeVisible();
    await page.getByRole("button", { name: "Load full request & response", exact: true }).click();
    await expect(inspector.getByText("สวัสดี from Playground", { exact: true })).toBeVisible();
    await expect(inspector.getByText("Hello OpenAI", { exact: true })).toBeVisible();
    await page.getByRole("tab", { name: "Attempts", exact: true }).click();
    await expect(inspector.getByText("Attempt 1", { exact: true })).toBeVisible();
    await page.getByRole("tab", { name: "Timing", exact: true }).click();
    await expect(inspector.getByRole("heading", { name: "Request waterfall" })).toBeVisible();
    await expect(inspector.getByText("First streamed output", { exact: true })).toBeVisible();
    await page.screenshot({ path: info.outputPath("inspector-desktop.png"), fullPage: true });
    await page.getByRole("button", { name: "Open in Playground", exact: false }).click();
    await expect(page.getByRole("textbox", { name: "User message", exact: true })).toHaveValue(
      "สวัสดี from Playground",
    );
    await expect(page.getByLabel("Gateway API key", { exact: true })).toHaveValue("");
    await page.screenshot({ path: info.outputPath("playground-desktop.png"), fullPage: true });
    await page.getByRole("button", { name: "Overview", exact: true }).click();
    await page.getByRole("button", { name: "Refresh", exact: false }).click();
    await expect(page.locator(".chart-panel")).toHaveAttribute("aria-busy", "false");
    await expect(
      page.getByRole("img", { name: "Traffic over the selected time range" }),
    ).toBeVisible();
    await page.getByRole("button", { name: "P95 latency", exact: true }).click();
    await expect(
      page.getByRole("img", { name: "P95 latency over the selected time range" }),
    ).toBeVisible();
    await page.getByLabel("Inspect time bucket", { exact: true }).fill("0");
    await page.getByRole("button", { name: "Last 7 days", exact: true }).click();
    await expect(page.locator(".chart-panel")).toHaveAttribute("aria-busy", "false");
    await expect(page.locator(".activity-chart circle[data-sample]").first()).toBeVisible();
    await expect(page.locator(".chart-readout strong")).not.toHaveText(/^—/);
    await page.screenshot({ path: info.outputPath("overview-desktop.png"), fullPage: true });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(
      await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    ).toBe(true);
    await page.screenshot({ path: info.outputPath("overview-mobile.png"), fullPage: true });
    expect(errors).toEqual([]);
  } finally {
    await request
      .delete(`/admin/api/keys/${key.id}`, { headers: { "X-Gateway-Admin": "1" } })
      .catch(() => {});
  }
});
test("Inspector shows function arguments, tool results, and nonstream conversation", async ({
  page,
  request,
}) => {
  const created = await request.post("/admin/api/keys", {
    headers: { "X-Gateway-Admin": "1" },
    data: {
      name: `tools-${Date.now()}`,
      allowed_models: ["fast"],
      rpm: 20,
      concurrency: 2,
      monthly_budget_microusd: 0,
    },
  });
  const { token, key } = await created.json();
  try {
    const response = await request.post("/v1/chat/completions", {
      headers: { Authorization: `Bearer ${token}` },
      data: {
        model: "fast",
        max_tokens: 100,
        messages: [
          { role: "user", content: "Check the weather" },
          {
            role: "assistant",
            content: null,
            tool_calls: [
              {
                id: "call_weather",
                type: "function",
                function: { name: "weather", arguments: '{"city":"Bangkok"}' },
              },
            ],
          },
          { role: "tool", tool_call_id: "call_weather", content: "Sunny" },
          { role: "user", content: "Summarize it" },
        ],
      },
    });
    expect(response.status()).toBe(200);
    const id = response.headers()["x-request-id"];
    await page.goto("/admin");
    await page.getByRole("button", { name: "Request history", exact: true }).click();
    await page.getByRole("button", { name: `${id.slice(0, 14)}…`, exact: true }).click();
    await page.getByRole("button", { name: "Load full request & response", exact: true }).click();
    await page.getByRole("tab", { name: "Tools", exact: true }).click();
    await expect(page.getByRole("dialog").getByText("Sunny", { exact: true })).toBeVisible();
    await expect(
      page.getByRole("dialog").locator("pre").filter({ hasText: "Bangkok" }),
    ).toBeVisible();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("dialog")).toHaveCount(0);
  } finally {
    await request
      .delete(`/admin/api/keys/${key.id}`, { headers: { "X-Gateway-Admin": "1" } })
      .catch(() => {});
  }
});
