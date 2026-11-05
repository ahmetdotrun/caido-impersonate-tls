const assert = require("node:assert/strict");
const { writeFile } = require("node:fs/promises");
const path = require("node:path");
const test = require("node:test");
const { sandbox } = require("./helpers.cjs");
const {
  init,
} = require("../../.cache/backend-tests/packages/backend/src/index.js");

test("settings API reports startup failure after persisting settings and can repair a corrupt file", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const handlers = {};
  sdk.api.register = (name, handler) => {
    handlers[name] = handler;
  };
  sdk.events = { onUpstream() {} };
  sdk.meta.assetsPath = () => directory;
  await writeFile(path.join(directory, "settings.json"), "{broken");
  await init(sdk);
  assert.equal((await handlers.updateSettings(sdk, null)).kind, "Error");
  assert.equal(handlers.getSettings(sdk).kind, "Error");
  assert.equal(handlers.getStatus(sdk).value.state, "idle");
  const repair = {
    enabled: false,
    autoStart: false,
    defaultProfile: "chrome_152",
    headerMode: "preserve",
    maximumUploadMiB: 0,
  };
  assert.equal((await handlers.updateSettings(sdk, repair)).kind, "Ok");
  const result = await handlers.updateSettings(sdk, {
    ...repair,
    enabled: true,
    autoStart: true,
  });
  assert.equal(result.kind, "Error");
  assert.match(
    result.error,
    /Settings were saved, but the transport could not start/,
  );
  assert.equal(handlers.getSettings(sdk).value.enabled, true);
  assert.equal(handlers.getStatus(sdk).value.state, "error");
  await handlers.stopTransport(sdk);
});

test("helper readiness, shaping enablement and Activity remain distinct through the API", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const handlers = {};
  let upstream;
  sdk.api.register = (name, handler) => {
    handlers[name] = handler;
  };
  sdk.events = {
    onUpstream(handler) {
      upstream = handler;
    },
  };
  sdk.net = { connect: async () => ({}) };
  const settings = {
    enabled: false,
    autoStart: false,
    defaultProfile: "chrome_157_cft_PSK",
    headerMode: "preserve",
    maximumUploadMiB: 128,
  };
  await writeFile(
    path.join(directory, "settings.json"),
    JSON.stringify(settings),
  );
  await init(sdk);
  t.after(() => handlers.stopTransport(sdk));
  assert.equal((await handlers.startTransport(sdk)).value.state, "running");
  const headers = new Map();
  const spec = {
    getMethod: () => "GET",
    setHeader: (name, value) => headers.set(name, value),
  };
  const request = {
    getTls: () => true,
    getHost: () => "fixture.test",
    getPort: () => 443,
    toSpec: () => spec,
  };
  assert.equal(await upstream(sdk, request), undefined);
  assert.deepEqual(handlers.getActivity(sdk), { kind: "Ok", value: [] });

  const configured = await handlers.updateSettings(sdk, {
    ...settings,
    enabled: true,
    autoStart: true,
  });
  assert.equal(configured.kind, "Ok");
  assert.equal(configured.value.maximumUploadMiB, 128);
  await upstream(sdk, request);
  await upstream(sdk, request);
  assert.equal(
    headers.get("X-Caido-Impersonate-Profile"),
    settings.defaultProfile,
  );
  const activity = handlers.getActivity(sdk);
  assert.equal(activity.kind, "Ok");
  assert.equal(activity.value.length, 2);
  assert(activity.value.every((entry) => entry.state === "routed"));
  activity.value[0].state = "failed";
  assert.equal(handlers.getActivity(sdk).value[0].state, "routed");
  await handlers.stopTransport(sdk);
  assert(
    handlers
      .getActivity(sdk)
      .value.every((entry) => entry.state === "interrupted"),
  );
});

test("rule refresh runs after hook registration and exposes failures beside helper status", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const handlers = {};
  let hookRegistered = false;
  sdk.api.register = (name, handler) => {
    handlers[name] = handler;
  };
  sdk.events = {
    onUpstream() {
      hookRegistered = true;
    },
  };
  sdk.graphql.execute = async () => {
    assert.equal(hookRegistered, true);
    throw new Error("upstream API unavailable");
  };
  await writeFile(
    path.join(directory, "settings.json"),
    JSON.stringify({
      enabled: false,
      autoStart: false,
      defaultProfile: "chrome_157_cft_PSK",
      headerMode: "preserve",
      maximumUploadMiB: 128,
    }),
  );
  await init(sdk);
  t.after(() => handlers.stopTransport(sdk));
  const status = (await handlers.startTransport(sdk)).value;
  assert.equal(status.state, "running");
  assert.match(
    status.routingError,
    /re-save the rule.*upstream API unavailable/,
  );
  assert.equal(handlers.getStatus(sdk).value.routingError, status.routingError);
});
