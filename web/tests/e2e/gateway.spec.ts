import { test, expect } from "@playwright/test";
test("open admin → create key → inference → full payload → rotate and revoke", async ({
  page,
  request,
}, testInfo) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/admin");
  await page.getByRole("button", { name: "Request history", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Every request, in view." })).toBeVisible();
  await expect(page.getByText("SSO disabled · Open access")).toBeVisible();
  await page.getByRole("button", { name: "API keys", exact: true }).click();
  const keyName = `browser-${Date.now()}`;
  await page.getByLabel("Key name").fill(keyName);
  await page.getByRole("button", { name: "Create API key", exact: true }).click();
  await expect(page.getByText("Copy your API key now")).toBeVisible();
  const token = (await page.locator(".secret code").textContent())!;
  expect(token).toMatch(/^gw_/);
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  const response = await request.post("/v1/chat/completions", {
    headers: { Authorization: `Bearer ${token}` },
    data: {
      model: "fast",
      messages: [{ role: "user", content: "browser private prompt" }],
      max_tokens: 100,
    },
  });
  expect(response.status()).toBe(200);
  expect((await response.json()).choices[0].message.content).toBe("Hello OpenAI");
  const id = response.headers()["x-request-id"];
  await page.getByRole("button", { name: "Request history", exact: true }).click();
  await page.getByRole("button", { name: /Refresh/ }).click();
  await page.getByRole("button", { name: `${id.slice(0, 14)}…`, exact: true }).click();
  await page.getByRole("button", { name: "Load full request & response" }).click();
  await page.getByRole("tab", { name: "Raw JSON", exact: true }).click();
  const input = page
    .locator("details")
    .filter({ has: page.getByText("client_request", { exact: true }) });
  await input.locator("summary").click();
  await expect(input.locator("pre")).toContainText("browser private prompt");
  const output = page
    .locator("details")
    .filter({ has: page.getByText("client_response", { exact: true }) });
  await output.locator("summary").click();
  await expect(output.locator("pre")).toContainText("Hello OpenAI");
  await page.screenshot({ path: testInfo.outputPath("request-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(
    true,
  );
  await page.screenshot({ path: testInfo.outputPath("request-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByRole("button", { name: "Close inspector", exact: true }).click();
  await page.getByRole("button", { name: "API keys", exact: true }).click();
  const row = page.getByRole("row").filter({ hasText: keyName });
  page.once("dialog", (dialog) => dialog.accept());
  await row.getByRole("button", { name: "Rotate", exact: true }).click();
  await expect(page.getByText("Copy your API key now")).toBeVisible();
  const rotated = (await page.locator(".secret code").textContent())!;
  expect(rotated).not.toBe(token);
  expect(
    (await request.get("/v1/models", { headers: { Authorization: `Bearer ${token}` } })).status(),
  ).toBe(401);
  expect(
    (await request.get("/v1/models", { headers: { Authorization: `Bearer ${rotated}` } })).status(),
  ).toBe(200);
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  page.once("dialog", (dialog) => dialog.accept());
  await row.getByRole("button", { name: "Revoke", exact: true }).click();
  await expect(row.getByRole("button", { name: "Revoke", exact: true })).toBeDisabled();
  expect(
    (await request.get("/v1/models", { headers: { Authorization: `Bearer ${rotated}` } })).status(),
  ).toBe(401);
  await page.getByRole("button", { name: "Audit trail", exact: true }).click();
  await expect(
    page.getByRole("cell", { name: "key.revoke_attempt", exact: true }).first(),
  ).toBeVisible();
  expect(errors).toEqual([]);
});
