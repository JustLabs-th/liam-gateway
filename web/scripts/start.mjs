import { cpSync, existsSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolve } from "node:path";
import { parseArgs } from "node:util";
const root = fileURLToPath(new URL("../", import.meta.url));
const { values } = parseArgs({
  options: { port: { type: "string", default: process.env.PORT || "3000" } },
});
if (!/^\d+$/.test(values.port) || Number(values.port) < 1 || Number(values.port) > 65535)
  throw new Error("Invalid port");
const server = resolve(root, ".next/standalone/server.js");
if (!existsSync(server)) throw new Error("Build the Admin UI first with pnpm build");
cpSync(resolve(root, ".next/static"), resolve(root, ".next/standalone/.next/static"), {
  recursive: true,
});
process.env.PORT = values.port;
process.env.HOSTNAME = process.env.GATEWAY_WEB_HOST || "127.0.0.1";
await import(pathToFileURL(server).href);
