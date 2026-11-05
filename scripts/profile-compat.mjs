// Run inside the disposable browser test image. Only generated loopback traffic
// is captured; no Caido instance, account, cookies or external service is used.
import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { request } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import { createInterface } from "node:readline";
import { promisify } from "node:util";

const exec = promisify(execFile);
const pins = JSON.parse(
  await readFile(new URL("../tests/browser/versions.json", import.meta.url)),
);
const directory = await mkdtemp(path.join(tmpdir(), "impersonate-parity-"));
const session = `parity-${randomBytes(6).toString("hex")}`;
const profile = process.env.PROFILE_ID ?? "chrome_157_cft";
const children = [];
const agent = process.env.AGENT_BROWSER_BIN ?? "agent-browser";
const chrome = process.env.CHROME_BIN ?? "/opt/chrome/chrome-linux64/chrome";
const configPath = path.join(directory, "config.json");
const caPath = path.join(directory, "ca.pem");
let heartbeat;

async function start(binary, args, env = process.env) {
  const child = spawn(binary, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  children.push(child);
  child.stderr.on("data", (data) => process.stderr.write(data));
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      child.kill();
      reject(new Error(`${binary} readiness timed out`));
    }, 10_000);
    const lines = createInterface({ input: child.stdout });
    const finish = (error, value) => {
      clearTimeout(timer);
      child.removeListener("error", failed);
      child.removeListener("exit", exited);
      if (error) reject(error);
      else resolve(value);
    };
    const failed = (error) => finish(error);
    const exited = () => finish(new Error(`${binary} exited before readiness`));
    child.once("error", failed);
    child.once("exit", exited);
    lines.on("line", (line) => {
      const value = JSON.parse(line);
      if (typeof value.port === "number") finish(undefined, value);
    });
  });
}

async function cli(...args) {
  const { stdout } = await exec(
    agent,
    ["--config", configPath, "--session", session, "--json", ...args],
    { timeout: 60_000, maxBuffer: 1024 * 1024 },
  );
  const result = JSON.parse(stdout);
  assert(result.success, JSON.stringify(result));
  return result.data;
}

const grease = (n) => (n & 0x0f0f) === 0x0a0a && n >> 8 === (n & 255);
const words = (data, skip = 2) => {
  const bytes = Buffer.from(data, "hex");
  const values = [];
  for (let i = skip; i + 1 < bytes.length; i += 2)
    values.push(bytes.readUInt16BE(i));
  return values.filter((value) => !grease(value));
};

function normalize(observation) {
  const ext = new Map(
    observation.tls.extensions.map((item) => [item.id, item.data]),
  );
  const anchors = [];
  const bytes = Buffer.from(ext.get(51764) ?? "", "hex");
  for (let i = 2; i < bytes.length;) {
    const size = bytes[i++];
    anchors.push(bytes.subarray(i, i + size).toString("hex"));
    i += size;
  }
  const shares = [];
  const keys = Buffer.from(ext.get(51) ?? "", "hex");
  for (let i = 2; i + 3 < keys.length;) {
    const group = keys.readUInt16BE(i);
    const size = keys.readUInt16BE(i + 2);
    i += size + 4;
    if (!grease(group)) shares.push(group);
  }
  return {
    protocol: observation.protocol,
    resumed: observation.resumed,
    ciphers: observation.tls.ciphers.filter((n) => !grease(n)),
    extensions: [...ext.keys()].filter((n) => !grease(n)).sort((a, b) => a - b),
    groups: words(ext.get(10) ?? ""),
    signatures: words(ext.get(13) ?? ""),
    versions: words(ext.get(43) ?? "", 1),
    keyShares: shares,
    alpn: ext.get(16),
    applicationSettings: ext.get(17613),
    certificateCompression: ext.get(27),
    trustAnchors: anchors.sort(),
    trustAnchorsWire: ext.get(51764),
    serverPadding: ext.get(4832),
    settings: observation.settings,
    window: observation.window,
    headers: observation.headers,
  };
}

async function throughRelay(port, originPort, baseline) {
  const authority = baseline.headers.find(
    ([name]) => name === ":authority" || name.toLowerCase() === "host",
  )[1];
  const destination = new URL(`https://${authority}`);
  const headers = ["Host", authority];
  for (const [name, value] of baseline.headers)
    if (!name.startsWith(":") && name.toLowerCase() !== "host")
      headers.push(name, value);
  for (const [name, value] of Object.entries({
    Token: token,
    Scheme: "https",
    Host: destination.hostname,
    Port: destination.port || String(originPort),
    Profile: profile,
    Trace: "parity-1",
  })) {
    headers.push(`X-Caido-Impersonate-${name}`, value);
  }
  return new Promise((resolve, reject) => {
    const req = request(
      {
        host: "127.0.0.1",
        port,
        method: "GET",
        path: baseline.path,
        headers,
        agent: false,
      },
      (res) => {
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (data) => {
          body += data;
        });
        res.on("end", () => {
          try {
            assert.equal(res.statusCode, 200, body);
            resolve(JSON.parse(body));
          } catch (error) {
            reject(error);
          }
        });
        res.on("error", reject);
      },
    );
    req.on("error", reject);
    req.on("upgrade", (res, socket, head) => {
      let pending = head;
      const receive = (data) => {
        pending = Buffer.concat([pending, data]);
        if (pending.length < 4) return;
        const length =
          (pending[1] & 127) === 126
            ? pending.readUInt16BE(2)
            : pending[1] & 127;
        const offset = (pending[1] & 127) === 126 ? 4 : 2;
        if (pending.length < offset + length) return;
        socket.destroy();
        try {
          assert.equal(res.statusCode, 101);
          resolve(
            JSON.parse(pending.subarray(offset, offset + length).toString()),
          );
        } catch (error) {
          reject(error);
        }
      };
      socket.on("data", receive);
      socket.on("error", reject);
      socket.setTimeout(10_000, () =>
        socket.destroy(new Error("WebSocket fixture timed out")),
      );
      receive(Buffer.alloc(0));
    });
    req.setTimeout(10_000, () =>
      req.destroy(new Error("relay request timed out")),
    );
    req.end();
  });
}

