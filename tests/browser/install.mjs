import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { execFileSync } from "node:child_process";

const pins = JSON.parse(
  await readFile(new URL("./versions.json", import.meta.url)),
);
await mkdir("/opt/browser", { recursive: true });
for (const [url, target, checksum] of [
  [
    `https://github.com/vercel-labs/agent-browser/releases/download/v${pins.agentBrowser}/agent-browser-linux-x64`,
    "/usr/local/bin/agent-browser",
    pins.agentBrowserSHA256,
  ],
  [
    `https://storage.googleapis.com/chrome-for-testing-public/${pins.chrome}/linux64/chrome-linux64.zip`,
    "/opt/browser/chrome.zip",
    pins.chromeSHA256,
  ],
]) {
  const response = await fetch(url);
  assert(response.ok, `Download failed: ${response.status} ${url}`);
  const data = Buffer.from(await response.arrayBuffer());
  assert.equal(
    createHash("sha256").update(data).digest("hex"),
    checksum,
    `Checksum mismatch: ${url}`,
  );
  await writeFile(target, data);
}
await chmod("/usr/local/bin/agent-browser", 0o755);
execFileSync("unzip", ["-q", "/opt/browser/chrome.zip", "-d", "/opt/browser"]);
await rm("/opt/browser/chrome.zip");
