import { inject, type InjectionKey, type Plugin } from "vue";

import type { FrontendSDK } from "../types";

const KEY: InjectionKey<FrontendSDK> = Symbol("FrontendSDK");

export const SDKPlugin: Plugin = (app, sdk: FrontendSDK) => {
  app.provide(KEY, sdk);
};

export function useSDK(): FrontendSDK {
  const sdk = inject(KEY);
  if (sdk === undefined) {
    throw new Error("Frontend SDK is unavailable");
  }
  return sdk;
}
