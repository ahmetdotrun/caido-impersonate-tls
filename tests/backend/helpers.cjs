const { mkdtemp, rm } = require("node:fs/promises");
const { tmpdir } = require("node:os");
const path = require("node:path");
const {
  RejectionService,
} = require("../../.cache/backend-tests/packages/backend/src/rejection.js");

async function sandbox(t) {
  // API unit tests exercise the forwarding helper separately. Native rejection
  // responses are checked in the disposable Caido integration fixture.
  t.mock.method(RejectionService.prototype, "start", async () => {});
  t.mock.method(RejectionService.prototype, "connect", async () => ({}));
  const directory = await mkdtemp(path.join(tmpdir(), "impersonate-backend-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const events = [];
  return {
    directory,
    events,
    sdk: {
      meta: {
        id: () => "impersonate-test-plugin",
        path: () => directory,
        assetsPath: () => path.resolve("assets/transport"),
      },
      api: { send: (...event) => events.push(event) },
      graphql: { execute: async () => ({ data: { upstreamPlugins: [] } }) },
      console: { log() {}, error() {} },
    },
  };
}

module.exports = { sandbox };
