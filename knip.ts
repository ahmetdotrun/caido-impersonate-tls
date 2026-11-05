import type { KnipConfig } from "knip";

const config: KnipConfig = {
  workspaces: {
    ".": {
      entry: [
        "caido.config.ts",
        "tests/browser/install.mjs",
        "tests/caido/restart.mjs",
      ],
      // Built from transport/cmd/fingerprint by tests/caido/Dockerfile.
      ignoreBinaries: ["fingerprint"],
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
