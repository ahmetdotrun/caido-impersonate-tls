// Controlled browser regression checks. Only a labelled disposable runtime is
// accepted: the script closes its test sessions and restarts that container.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { once } from "node:events";
import { createServer } from "node:http";
import { isIP } from "node:net";
import { Readable } from "node:stream";
import { pipeline } from "node:stream/promises";
import { setTimeout as delay } from "node:timers/promises";
import { promisify } from "node:util";
import { gzipSync } from "node:zlib";

const exec = promisify(execFile);
const container = process.env.COMPAT_CONTAINER;
const originHost = process.env.COMPAT_ORIGIN_HOST;
const expectedSource = process.env.COMPAT_EXPECTED_SOURCE;
const skipLargeTransfers = process.env.COMPAT_SKIP_LARGE_TRANSFERS === "1";
const httpsURL = new URL(
  process.env.COMPAT_HTTPS_URL ?? "https://example.com/",
);
const httpsText = process.env.COMPAT_HTTPS_TEXT ?? "Example Domain";
assert.equal(httpsURL.protocol, "https:", "HTTPS smoke URL must use TLS");
assert(httpsText.length > 0, "HTTPS smoke check needs expected page text");
assert(
  container && isIP(originHost) && isIP(expectedSource),
  "Set COMPAT_CONTAINER, COMPAT_ORIGIN_HOST (a local IP reachable by Caido), and COMPAT_EXPECTED_SOURCE (Caido's source IP)",
);
const docker = async (...args) => {
  try {
    return (
      await exec("docker", args, {
        timeout: 180_000,
        maxBuffer: 4 * 1024 * 1024,
      })
    ).stdout.trim();
  } catch (error) {
    throw new Error(
      error.stdout?.trim() || error.stderr?.trim() || error.message,
    );
  }
};
const inspected = JSON.parse(await docker("inspect", container))[0];
assert.equal(
  inspected.Config.Labels["io.caido.compat.disposable"],
  "true",
  "Refusing to restart an unlabelled runtime",
);
const binding = inspected.NetworkSettings.Ports["4948/tcp"].find(
  (item) => item.HostIp === "127.0.0.1",
);
assert(binding, "Publish the disposable dashboard on 127.0.0.1");
let dashboard = `http://127.0.0.1:${binding.HostPort}`;
const id = randomBytes(6).toString("hex");
const prefix = `/compat-${id}`;
const lanes = [`compat-cli-${id}`, `compat-ui-${id}`];
const profile = `/home/browser/compat-${id}`;
const results = [];
const sources = new Set();
const sockets = new Set();
let cancelledStreams = 0;

