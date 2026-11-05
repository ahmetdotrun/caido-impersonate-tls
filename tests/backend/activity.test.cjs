const assert = require("node:assert/strict");
const test = require("node:test");
const {
  ActivityStore,
} = require("../../.cache/backend-tests/packages/backend/src/activity.js");
const {
  mergeActivity,
} = require("../../.cache/backend-tests/packages/shared/src/activity.js");

const input = {
  host: "fixture.test",
  method: "GET",
  port: 443,
  profile: "chrome_152",
  scheme: "https",
};

test("transport interruption finalizes pending activity and preserves completed results", () => {
  const store = new ActivityStore({ api: { send() {} } });
  const complete = store.begin(input);
  store.complete({
    id: complete.id,
    outcome: "succeeded",
    statusCode: 200,
    protocol: "HTTP/2.0",
    durationMs: 1,
  });
  const pending = store.begin(input);
  store.interruptPending("Transport stopped");
  store.fail(pending.id, "late connection error");
  store.complete({
    id: pending.id,
    outcome: "succeeded",
    statusCode: 200,
    protocol: "HTTP/2.0",
    durationMs: 2,
  });
  assert.equal(
    store.list().find((entry) => entry.id === complete.id).state,
    "succeeded",
  );
  assert.equal(
    store.list().find((entry) => entry.id === pending.id).state,
    "interrupted",
  );
  assert.equal(
    store.list().find((entry) => entry.id === pending.id).error,
    "Transport stopped",
  );
});

test("a stale live row cannot replace a completed snapshot row", () => {
  const stale = { ...input, id: "same", startedAt: 1, state: "routed" };
  const completed = {
    ...stale,
    state: "succeeded",
    completedAt: 2,
    statusCode: 200,
  };
  assert.equal(mergeActivity([completed], [stale])[0].state, "succeeded");
});

test("requests started in the same millisecond retain numeric invocation order", () => {
  const rows = [35, 36].map((sequence) => ({
    ...input,
    id: `same-${sequence.toString(36)}`,
    sequence,
    startedAt: 1,
    state: "routed",
  }));
  assert.equal(mergeActivity([], rows)[0].sequence, 36);
});

test("activity snapshots and live updates stay ordered, bounded and deduplicated", () => {
  const snapshot = Array.from({ length: 300 }, (_, i) => ({
    ...input,
    id: `entry-${i}`,
    startedAt: i,
    state: "routed",
  }));
  const update = { ...snapshot[299], state: "succeeded" };
  const merged = mergeActivity(snapshot, [
    update,
    { ...snapshot[0], id: "new", startedAt: 301 },
  ]);
  assert.equal(merged.length, 250);
  assert.equal(merged[0].id, "new");
  assert.equal(merged[1].state, "succeeded");
  assert.equal(new Set(merged.map((entry) => entry.id)).size, 250);
  assert(!merged.some((entry) => entry.id === "entry-0"));
});