const token = randomBytes(32).toString("hex");
try {
  await writeFile(configPath, "{}");
  const version = await exec(agent, ["--config", configPath, "--version"]);
  assert.equal(version.stdout.trim(), `agent-browser ${pins.agentBrowser}`);
  const browserVersion = await exec(chrome, ["--version"]);
  assert(browserVersion.stdout.includes(pins.chrome), browserVersion.stdout);
  const origin = await start(process.env.FINGERPRINT_BIN ?? "fingerprint", [
    "--ca-file",
    caPath,
  ]);
  const tokenPath = path.join(directory, "token");
  const ownerPath = path.join(directory, "owner");
  await writeFile(tokenPath, token, { mode: 0o600 });
  await writeFile(ownerPath, "owner", { mode: 0o600 });
  heartbeat = setInterval(() => {
    void writeFile(ownerPath, "owner").catch(() => {});
  }, 5000);
  const relay = await start(
    process.env.TRANSPORT_BIN ?? "caido-impersonate-transport",
    ["--token-file", tokenPath, "--owner-file", ownerPath],
    { ...process.env, SSL_CERT_FILE: caPath },
  );
  await cli(
    "--headed",
    "--executable-path",
    chrome,
    "--ca-cert",
    caPath,
    "--args",
    "--no-sandbox,--disable-quic",
    "open",
    `https://localhost:${origin.port}/capture`,
  );
  const observations = [];
  for (let i = 0; i < 2; i++) {
    if (i > 0)
      await cli("open", `https://localhost:${origin.port}/capture-${i}`);
    const baseline = (await cli("eval", "JSON.parse(document.body.innerText)"))
      .result;
    const shaped = await throughRelay(relay.port, origin.port, baseline);
    observations.push({ baseline, shaped });
  }
  if (profile.endsWith("_PSK")) {
    for (let i = 0; i < 2; i++) {
      await cli("open", `https://localhost:${origin.port}/fresh-${i}`);
      const baseline = (
        await cli("eval", "JSON.parse(document.body.innerText)")
      ).result;
      observations.push({
        baseline,
        shaped: await throughRelay(relay.port, origin.port, baseline),
      });
    }
    assert(
      observations.at(-1).baseline.resumed,
      "Browser did not exercise TLS resumption",
    );
    assert(
      observations.at(-1).shaped.resumed,
      "Relay did not resume the TLS session",
    );
  }
  // Use a separate origin identity for the cold WebSocket check. HTTP/WS
  // clients have separate session caches; cross-mode resumption is unverified.
  const socketURL = `wss://127.0.0.1:${origin.port}/socket`;
  const socket = await cli(
    "eval",
    `new Promise((resolve,reject)=>{const ws=new WebSocket(${JSON.stringify(socketURL)});const timer=setTimeout(()=>{ws.close();reject(new Error('fixture timeout'))},5000);ws.onmessage=e=>{clearTimeout(timer);resolve(JSON.parse(e.data));ws.close()};ws.onerror=()=>{clearTimeout(timer);reject(new Error('fixture WebSocket failed'))}})`,
  );
  observations.push({
    baseline: socket.result,
    shaped: await throughRelay(relay.port, origin.port, socket.result),
  });
  const report = {
    agentBrowser: pins.agentBrowser,
    chrome: browserVersion.stdout.trim(),
    profile,
    observations,
  };
  if (process.env.PROFILE_REPORT)
    await writeFile(
      process.env.PROFILE_REPORT,
      JSON.stringify(report, null, 2),
    );
  for (const { baseline, shaped } of observations)
    assert.deepEqual(normalize(shaped), normalize(baseline));
  console.log(
    JSON.stringify({
      passed: true,
      agentBrowser: pins.agentBrowser,
      chrome: pins.chrome,
      profile,
      observations: observations.length,
    }),
  );
} finally {
  clearInterval(heartbeat);
  try {
    await cli("close");
  } catch {}
  for (const child of children.reverse()) {
    if (child.exitCode !== null || child.signalCode !== null) continue;
    await new Promise((resolve) => {
      child.once("exit", resolve);
      child.kill();
      const timer = setTimeout(() => {
        child.kill("SIGKILL");
      }, 5000);
      child.once("exit", () => clearTimeout(timer));
    });
  }
  await rm(directory, { recursive: true, force: true });
}