const server = createServer(async (req, res) => {
  const url = new URL(req.url, "http://fixture");
  if (!url.pathname.startsWith(`${prefix}/`)) {
    res.writeHead(404).end();
    return;
  }
  const source = req.socket.remoteAddress.replace(/^::ffff:/, "");
  sources.add(source);
  if (source !== expectedSource) {
    res
      .writeHead(403)
      .end("Fixture request did not arrive from the configured proxy");
    return;
  }
  const route = url.pathname.slice(prefix.length);
  res.setHeader("Cache-Control", "no-store");
  try {
    if (route === "/") {
      res.setHeader("Content-Type", "text/html");
      res.end(
        '<!doctype html><title>Proxy compatibility</title><h1>Proxy compatibility</h1><p id="ready">Ready</p>',
      );
    } else if (route === "/cookies") {
      res.setHeader("Set-Cookie", [
        `visible=${id}; Path=${prefix}/; Max-Age=3600; SameSite=Lax`,
        `private=${id}; Path=${prefix}/; Max-Age=3600; HttpOnly; SameSite=Lax`,
      ]);
      res.end("set");
    } else if (route === "/gzip") {
      res.setHeader("Content-Encoding", "gzip");
      res.end(gzipSync("compressed-ok"));
    } else if (route === "/redirect" || route === "/redirect307") {
      res
        .writeHead(route === "/redirect" ? 302 : 307, {
          Location: `${prefix}/echo`,
        })
        .end();
    } else if (route === "/echo" || route === "/upload") {
      let bytes = 0;
      const hash = createHash("sha256");
      for await (const chunk of req) {
        bytes += chunk.length;
        hash.update(chunk);
      }
      res.setHeader("Content-Type", "application/json");
      res.end(
        JSON.stringify({
          bytes,
          hash: hash.digest("hex"),
          method: req.method,
          cookies: req.headers.cookie ?? "",
          cancelledStreams,
          source,
          privateHeaders: Object.keys(req.headers).filter((name) =>
            name.startsWith("x-caido-impersonate-"),
          ),
        }),
      );
    } else if (route === "/download") {
      const size = 70 * 1024 * 1024;
      res.writeHead(200, {
        "Content-Length": size,
        "Content-Type": "application/octet-stream",
        "Content-Disposition": 'attachment; filename="compat.bin"',
      });
      await pipeline(
        Readable.from(
          (function* () {
            const chunk = Buffer.alloc(64 * 1024, 120);
            for (let offset = 0; offset < size; offset += chunk.length)
              yield chunk;
          })(),
        ),
        res,
      );
    } else if (route === "/range") {
      assert.equal(req.headers.range, "bytes=2-5");
      res
        .writeHead(206, {
          "Content-Range": "bytes 2-5/10",
          "Content-Length": 4,
          "Accept-Ranges": "bytes",
        })
        .end("2345");
    } else if (route === "/events" || route === "/cancel") {
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      res.write("data: first\n\n");
      const started = Date.now();
      const timer = setInterval(() => {
        res.write("data: tick\n\n");
        const lifetime = route === "/events" ? 100_000 : 20_000;
        if (Date.now() - started >= lifetime) res.end("data: complete\n\n");
      }, 2000);
      res.on("close", () => {
        clearInterval(timer);
        if (route === "/cancel" && !res.writableFinished) cancelledStreams++;
      });
    } else {
      res.writeHead(404).end();
    }
  } catch (error) {
    if (!res.headersSent) res.writeHead(500);
    res.end(String(error));
  }
});
server.on("connection", (socket) => {
  sockets.add(socket);
  socket.on("close", () => sockets.delete(socket));
});
server.on("upgrade", (req, socket, head) => {
  if (
    req.url !== `${prefix}/socket` ||
    req.socket.remoteAddress.replace(/^::ffff:/, "") !== expectedSource
  ) {
    socket.destroy();
    return;
  }
  const accept = createHash("sha1")
    .update(
      req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
    )
    .digest("base64");
  socket.write(
    `HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
  );
  let pending = head;
  socket.on("error", () => {});
  socket.on("data", (chunk) => {
    pending = Buffer.concat([pending, chunk]);
    while (pending.length >= 6) {
      const size = pending[1] & 127;
      if (size > 125 || !(pending[1] & 128)) {
        socket.destroy();
        return;
      }
      if (pending.length < size + 6) return;
      const opcode = pending[0] & 15;
      const payload = Buffer.from(pending.subarray(6, 6 + size));
      for (let i = 0; i < size; i++) payload[i] ^= pending[2 + (i % 4)];
      pending = pending.subarray(6 + size);
      if (opcode === 8) {
        socket.end(Buffer.from([0x88, 0]));
        return;
      }
      socket.write(
        Buffer.concat([
          Buffer.from([opcode === 9 ? 0x8a : 0x81, size]),
          payload,
        ]),
      );
    }
  });
});
server.listen(0, originHost);
await once(server, "listening");
const base = `http://${originHost}:${server.address().port}${prefix}`;
console.log(JSON.stringify({ fixture: base, container, lanes }));

function parsedOutput(stdout) {
  const output = JSON.parse(stdout);
  assert.equal(output.success, true, JSON.stringify(output));
  return output.data;
}
async function cli(lane, ...args) {
  return parsedOutput(
    await docker(
      "exec",
      "--user",
      "browser",
      container,
      "agent-browser",
      "--session",
      lane,
      "--json",
      ...args,
    ),
  );
}
async function ui(lane, ...args) {
  const response = await fetch(`${dashboard}/api/exec`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Origin: dashboard },
    body: JSON.stringify({ args: ["--session", lane, ...args] }),
    signal: AbortSignal.timeout(180_000),
  });
  assert(response.ok, `dashboard HTTP ${response.status}`);
  const result = await response.json();
  assert.equal(result.success, true, JSON.stringify(result));
  return parsedOutput(result.stdout);
}
async function evaluate(run, lane, expression) {
  const value = (await run(lane, "eval", expression)).result;
  return typeof value === "string" ? JSON.parse(value) : value;
}
// Browser work can legitimately outlive a CLI/CDP command timeout. Start once
// and inspect the same promise's result without resending the HTTP operation.
async function evaluateLong(run, lane, expression) {
  await evaluate(
    run,
    lane,
    `window.compatAsync={done:false};Promise.resolve().then(()=>(${expression})).then(value=>window.compatAsync={done:true,value},error=>window.compatAsync={done:true,error:String(error)});JSON.stringify({started:true})`,
  );
  const deadline = Date.now() + 300_000;
  while (Date.now() < deadline) {
    const state = await evaluate(
      run,
      lane,
      "JSON.stringify(window.compatAsync)",
    );
    if (state.done) {
      assert(!state.error, state.error);
      return typeof state.value === "string"
        ? JSON.parse(state.value)
        : state.value;
    }
    await delay(1000);
  }
  throw new Error("Browser operation exceeded the regression check deadline");
}
async function check(name, operation) {
  try {
    await operation();
    results.push({ name, passed: true });
  } catch (error) {
    results.push({ name, passed: false, error: String(error) });
  }
  console.log(JSON.stringify(results.at(-1)));
}

try {
  await cli(lanes[0], "--profile", profile, "open", `${base}/`);
  await ui(lanes[1], "--engine", "chrome", "open", `${base}/`);
  for (let index = 0; index < lanes.length; index++) {
    const lane = lanes[index];
    const run = index === 0 ? cli : ui;
    const followup = index === 0 ? ui : cli;
    await check(`${lane}: trusted HTTPS navigation`, async () => {
      try {
        await run(lane, "open", httpsURL.href);
        const state = await evaluate(
          followup,
          lane,
          "JSON.stringify({url:location.href,secure:isSecureContext,text:document.body.innerText})",
        );
        assert.equal(new URL(state.url).origin, httpsURL.origin);
        assert.equal(state.secure, true);
        assert(
          state.text.includes(httpsText),
          "Expected HTTPS content not received",
        );
      } finally {
        await run(lane, "open", `${base}/`);
      }
    });
    await check(`${lane}: cross-entry-point session continuity`, async () => {
      await evaluate(
        run,
        lane,
        `window.compatMarker=${JSON.stringify(id)}; JSON.stringify({ok:true})`,
      );
      const state = await evaluate(
        followup,
        lane,
        `JSON.stringify({marker:window.compatMarker,url:location.href,width:screen.width,height:screen.height})`,
      );
      assert.equal(state.marker, id);
      assert.equal(state.url, `${base}/`);
      assert.equal(state.width, 1920);
      assert.equal(state.height, 1080);
    });
    await check(
      `${lane}: cookies, redirects, compression and ranges`,
      async () => {
        const state = await evaluate(
          run,
          lane,
          `(async()=>{
        await fetch(${JSON.stringify(base + "/cookies")});
        const echo=await (await fetch(${JSON.stringify(base + "/redirect")})).json();
        const posted=await (await fetch(${JSON.stringify(base + "/redirect307")},{method:"POST",body:"keep-body"})).json();
        const compressed=await (await fetch(${JSON.stringify(base + "/gzip")})).text();
        const range=await fetch(${JSON.stringify(base + "/range")},{headers:{Range:"bytes=2-5"}});
        return JSON.stringify({echo,posted,compressed,rangeStatus:range.status,range:await range.text(),visible:document.cookie});
      })()`,
        );
        assert(state.echo.cookies.includes(`private=${id}`));
        assert(state.visible.includes(`visible=${id}`));
        assert(!state.visible.includes("private="));
        assert.deepEqual(state.echo.privateHeaders, []);
        assert.equal(state.posted.method, "POST");
        assert.equal(state.posted.bytes, 9);
        assert.equal(state.compressed, "compressed-ok");
        assert.equal(state.rangeStatus, 206);
        assert.equal(state.range, "2345");
      },
    );
    await check(`${lane}: WebSocket echo`, async () => {
      const result = await evaluate(
        run,
        lane,
        `new Promise((resolve,reject)=>{
        const socket=new WebSocket(${JSON.stringify(base.replace("http:", "ws:") + "/socket")});
        const timer=setTimeout(()=>{socket.close();reject(new Error("WebSocket timeout"))},10000);
        socket.onopen=()=>socket.send("compat-echo"); socket.onerror=()=>{clearTimeout(timer);reject(new Error("WebSocket failed"))};
        socket.onmessage=event=>{clearTimeout(timer);socket.close();resolve(JSON.stringify(event.data))};
      })`,
      );
      assert.equal(result, "compat-echo");
    });
    await check(`${lane}: start 100-second stream`, async () => {
      await evaluate(
        run,
        lane,
        `window.compatStream={done:false}; (async()=>{
        const started=Date.now();try {const response=await fetch(${JSON.stringify(base + "/events")});
        if(!response.ok)throw new Error("stream HTTP "+response.status);
        const reader=response.body.getReader();let text="",firstMs;
        while(true){const part=await reader.read();if(part.done)break;if(firstMs===undefined){firstMs=Date.now()-started;window.compatStream.firstMs=firstMs}text+=new TextDecoder().decode(part.value)}
        window.compatStream={done:true,firstMs,elapsed:Date.now()-started,complete:text.includes("data: complete")};
        }catch(error){window.compatStream={done:true,error:String(error)}}})();JSON.stringify({started:true})`,
      );
    });
  }
  if (!skipLargeTransfers) {
    await check("70 MiB browser upload", async () => {
      const result = await evaluateLong(
        cli,
        lanes[0],
        `(async()=>{const r=await fetch(${JSON.stringify(base + "/upload")},{method:"POST",body:new Blob([new Uint8Array(70*1024*1024)])});return JSON.stringify({status:r.status,body:await r.text()})})()`,
      );
      assert.equal(result.status, 200);
      assert.equal(JSON.parse(result.body).bytes, 70 * 1024 * 1024);
    });
    await check("70 MiB browser download", async () => {
      const result = await evaluateLong(
        cli,
        lanes[0],
        `(async()=>{const r=await fetch(${JSON.stringify(base + "/download")});const reader=r.body.getReader();let bytes=0,valid=true;while(true){const part=await reader.read();if(part.done)break;bytes+=part.value.length;valid=valid&&part.value.every(b=>b===120)}return JSON.stringify({status:r.status,bytes,valid})})()`,
      );
      assert.equal(result.status, 200);
      assert.equal(result.bytes, 70 * 1024 * 1024);
      assert(result.valid);
    });
  }
  await check("browser abort reaches the origin", async () => {
    await evaluateLong(
      cli,
      lanes[0],
      `(async()=>{const controller=new AbortController();const timer=setTimeout(()=>controller.abort(),1000);try{const r=await fetch(${JSON.stringify(base + "/cancel")},{signal:controller.signal});await r.text();throw new Error("Stream completed before abort")}catch(error){if(error.name!=="AbortError")throw error;return JSON.stringify({aborted:true})}finally{clearTimeout(timer)}})()`,
    );
    const deadline = Date.now() + 10_000;
    while (cancelledStreams === 0 && Date.now() < deadline) await delay(100);
    assert(
      cancelledStreams > 0,
      "Origin stream remained open after browser abort",
    );
  });
  const streamDeadline = Date.now() + 120_000;
  while (Date.now() < streamDeadline) {
    const state = await Promise.all(
      lanes.map((lane) =>
        evaluate(cli, lane, "JSON.stringify(window.compatStream)"),
      ),
    );
    if (state.every((item) => item.done)) break;
    console.log(JSON.stringify({ waitingForStreams: state }));
    await delay(10_000);
  }
  for (const lane of lanes)
    await check(
      `${lane}: stream completion and incremental delivery`,
      async () => {
        const state = await evaluate(
          cli,
          lane,
          "JSON.stringify(window.compatStream)",
        );
        assert(
          state.done && state.complete && !state.error,
          JSON.stringify(state),
        );
        assert(
          state.firstMs < 5000,
          `First bytes buffered for ${state.firstMs}ms`,
        );
        assert(
          state.elapsed >= 95_000,
          `Stream ended after ${state.elapsed}ms`,
        );
      },
    );
  await check(
    "persistent profile survives browser close and container restart",
    async () => {
      await evaluate(
        cli,
        lanes[0],
        `localStorage.setItem("compat",${JSON.stringify(id)});JSON.stringify({saved:true})`,
      );
      for (const lane of lanes) await cli(lane, "close");
      await docker("restart", "--timeout", "10", container);
      const restarted = JSON.parse(await docker("inspect", container))[0];
      const port = restarted.NetworkSettings.Ports["4948/tcp"].find(
        (item) => item.HostIp === "127.0.0.1",
      ).HostPort;
      dashboard = `http://127.0.0.1:${port}`;
      const readyDeadline = Date.now() + 60_000;
      while (Date.now() < readyDeadline) {
        const health = await docker(
          "inspect",
          "--format",
          "{{.State.Health.Status}}",
          container,
        );
        if (health === "healthy") break;
        await delay(1000);
      }
      await cli(lanes[0], "--profile", profile, "open", `${base}/`);
      const state = await evaluate(
        ui,
        lanes[0],
        `(async()=>JSON.stringify({stored:localStorage.getItem("compat"),echo:await(await fetch(${JSON.stringify(base + "/echo")})).json()}))()`,
      );
      assert.equal(state.stored, id);
      assert(state.echo.cookies.includes(`private=${id}`));
    },
  );
  assert.deepEqual(
    [...sources],
    [expectedSource],
    "Some requests bypassed the configured proxy",
  );
} finally {
  for (const lane of lanes) {
    try {
      await cli(lane, "close");
    } catch {}
  }
  for (const socket of sockets) socket.destroy();
  await new Promise((resolve) => server.close(resolve));
}
console.log(
  JSON.stringify(
    {
      passed: results.filter((item) => item.passed).length,
      failed: results.filter((item) => !item.passed).length,
      sources: [...sources],
      results,
    },
    null,
    2,
  ),
);
if (results.some((item) => !item.passed)) process.exitCode = 1;
