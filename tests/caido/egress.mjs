import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { once } from "node:events";
import { readFile, writeFile } from "node:fs/promises";
import http from "node:http";
import net from "node:net";

const username = "fixture-user";
const password = "fixture-password";

export async function assertRedactedLogs() {
  let logs = "";
  for await (const chunk of process.stdin) logs += chunk;
  for (const secret of [
    username,
    password,
    Buffer.from(`${username}:${password}`).toString("base64"),
  ]) {
    assert(
      !logs.includes(secret),
      "Egress credentials appeared in Caido or transport logs",
    );
  }
}

async function take(socket, size) {
  for (;;) {
    const bytes = socket.read(size);
    if (bytes !== null) return bytes;
    if (socket.destroyed || socket.readableEnded)
      throw new Error("fixture connection closed");
    await once(socket, "readable");
  }
}

async function listen(server) {
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return server.address().port;
}

export async function startEgressFixture() {
  const upgrades = [];
  await writeFile("/tmp/egress-upgrades.json", "[]", { mode: 0o600 });
  const origin = http.createServer((request, response) => {
    response.setHeader("Content-Type", "application/json");
    response.end(
      JSON.stringify({
        source: request.socket.remoteAddress,
        selector: request.headers["x-morpheus-egress"] ?? null,
      }),
    );
  });
  origin.on("upgrade", (request, socket) => {
    upgrades.push({
      source: socket.remoteAddress,
      selector: request.headers["x-morpheus-egress"] ?? null,
      context: request.headers["x-egress-test-context"] ?? null,
    });
    void writeFile("/tmp/egress-upgrades.json", JSON.stringify(upgrades), {
      mode: 0o600,
    });
    const accept = createHash("sha1")
      .update(
        request.headers["sec-websocket-key"] +
          "258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
      )
      .digest("base64");
    socket.write(
      `HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`,
    );
    void (async () => {
      assert.equal(request.headers["x-morpheus-egress"], undefined);
      const frame = await take(socket, 2);
      assert.equal(frame[0], 0x81);
      assert.equal(frame[1], 0x84);
      const mask = await take(socket, 4);
      const payload = await take(socket, 4);
      assert.equal(
        Buffer.from(payload.map((byte, i) => byte ^ mask[i % 4])).toString(),
        "ping",
      );
      const result = Buffer.from(socket.remoteAddress);
      socket.end(Buffer.concat([Buffer.from([0x81, result.length]), result]));
    })().catch(() => socket.destroy());
  });
  const port = await listen(origin);
  const connect = async (address, source) => {
    const colon = address.lastIndexOf(":");
    const host = address.slice(0, colon);
    assert(["127.0.0.1", "only.proxy.invalid"].includes(host));
    assert.equal(Number(address.slice(colon + 1)), port);
    const upstream = net.connect({
      host: "127.0.0.1",
      port,
      localAddress: source,
    });
    await once(upstream, "connect");
    return upstream;
  };
  const bridge = (socket, upstream, head = Buffer.alloc(0)) => {
    socket.on("error", () => upstream.destroy());
    upstream.on("error", () => socket.destroy());
    socket.on("close", () => upstream.destroy());
    upstream.on("close", () => socket.destroy());
    if (head.length) upstream.write(head);
    socket.pipe(upstream).pipe(socket);
  };
  const httpProxy = http.createServer();
  httpProxy.on("connect", (request, socket, head) => {
    void (async () => {
      assert.equal(request.headers["x-morpheus-egress"], undefined);
      assert.equal(
        request.headers["proxy-authorization"],
        `Basic ${Buffer.from(`${username}:${password}`).toString("base64")}`,
      );
      const upstream = await connect(request.url, "127.0.0.2");
      socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      bridge(socket, upstream, head);
    })().catch(() => socket.destroy());
  });
  const httpPort = await listen(httpProxy);
  const socksProxy = net.createServer((socket) => {
    socket.setTimeout(10000, () => socket.destroy());
    void (async () => {
      const greeting = await take(socket, 2);
      assert.equal(greeting[0], 5);
      await take(socket, greeting[1]);
      socket.write(Buffer.from([5, 2]));
      assert.equal((await take(socket, 1))[0], 1);
      const string = async () =>
        (await take(socket, (await take(socket, 1))[0])).toString();
      assert.equal(await string(), username);
      assert.equal(await string(), password);
      socket.write(Buffer.from([1, 0]));
      const request = await take(socket, 4);
      assert.equal(request[0], 5);
      assert.equal(request[1], 1);
      let host;
      if (request[3] === 3) host = await string();
      else {
        assert.equal(request[3], 1);
        host = [...(await take(socket, 4))].join(".");
      }
      const targetPort = (await take(socket, 2)).readUInt16BE();
      const upstream = await connect(`${host}:${targetPort}`, "127.0.0.3");
      socket.write(Buffer.from([5, 0, 0, 1, 127, 0, 0, 1, 0, 0]));
      bridge(socket, upstream);
    })().catch(() => socket.destroy());
  });
  const socksPort = await listen(socksProxy);
  const dead = net.createServer();
  const deadPort = await listen(dead);
  await new Promise((resolve) => dead.close(resolve));
  await writeFile(
    "/tmp/egress-fixture.json",
    JSON.stringify({
      port,
      profiles: {
        "caller-selected name": {
          proxy: `http://${username}:${password}@127.0.0.1:${httpPort}`,
        },
        "socks route": {
          proxy: `socks5h://${username}:${password}@127.0.0.1:${socksPort}`,
        },
        "dead route": {
          proxy: `http://${username}:${password}@127.0.0.1:${deadPort}`,
        },
      },
    }),
    { mode: 0o600 },
  );
}

