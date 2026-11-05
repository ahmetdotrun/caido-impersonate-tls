// Executed only in the project-owned, network-isolated Docker fixture.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { readFile, writeFile } from "node:fs/promises";
import http from "node:http";
import { createInterface } from "node:readline";
import tls from "node:tls";

const base = "http://127.0.0.1:8080";
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const fields = "id enabled rank allowlist denylist plugin { id }";
let token;
let state;

async function waitFor(read) {
  let lastError;
  const deadline = Date.now() + 30000;
  while (Date.now() < deadline) {
    try {
      return await read();
    } catch (error) {
      lastError = error;
    }
    await sleep(200);
  }
  throw lastError;
}

async function graphql(query, variables = {}) {
  const response = await fetch(`${base}/graphql`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    body: JSON.stringify({ query, variables }),
    signal: AbortSignal.timeout(5000),
  });
  assert.equal(response.status, 200);
  const result = await response.json();
  assert.equal(result.errors, undefined, JSON.stringify(result.errors));
  return result.data;
}

async function rpc(name, ...args) {
  const response = await fetch(
    `${base}/plugin/backend/${state.pluginId}/function`,
    {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify({
        name,
        args: args.map((arg) => JSON.stringify(arg)),
      }),
      signal: AbortSignal.timeout(15000),
    },
  );
  assert.equal(response.status, 200);
  const raw = await response.json();
  assert.equal(raw.kind, "success", JSON.stringify(raw));
  const result = JSON.parse(raw.returns);
  assert.equal(result.kind, "Ok", JSON.stringify(result));
  return result.value;
}

async function login() {
  const auth = await waitFor(() =>
    graphql(`
      mutation {
        loginAsGuest {
          token {
            accessToken
          }
          error {
            __typename
          }
        }
      }
    `),
  );
  token = auth.loginAsGuest.token?.accessToken;
  assert(token, JSON.stringify(auth));
  await writeFile("/tmp/auth.json", JSON.stringify({ token }), { mode: 0o600 });
  const project = await graphql(
    `
      mutation ($input: CreateProjectInput!) {
        createProject(input: $input) {
          project {
            id
          }
          error {
            __typename
          }
        }
      }
    `,
    { input: { name: "Impersonate TLS restart regression", temporary: true } },
  );
  const id = project.createProject.project?.id;
  assert(id, JSON.stringify(project));
  const selected = await graphql(
    `
      mutation ($id: ID!) {
        selectProject(id: $id) {
          currentProject {
            project {
              id
            }
          }
          error {
            __typename
          }
        }
      }
    `,
    { id },
  );
  assert.equal(selected.selectProject.currentProject?.project.id, id);
}

async function rules() {
  const data = await graphql(`query { upstreamPlugins { ${fields} } }`);
  return data.upstreamPlugins.sort((a, b) => a.id.localeCompare(b.id));
}

async function saveState() {
  state.rules = await rules();
  await writeFile("/tmp/state.json", JSON.stringify(state));
}

async function toggleRule(enabled) {
  const data = await graphql(
    `
      mutation ($id: ID!, $enabled: Boolean!) {
        toggleUpstreamPlugin(id: $id, enabled: $enabled) {
          upstream {
            id
            enabled
          }
        }
      }
    `,
    { id: state.ruleId, enabled },
  );
  assert.equal(data.toggleUpstreamPlugin.upstream.enabled, enabled);
  await saveState();
}

async function capture(host) {
  const { port } = JSON.parse(await readFile("/tmp/origin.json"));
  const ca = await (await fetch(`${base}/ca.crt`)).text();
  const socket = await new Promise((resolve, reject) => {
    const connect = http.request({
      host: "127.0.0.1",
      port: 8080,
      method: "CONNECT",
      path: `${host}:${port}`,
      headers: { Host: `${host}:${port}` },
    });
    connect.setTimeout(10000, () =>
      connect.destroy(new Error("CONNECT timed out")),
    );
    connect.on("error", reject);
    connect.on("connect", (response, connection, head) => {
      if (response.statusCode !== 200 || head.length !== 0) {
        connection.destroy();
        reject(
          new Error(`Unexpected CONNECT response: ${response.statusCode}`),
        );
      } else resolve(connection);
    });
    connect.end();
  });
  const agent = new http.Agent();
  agent.createConnection = () => tls.connect({ socket, host, ca });
  try {
    return await new Promise((resolve, reject) => {
      const request = http.request(
        { host, port, path: "/capture", agent },
        (response) => {
          const chunks = [];
          response.on("error", reject);
          response.on("data", (chunk) => chunks.push(chunk));
          response.on("end", () => {
            try {
              assert.equal(
                response.statusCode,
                200,
                Buffer.concat(chunks).toString().slice(-1000),
              );
              resolve(JSON.parse(Buffer.concat(chunks).toString()));
            } catch (error) {
              reject(error);
            }
          });
        },
      );
      request.setTimeout(15000, () =>
        request.destroy(new Error("Capture timed out")),
      );
      request.on("error", reject);
      request.end();
    });
  } finally {
    agent.destroy();
    socket.destroy();
  }
}

