const assert = require("node:assert/strict");
const { once } = require("node:events");
const { setTimeout: delay } = require("node:timers/promises");
const test = require("node:test");
const {
  TransportService,
} = require("../../.cache/backend-tests/packages/backend/src/transport.js");
const { sandbox } = require("./helpers.cjs");

async function serviceFixture(t, sdk) {
  const service = new TransportService(sdk, () => {});
  const children = [];
  const waitUntilReady = service.waitUntilReady.bind(service);
  service.waitUntilReady = (child) => {
    children.push({ child, kill: child.kill.bind(child) });
    return waitUntilReady(child);
  };
  t.after(async () => {
    service.stopHeartbeat();
    for (const { child, kill } of children) {
      if (child.exitCode !== null || child.signalCode !== null) continue;
      const closed = once(child, "close");
      kill("SIGTERM");
      await Promise.race([closed, delay(1000)]);
      if (child.exitCode === null && child.signalCode === null) {
        kill("SIGKILL");
        await closed;
      }
    }
    await service.stop();
  });
  return { service, children };
}

test("overlapping stop/start waits for exit and retains the new process", async (t) => {
  const { sdk } = await sandbox(t);
  const { service, children } = await serviceFixture(t, sdk);
  assert.equal(
    (await service.start()).kind,
    "Ok",
    "Build transport assets before running backend integration tests",
  );
  const old = children[0];
  old.child.kill = (signal) => {
    if (signal === "SIGTERM") {
      setTimeout(() => old.kill(signal), 150);
      return true;
    }
    return old.kill(signal);
  };
  const stopping = service.stop();
  const starting = service.start();
  assert.equal((await stopping).value.state, "idle");
  assert.equal((await starting).value.state, "running");
  assert.equal(service.getStatus().state, "running");
  assert.notEqual(service.child, old.child);
  assert(service.getConnectionDetails());
  assert.equal(
    children.filter(
      ({ child }) => child.exitCode === null && child.signalCode === null,
    ).length,
    1,
  );
  assert.equal((await service.stop()).value.state, "idle");
});

test("overlapping starts use one process and start/stop ordering is retained", async (t) => {
  const { sdk } = await sandbox(t);
  const { service, children } = await serviceFixture(t, sdk);
  const results = await Promise.all([
    service.start(),
    service.start(),
    service.stop(),
  ]);
  assert.deepEqual(
    results.map((r) => r.value?.state),
    ["running", "running", "idle"],
  );
  assert.equal(children.length, 1);
  assert.equal(service.getConnectionDetails(), undefined);
});

test("same-version replacement does not overwrite the executing binary", async (t) => {
  const { sdk } = await sandbox(t);
  const first = (await serviceFixture(t, sdk)).service;
  const second = (await serviceFixture(t, sdk)).service;
  assert.equal((await first.start()).kind, "Ok");
  assert.equal((await second.start()).kind, "Ok");
  const replacement = second.getConnectionDetails();
  await first.stop();
  assert.deepEqual(second.getConnectionDetails(), replacement);
});

test("a failed start does not block a corrected start", async (t) => {
  const { sdk, directory } = await sandbox(t);
  const assetsPath = sdk.meta.assetsPath;
  sdk.meta.assetsPath = () => directory;
  const { service } = await serviceFixture(t, sdk);
  assert.equal((await service.start()).kind, "Error");
  sdk.meta.assetsPath = assetsPath;
  assert.equal((await service.start()).value.state, "running");
});

test(
  "stop waits for close after escalation to SIGKILL",
  { timeout: 10_000 },
  async (t) => {
    const { sdk } = await sandbox(t);
    const { service, children } = await serviceFixture(t, sdk);
    assert.equal((await service.start()).kind, "Ok");
    const current = children[0];
    const signals = [];
    current.child.kill = (signal) => {
      signals.push(signal);
      // Exercise escalation against a real process without making it exit on TERM.
      if (signal === "SIGTERM") return true;
      return current.kill(signal);
    };
    assert.equal((await service.stop()).value.state, "idle");
    assert.deepEqual(signals, ["SIGTERM", "SIGKILL"]);
    assert.equal(current.child.signalCode, "SIGKILL");
    assert.equal(service.child, undefined);
    assert.equal(service.getConnectionDetails(), undefined);
  },
);

test("unexpected exit clears connection details and permits restart", async (t) => {
  const { sdk } = await sandbox(t);
  const { service, children } = await serviceFixture(t, sdk);
  assert.equal((await service.start()).kind, "Ok");
  const current = children[0];
  const closed = once(current.child, "close");
  current.kill("SIGKILL");
  await closed;
  assert.equal(service.getStatus().state, "error");
  assert.equal(service.getConnectionDetails(), undefined);
  assert.equal((await service.start()).value.state, "running");
  assert.notEqual(service.child, current.child);
});