export async function configureEgress(rpc) {
  const { profiles } = JSON.parse(await readFile("/tmp/egress-fixture.json"));
  const saved = await rpc("set-egress", profiles);
  assert(!JSON.stringify(saved).includes(password));
}

export async function testDisabledEgress() {
  const { port } = JSON.parse(await readFile("/tmp/egress-fixture.json"));
  const result = await capture(
    "127.0.0.1",
    port,
    "caller-selected name",
    false,
  );
  assert(result.status >= 400);
  assert.match(result.body, /direct fallback is disabled/);
}

export async function testStoppedEgress(rpc) {
  const { port } = JSON.parse(await readFile("/tmp/egress-fixture.json"));
  await rpc("stopTransport");
  try {
    for (const websocket of [false, true]) {
      for (const name of ["caller-selected name", "unknown route", undefined]) {
        const result = await capture("127.0.0.1", port, name, websocket);
        assert.equal(result.status, 503);
        assert.match(result.body, /direct fallback is disabled/);
      }
    }
  } finally {
    await rpc("startTransport");
  }
}

async function capture(host, port, name, websocket) {
  const headers = { Host: `${host}:${port}` };
  if (name !== undefined) headers["X-Morpheus-Egress"] = name;
  if (websocket)
    Object.assign(headers, {
      Connection: "Upgrade",
      Upgrade: "websocket",
      "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
      "Sec-WebSocket-Version": "13",
    });
  return new Promise((resolve, reject) => {
    const request = http.request(
      {
        host: "127.0.0.1",
        port: 8080,
        path: `http://${host}:${port}/egress`,
        headers,
      },
      (response) => {
        const chunks = [];
        response.on("data", (chunk) => chunks.push(chunk));
        response.on("error", reject);
        response.on("end", () =>
          resolve({
            status: response.statusCode,
            body: Buffer.concat(chunks).toString(),
          }),
        );
      },
    );
    request.setTimeout(15000, () =>
      request.destroy(new Error("Caido egress probe timed out")),
    );
    request.on("error", reject);
    request.on("upgrade", (response, socket, head) => {
      void (async () => {
        if (head.length) socket.unshift(head);
        socket.write(
          Buffer.from([
            0x81,
            0x84,
            1,
            2,
            3,
            4,
            0x70 ^ 1,
            0x69 ^ 2,
            0x6e ^ 3,
            0x67 ^ 4,
          ]),
        );
        const frame = await take(socket, 2);
        assert.equal(frame[0], 0x81);
        const source = (await take(socket, frame[1])).toString();
        socket.destroy();
        resolve({ status: response.statusCode, source });
      })().catch((error) => {
        socket.destroy();
        reject(error);
      });
    });
    request.end();
  });
}

