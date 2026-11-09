const assert = require("node:assert/strict");
const {
  readFile,
  readdir,
  stat,
  writeFile,
  chmod,
} = require("node:fs/promises");
const path = require("node:path");
const test = require("node:test");
const {
  EgressStore,
  egressFilePath,
} = require("../../.cache/backend-tests/packages/backend/src/egress.js");
const { sandbox } = require("./helpers.cjs");

test("egress names are independent caller labels; persistence is owner-only and summaries redact credentials", async (t) => {
  const { sdk } = await sandbox(t);
  const store = new EgressStore();
  assert.deepEqual(await store.load(sdk), {});
  const profiles = JSON.parse(
    '{"not a Caido color":{"proxy":"http://user%40name:private-password@proxy.invalid:8080"},"__proto__":{"proxy":"socks5h://alice:private-password@[::1]:1080"},"İstanbul":{"proxy":"http://127.0.0.1"}}',
  );
  const result = await store.set(sdk, profiles);
  assert.deepEqual(result, {
    "not a Caido color": { proxy: "http://proxy.invalid:8080" },
    ["__proto__"]: { proxy: "socks5h://[::1]:1080" },
    İstanbul: { proxy: "http://127.0.0.1" },
  });
  assert(!JSON.stringify(result).includes("private-password"));
  assert.equal((await stat(egressFilePath(sdk))).mode & 0o777, 0o600);
  assert.equal(
    (await stat(path.dirname(egressFilePath(sdk)))).mode & 0o777,
    0o700,
  );
  assert(
    (await readFile(egressFilePath(sdk), "utf8")).includes("private-password"),
  );
  assert.deepEqual(await new EgressStore().load(sdk), result);
  assert.deepEqual(await store.get(sdk), result);
});

test("invalid replacement leaves the previous configuration intact without reflecting secrets", async (t) => {
  const { sdk } = await sandbox(t);
  const store = new EgressStore();
  await store.load(sdk);
  const kept = await store.set(sdk, {
    kept: { proxy: "http://proxy.invalid:8080" },
  });
  for (const input of [
    null,
    [],
    { bad: {} },
    { bad: { proxy: "http://user:secret@" } },
    { bad: { proxy: "socks5://user:secret@proxy.invalid:1080" } },
    { bad: { proxy: "https://user:secret@proxy.invalid" } },
    { bad: { proxy: "http://user:secret@proxy.invalid/path" } },
    { bad: { proxy: "http://user:secret@proxy.invalid?token=secret" } },
    { bad: { proxy: "http://user:secret@proxy.invalid#secret" } },
    { " bad": { proxy: "http://proxy.invalid" } },
    { "bad\r\nname": { proxy: "http://proxy.invalid" } },
    { bad: { proxy: "http://proxy.invalid:0" } },
    { bad: { proxy: "http://proxy.invalid", tlsProfile: "unimplemented" } },
  ]) {
    await assert.rejects(
      async () => store.set(sdk, input),
      (error) => !String(error).includes("secret"),
    );
    assert.deepEqual(await store.get(sdk), kept);
  }
});

test("concurrent replacements are atomic, snapshot their input, and support clearing the map", async (t) => {
  const { sdk } = await sandbox(t);
  const store = new EgressStore();
  await store.load(sdk);
  const input = { first: { proxy: "http://one.invalid:8080" } };
  const first = store.set(sdk, input);
  input.first.proxy = "http://should-not-persist.invalid";
  const second = store.set(sdk, {
    second: { proxy: "socks5h://two.invalid:1080" },
  });
  assert.deepEqual(await first, {
    first: { proxy: "http://one.invalid:8080" },
  });
  assert.deepEqual(await store.get(sdk), await second);
  assert.deepEqual(await readdir(path.dirname(egressFilePath(sdk))), [
    "egress.json",
  ]);
  assert.deepEqual(await store.set(sdk, {}), {});
  assert.deepEqual(await store.get(sdk), {});
});

test("corrupt stored configuration is preserved, errors are sanitized, and set-egress can repair it", async (t) => {
  const { sdk } = await sandbox(t);
  const store = new EgressStore();
  await store.load(sdk);
  await writeFile(egressFilePath(sdk), '{"secret-password":broken');
  await chmod(egressFilePath(sdk), 0o644);
  await assert.rejects(store.load(sdk), /unavailable or invalid/);
  await assert.rejects(
    store.get(sdk),
    (error) => !String(error).includes("secret-password"),
  );
  assert.equal(
    await readFile(egressFilePath(sdk), "utf8"),
    '{"secret-password":broken',
  );
  assert.equal((await stat(egressFilePath(sdk))).mode & 0o777, 0o600);
  await store.set(sdk, {});
  assert.deepEqual(await store.get(sdk), {});
});
