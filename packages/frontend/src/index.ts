import { Classic } from "@caido/primevue";
import PrimeVue from "primevue/config";
import { createApp } from "vue";

import { SDKPlugin } from "./plugins/sdk";
import "./styles/index.css";
import type { FrontendSDK } from "./types";
import App from "./views/App.vue";

export function init(sdk: FrontendSDK): void {
  const app = createApp(App);
  app.use(PrimeVue, {
    unstyled: true,
    pt: Classic,
  });
  app.use(SDKPlugin, sdk);

  const root = document.createElement("div");
  root.id = "plugin--impersonate-tls";
  root.style.height = "100%";
  root.style.width = "100%";
  app.mount(root);

  sdk.navigation.addPage("/impersonate-tls", { body: root });
  sdk.sidebar.registerItem("Impersonate TLS", "/impersonate-tls", {
    icon: "fas fa-mask-face",
  });
}