async function eventually(read) {
  const deadline = Date.now() + 10000;
  let error;
  while (Date.now() < deadline) {
    try {
      return await read();
    } catch (cause) {
      error = cause;
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw error;
}

function assertStoredRoute(request, name, source) {
  assert(
    Buffer.from(request.raw, "base64")
      .toString()
      .includes(`X-Morpheus-Egress: ${name}\r\n`),
    "stored request lost its selector",
  );
  assert.equal(request.response?.statusCode, 200);
  const raw = Buffer.from(request.response.raw, "base64").toString();
  assert.deepEqual(JSON.parse(raw.slice(raw.indexOf("\r\n\r\n") + 4)), {
    source,
    selector: null,
  });
}

async function testReplayAndAutomate(graphql) {
  const requests = await eventually(async () => {
    const data = await graphql(`
      query {
        requests(first: 100) {
          edges {
            node {
              id
              host
              port
              isTls
              raw
              response {
                statusCode
                raw
              }
            }
          }
        }
      }
    `);
    const requests = data.requests.edges.map(({ node }) => node);
    assert(
      requests.some(
        (request) =>
          request.response?.statusCode === 200 &&
          Buffer.from(request.raw, "base64")
            .toString()
            .includes("X-Morpheus-Egress: socks route\r\n"),
      ),
    );
    return requests;
  });
  const sourceRequest = (name) =>
    requests.find(
      (request) =>
        request.response?.statusCode === 200 &&
        Buffer.from(request.raw, "base64")
          .toString()
          .includes(`X-Morpheus-Egress: ${name}\r\n`),
    );
  const httpRequest = sourceRequest("caller-selected name");
  assertStoredRoute(httpRequest, "caller-selected name", "127.0.0.2");
  const replay = await graphql(
    `
      mutation ($input: CreateReplaySessionInput!) {
        createReplaySession(input: $input) {
          session {
            id
          }
          error {
            __typename
          }
        }
      }
    `,
    { input: { kind: "HTTP", requestSource: { id: httpRequest.id } } },
  );
  const sessionId = replay.createReplaySession.session?.id;
  assert(sessionId, JSON.stringify(replay));
  try {
    const started = await graphql(
      `
        mutation ($id: ID!) {
          startReplayTask(sessionId: $id) {
            task {
              replayEntry {
                id
              }
            }
            error {
              __typename
            }
          }
        }
      `,
      { id: sessionId },
    );
    const entryId = started.startReplayTask.task?.replayEntry.id;
    assert(entryId, JSON.stringify(started));
    await eventually(async () => {
      const data = await graphql(
        `
          query ($id: ID!) {
            replayEntry(id: $id, sessionKind: HTTP) {
              error
              ... on ReplayEntryHttp {
                request {
                  raw
                  response {
                    statusCode
                    raw
                  }
                }
              }
            }
          }
        `,
        { id: entryId },
      );
      assert.equal(data.replayEntry.error, null);
      assert(data.replayEntry.request);
      assertStoredRoute(
        data.replayEntry.request,
        "caller-selected name",
        "127.0.0.2",
      );
    });
  } finally {
    await graphql(
      `
        mutation ($ids: [ID!]!) {
          deleteReplaySessions(ids: $ids) {
            deletedIds
          }
        }
      `,
      { ids: [sessionId] },
    );
  }

  const websocketRequest = requests.find(
    (request) =>
      request.response?.statusCode === 101 &&
      Buffer.from(request.raw, "base64")
        .toString()
        .includes("X-Morpheus-Egress: caller-selected name\r\n"),
  );
  assert(websocketRequest);
  const context = `replay-ws-${Date.now().toString(36)}`;
  const websocketRaw = Buffer.from(websocketRequest.raw, "base64")
    .toString()
    .replace("\r\n\r\n", `\r\nX-Egress-Test-Context: ${context}\r\n\r\n`);
  const websocketSession = await graphql(
    `
      mutation ($input: CreateReplaySessionInput!) {
        createReplaySession(input: $input) {
          session {
            id
          }
          error {
            __typename
          }
        }
      }
    `,
    {
      input: {
        kind: "WS",
        requestSource: {
          raw: {
            connectionInfo: {
              host: websocketRequest.host,
              port: websocketRequest.port,
              isTLS: websocketRequest.isTls,
            },
            raw: Buffer.from(websocketRaw).toString("base64"),
          },
        },
      },
    },
  );
  const websocketSessionId = websocketSession.createReplaySession.session?.id;
  assert(websocketSessionId, JSON.stringify(websocketSession));
  let websocketTaskId;
  try {
    const started = await graphql(
      `
        mutation ($id: ID!) {
          startReplayTask(sessionId: $id) {
            task {
              id
            }
            error {
              __typename
            }
          }
        }
      `,
      { id: websocketSessionId },
    );
    websocketTaskId = started.startReplayTask.task?.id;
    assert(websocketTaskId, JSON.stringify(started));
    await eventually(async () => {
      const observations = JSON.parse(
        await readFile("/tmp/egress-upgrades.json"),
      );
      const observed = observations.find((entry) => entry.context === context);
      assert(
        observed,
        "Replay WebSocket did not reach the origin through its proxy",
      );
      assert.equal(observed.source, "127.0.0.2");
      assert.equal(observed.selector, null);
    });
  } finally {
    if (websocketTaskId)
      await graphql(
        `
          mutation ($ids: [ID!]!) {
            stopReplayWsTasks(taskIds: $ids) {
              __typename
            }
          }
        `,
        { ids: [websocketTaskId] },
      );
    await graphql(
      `
        mutation ($ids: [ID!]!) {
          deleteReplaySessions(ids: $ids) {
            deletedIds
          }
        }
      `,
      { ids: [websocketSessionId] },
    );
  }

  const socksRequest = sourceRequest("socks route");
  assertStoredRoute(socksRequest, "socks route", "127.0.0.3");
  const created = await graphql(
    `
      mutation ($input: CreateAutomateSessionInput!) {
        createAutomateSession(input: $input) {
          session {
            id
          }
        }
      }
    `,
    { input: { requestSource: { id: socksRequest.id } } },
  );
  const automateId = created.createAutomateSession.session?.id;
  assert(automateId, JSON.stringify(created));
  try {
    const raw = Buffer.from(socksRequest.raw, "base64")
      .toString()
      .replace("\r\n\r\n", "\r\nX-Egress-Test: SEEDX\r\n\r\n");
    const start = raw.indexOf("SEEDX");
    await graphql(
      `
        mutation ($id: ID!, $input: UpdateAutomateSessionInput!) {
          updateAutomateSession(id: $id, input: $input) {
            __typename
          }
        }
      `,
      {
        id: automateId,
        input: {
          connection: {
            host: socksRequest.host,
            port: socksRequest.port,
            isTLS: socksRequest.isTls,
          },
          raw: Buffer.from(raw).toString("base64"),
          settings: {
            payloads: [
              {
                options: { simpleList: { list: ["once"] } },
                preprocessors: [],
              },
            ],
            placeholders: [{ start, end: start + 4 }],
            redirect: { strategy: "NEVER", max: 5 },
            strategy: "ALL",
            concurrency: { workers: 1, delay: 0 },
            retryOnFailure: { maximumRetries: 0, backoff: 0 },
            closeConnection: true,
            updateContentLength: true,
            extractors: [],
          },
        },
      },
    );
    const started = await graphql(
      `
        mutation ($id: ID!) {
          startAutomateTask(automateSessionId: $id) {
            automateTask {
              entry {
                id
              }
            }
          }
        }
      `,
      { id: automateId },
    );
    const entryId = started.startAutomateTask.automateTask?.entry.id;
    assert(entryId, JSON.stringify(started));
    await eventually(async () => {
      const data = await graphql(
        `
          query ($id: ID!) {
            automateEntry(id: $id) {
              requests(first: 5) {
                edges {
                  node {
                    error
                    request {
                      raw
                      response {
                        statusCode
                        raw
                      }
                    }
                  }
                }
              }
            }
          }
        `,
        { id: entryId },
      );
      const entries = data.automateEntry.requests.edges;
      assert.equal(entries.length, 1);
      assert.equal(entries[0].node.error, null);
      assertStoredRoute(entries[0].node.request, "socks route", "127.0.0.3");
    });
  } finally {
    await graphql(
      `
        mutation ($id: ID!) {
          deleteAutomateSession(id: $id) {
            __typename
          }
        }
      `,
      { id: automateId },
    );
  }
}

export async function testEgress(rpc, graphql) {
  const { port, profiles } = JSON.parse(
    await readFile("/tmp/egress-fixture.json"),
  );
  const expected = Object.fromEntries(
    Object.entries(profiles).map(([name, { proxy }]) => {
      const endpoint = new URL(proxy);
      return [name, { proxy: `${endpoint.protocol}//${endpoint.host}` }];
    }),
  );
  assert.deepEqual(
    await rpc("get-egress"),
    expected,
    "egress configuration did not survive restart or exposed credentials",
  );
  for (const websocket of [false, true]) {
    for (const [name, source] of [
      ["caller-selected name", "127.0.0.2"],
      ["socks route", "127.0.0.3"],
      [undefined, "127.0.0.1"],
    ]) {
      const result = await capture(
        name === undefined ? "127.0.0.1" : "only.proxy.invalid",
        port,
        name,
        websocket,
      );
      assert.equal(result.status, websocket ? 101 : 200);
      if (websocket) assert.equal(result.source, source);
      else
        assert.deepEqual(JSON.parse(result.body), { source, selector: null });
    }
    for (const name of ["unknown route", "dead route"]) {
      const result = await capture("127.0.0.1", port, name, websocket);
      assert.equal(result.status, name === "unknown route" ? 400 : 502);
      assert.match(result.body, /direct fallback is disabled/);
      assert(!result.body.includes(password));
    }
  }
  console.log(
    "Caido HTTP/WebSocket egress, remote DNS, redaction and fail-closed checks passed.",
  );
  await testReplayAndAutomate(graphql);
  console.log("Stored selectors, Replay and Automate egress checks passed.");
}
