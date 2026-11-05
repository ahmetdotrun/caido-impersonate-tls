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
