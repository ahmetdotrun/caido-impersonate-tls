const assert = require("node:assert/strict");
const { mkdir, readFile, readdir, writeFile } = require("node:fs/promises");
const path = require("node:path");
const test = require("node:test");
const {
  SettingsStore,
} = require("../../.cache/backend-tests/packages/backend/src/settings.js");
const {
  SerialQueue,
} = require("../../.cache/backend-tests/packages/backend/src/serial.js");
const { sandbox } = require("./helpers.cjs");

test("serial operations retain order after a rejection", async () => {
  const queue = new SerialQueue();
  const order = [];
  const first = queue.run(async () => {
    order.push(1);
    throw new Error("expected");
  });
  const second = queue.run(async () => {
    order.push(2);
    return "ok";
  });
  await assert.rejects(first, /expected/);
  assert.equal(await second, "ok");
  assert.deepEqual(order, [1, 2]);
});

test("concurrent settings saves remain ordered, valid and consistent with memory", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const store = new SettingsStore();
  assert.throws(() => store.get(), /not been loaded/);
  const initial = await store.load(sdk);
  for (let i = 0; i < 30; i++) {
    const first = { ...initial, maximumUploadMiB: 10 + i };
    const last = { ...initial, maximumUploadMiB: 100 + i };
    assert.deepEqual(
      await Promise.all([store.save(sdk, first), store.save(sdk, last)]),
      [first, last],
    );
    assert.deepEqual(store.get(), last);
    assert.deepEqual(
      JSON.parse(await readFile(path.join(directory, "settings.json"), "utf8")),
      last,
    );
  }
  assert.deepEqual(await readdir(directory), ["settings.json"]);
});

test("save snapshots its input and load waits for earlier saves", async (t) => {
  const { sdk } = await sandbox(t);
  const store = new SettingsStore();
  const input = { ...(await store.load(sdk)), maximumUploadMiB: 5 };
  const saving = store.save(sdk, input);
  input.maximumUploadMiB = 999;
  const loaded = store.load(sdk);
  assert.equal((await saving).maximumUploadMiB, 5);
  assert.equal((await loaded).maximumUploadMiB, 5);
});

test("failed writes keep the last settings and do not poison subsequent saves", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const store = new SettingsStore();
  const initial = await store.load(sdk);
  const failingDirectory = path.join(directory, "failed");
  await mkdir(path.join(failingDirectory, "settings.json"), {
    recursive: true,
  });
  const failingSDK = {
    ...sdk,
    meta: { ...sdk.meta, path: () => failingDirectory },
  };
  await assert.rejects(
    store.save(failingSDK, { ...initial, maximumUploadMiB: 9 }),
  );
  assert.deepEqual(store.get(), initial);
  assert.deepEqual(await readdir(failingDirectory), ["settings.json"]);
  assert.equal(
    (await store.save(sdk, { ...initial, maximumUploadMiB: 12 }))
      .maximumUploadMiB,
    12,
  );
});

test("legacy settings migrate without changing profile or enablement", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const legacy = {
    enabled: false,
    autoStart: false,
    defaultProfile: "chrome_152_cft",
    headerMode: "preserve",
  };
  await writeFile(
    path.join(directory, "settings.json"),
    JSON.stringify(legacy),
  );
  assert.deepEqual(await new SettingsStore().load(sdk), {
    ...legacy,
    maximumUploadMiB: 0,
  });
});

test("corrupt settings are preserved and cannot silently enable a default profile", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const store = new SettingsStore();
  const initial = await store.load(sdk);
  const filename = path.join(directory, "settings.json");
  await writeFile(filename, "{broken");
  await assert.rejects(store.load(sdk), /Failed to load settings/);
  assert.throws(() => store.get(), /Failed to load settings/);
  assert.equal(await readFile(filename, "utf8"), "{broken");
  await store.save(sdk, initial);
  assert.deepEqual(store.get(), initial);
  await assert.rejects(
    store.save(sdk, { ...initial, maximumUploadMiB: -1 }),
    /invalid/,
  );
  assert.deepEqual(store.get(), initial);
});