const grease = (value) =>
  (value & 0x0f0f) === 0x0a0a && value >> 8 === (value & 255);

async function assertCapture(host, shaped) {
  await rpc("clearActivity");
  const observation = await capture(host);
  const ciphers = observation.tls.ciphers;
  assert.equal(ciphers.length, shaped ? 16 : 31);
  assert.equal(ciphers.some(grease), shaped);
  if (shaped) {
    assert.equal(observation.protocol, "h2");
    for (const extension of [27, 4832, 51764]) {
      assert(observation.tls.extensions.some(({ id }) => id === extension));
    }
  }
  const activity = await waitFor(async () => {
    const entries = await rpc("getActivity");
    if (shaped) {
      assert.equal(entries.length, 1);
      assert.equal(entries[0].state, "succeeded");
      assert.equal(entries[0].profile, "chrome_157_cft_PSK");
    }
    return entries;
  });
  if (!shaped) assert.deepEqual(activity, []);
  console.log(
    JSON.stringify({
      host,
      shaped,
      cipherCount: ciphers.length,
      grease: ciphers.some(grease),
    }),
  );
}

const action = process.argv[2];
if (action === "serve") {
  const child = spawn("fingerprint", ["--ca-file", "/tmp/origin-ca.pem"], {
    stdio: ["ignore", "pipe", "inherit"],
  });
  child.on("error", (error) => {
    console.error(error);
    process.exit(1);
  });
  child.on("exit", (code) => process.exit(code ?? 1));
  const lines = createInterface({ input: child.stdout });
  for await (const line of lines) {
    const value = JSON.parse(line);
    if (typeof value.port === "number")
      await writeFile("/tmp/origin.json", line);
  }
} else if (action === "wait-origin") {
  await waitFor(async () =>
    assert(JSON.parse(await readFile("/tmp/origin.json")).port > 0),
  );
} else if (action === "login") {
  await login();
} else if (action === "setup") {
  await login();
  const form = new FormData();
  form.set(
    "operations",
    JSON.stringify({
      query: `mutation($input: InstallPluginPackageInput!) {
      installPluginPackage(input: $input) {
        package { plugins { __typename ... on PluginBackend { id } } }
        error { __typename }
      }
    }`,
      variables: { input: { source: { file: null }, force: false } },
    }),
  );
  form.set("map", JSON.stringify({ 0: ["variables.input.source.file"] }));
  form.set(
    "0",
    new Blob([await readFile("/artifacts/plugin_package.zip")]),
    "plugin_package.zip",
  );
  const response = await fetch(`${base}/graphql`, {
    method: "POST",
    headers: { Authorization: `Bearer ${token}` },
    body: form,
    signal: AbortSignal.timeout(30000),
  });
  const installed = await response.json();
  assert.equal(installed.errors, undefined, JSON.stringify(installed));
  const plugin = installed.data?.installPluginPackage.package?.plugins.find(
    (p) => p.__typename === "PluginBackend",
  );
  assert(plugin, JSON.stringify(installed));
  state = { pluginId: plugin.id };
  for (const config of [
    { enabled: false, allowlist: ["disabled.invalid"], denylist: [] },
    {
      enabled: true,
      allowlist: ["*.example.invalid"],
      denylist: ["excluded.example.invalid"],
    },
    { enabled: true, allowlist: ["*"], denylist: ["localhost"] },
  ]) {
    const data = await graphql(
      `
        mutation ($input: CreateUpstreamPluginInput!) {
          createUpstreamPlugin(input: $input) {
            upstream {
              id
            }
          }
        }
      `,
      { input: { pluginId: state.pluginId, ...config } },
    );
    state.ruleId = data.createUpstreamPlugin.upstream.id;
  }
  const settings = await rpc("getSettings");
  await rpc("updateSettings", {
    ...settings,
    enabled: true,
    autoStart: true,
    defaultProfile: "chrome_157_cft_PSK",
  });
  await saveState();
} else {
  ({ token } = JSON.parse(await readFile("/tmp/auth.json")));
  state = JSON.parse(await readFile("/tmp/state.json"));
  if (action === "enable-rule" || action === "disable-rule") {
    await toggleRule(action === "enable-rule");
  } else if (action === "enable-shaping" || action === "disable-shaping") {
    const settings = await rpc("getSettings");
    await rpc("updateSettings", {
      ...settings,
      enabled: action === "enable-shaping",
    });
  } else if (action.startsWith("assert-")) {
    const status = await rpc("getStatus");
    assert.equal(status.routingError, undefined);
    assert.equal(
      status.state,
      action === "assert-disabled-shaping" ? "idle" : "running",
    );
    assert.deepEqual(
      await rules(),
      state.rules,
      "startup changed rule configuration or rank",
    );
    await assertCapture("127.0.0.1", action === "assert-shaped");
    await assertCapture("localhost", false);
  } else throw new Error(`Unknown action: ${action}`);
}
