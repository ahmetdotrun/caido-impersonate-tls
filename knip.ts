import type { KnipConfig } from "knip";

const config: KnipConfig = {
  workspaces: {
    ".": {
      entry: [
        "caido.config.ts",
        "tests/browser/install.mjs",
        "tests/caido/restart.mjs",
      ],
      // fingerprint is built by the Caido fixture; jq is provided by the release runner.
      ignoreBinaries: ["fingerprint", "jq"],
    },
    "packages/shared": {
      project: ["src/**/*.ts"],
    },
    "packages/backend": {
      project: ["src/**/*.ts"],
      ignoreDependencies: ["caido"],
    },
    "packages/frontend": {
      entry: ["src/index.ts"],
      project: ["src/**/*.{ts,vue}"],
    },
  },
};

export default config;
