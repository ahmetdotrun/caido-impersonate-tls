import path from "path";

import tailwindCaido from "@caido/tailwindcss";
import { defineConfig } from "@caido-community/dev";
import vue from "@vitejs/plugin-vue";
import prefixwrap from "postcss-prefixwrap";
import tailwindcss from "tailwindcss";
// @ts-expect-error tailwindcss-primeui does not publish TypeScript declarations.
import tailwindPrimeui from "tailwindcss-primeui";

const id = "impersonate-tls";

export default defineConfig({
  id,
  name: "Impersonate TLS",
  description:
    "Apply browser-like TLS and HTTP transport profiles to domain-scoped Caido traffic.",
  version: "0.1.0",
  author: {
    name: "A. Eren Kilic",
    email: "aerenkilic@pm.me",
    url: "https://github.com/ahmetdotrun",
  },
  plugins: [
    {
      kind: "backend",
      id: "impersonate-tls-backend",
      root: "packages/backend",
      assets: ["./assets/transport", "./LICENSE"],
    },
    {
      kind: "frontend",
      id: "impersonate-tls-frontend",
      root: "packages/frontend",
      backend: {
        id: "impersonate-tls-backend",
      },
      vite: {
        plugins: [vue()],
        build: {
          rollupOptions: {
            external: ["@caido/frontend-sdk", "vue"],
          },
        },
        resolve: {
          alias: [
            {
              find: "@",
              replacement: path.resolve(__dirname, "packages/frontend/src"),
            },
          ],
        },
        css: {
          postcss: {
            plugins: [
              prefixwrap(`#plugin--${id}`),
              tailwindcss({
                corePlugins: {
                  preflight: false,
                },
                content: [
                  "./packages/frontend/src/**/*.{vue,ts}",
                  "./node_modules/@caido/primevue/dist/primevue.mjs",
                ],
                darkMode: ["selector", '[data-mode="dark"]'],
                plugins: [tailwindPrimeui, tailwindCaido],
              }),
            ],
          },
        },
      },
    },
  ],
});
