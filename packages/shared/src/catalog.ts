import catalog from "../../../transport/internal/relay/catalog.json";

export type Profile = {
  id: string;
  label: string;
  family: "chromium" | "firefox" | "safari" | "okhttp" | "application";
  version?: string;
  platform?: string;
  source: "tls-client" | "local";
  verifiedBuilds?: string[];
  verification?: string;
};

export const PLUGIN_VERSION = catalog.version;
export const TLS_CLIENT_VERSION = catalog.tlsClientVersion;
export const PROFILES: readonly Profile[] = catalog.profiles as Profile[];
