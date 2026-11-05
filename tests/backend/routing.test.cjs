const assert = require("node:assert/strict");
const test = require("node:test");
const { sandbox } = require("./helpers.cjs");
const {
  refreshUpstreamRules,
} = require("../../.cache/backend-tests/packages/backend/src/routing.js");

test("startup refresh only reasserts this backend's enabled rules", async (t) => {
  const { sdk } = await sandbox(t);
  const own = { id: sdk.meta.id() };
  const rules = [
    { id: "active", enabled: true, plugin: own },
    { id: "disabled", enabled: false, plugin: own },
    { id: "foreign", enabled: true, plugin: { id: "another-plugin" } },
    { id: "missing-plugin", enabled: true, plugin: null },
    { id: "second-active", enabled: true, plugin: own },
  ];
  const refreshed = [];
  sdk.graphql.execute = async (query, variables) => {
    if (variables === undefined) return { data: { upstreamPlugins: rules } };
    // A same-value toggle leaves filters and rank under Caido's control.
    assert.match(query, /enabled: true/);
    assert.doesNotMatch(query, /allowlist|denylist|rank/);
    refreshed.push(variables.id);
    return {
      data: {
        toggleUpstreamPlugin: {
          upstream: rules.find((rule) => rule.id === variables.id),
        },
      },
    };
  };
  await refreshUpstreamRules(sdk);
  assert.deepEqual(refreshed, ["active", "second-active"]);
  assert.equal(rules[1].enabled, false);
});

test("an empty installation does not create or enable a rule", async (t) => {
  const { sdk } = await sandbox(t);
  let calls = 0;
  sdk.graphql.execute = async () => {
    calls += 1;
    return { data: { upstreamPlugins: [] } };
  };
  await refreshUpstreamRules(sdk);
  assert.equal(calls, 1);
});

test("query errors and missing data cannot masquerade as a successful refresh", async (t) => {
  const { sdk } = await sandbox(t);
  for (const response of [
    {},
    { errors: [{ message: "permission denied" }] },
    { data: { upstreamPlugins: [] }, errors: [{ message: "partial result" }] },
  ]) {
    sdk.graphql.execute = async () => response;
    await assert.rejects(
      refreshUpstreamRules(sdk),
      /Could not read upstream rules/,
    );
  }
});

test("failed or mismatched mutations are reported without retrying other rules", async (t) => {
  const { sdk } = await sandbox(t);
  const rule = { id: "active", enabled: true, plugin: { id: sdk.meta.id() } };
  for (const response of [
    {},
    { errors: [{ message: "permission denied" }] },
    { data: { toggleUpstreamPlugin: { upstream: null } } },
    {
      data: { toggleUpstreamPlugin: { upstream: { ...rule, enabled: false } } },
    },
    { data: { toggleUpstreamPlugin: { upstream: { ...rule, id: "other" } } } },
  ]) {
    let calls = 0;
    sdk.graphql.execute = async () => {
      calls += 1;
      if (calls === 1) return { data: { upstreamPlugins: [rule, rule] } };
      return response;
    };
    await assert.rejects(
      refreshUpstreamRules(sdk),
      /Could not refresh upstream rule active/,
    );
    assert.equal(calls, 2);
  }
});
