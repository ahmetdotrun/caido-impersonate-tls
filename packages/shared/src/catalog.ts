import catalog from "../../../transport/internal/relay/catalog.json";

type HTTP2HeaderProfile = {
  order?: string[];
  orderOverrides?: { destinations: string[]; order: string[] }[];
  priorityByDestination?: Record<string, string>;
  priorityOverrides?: { destination: string; mode: string; value: string }[];
};

export type Profile = {
  id: string;
  label: string;
  family: "chromium" | "firefox" | "safari" | "okhttp" | "application";
  version?: string;
  platform?: string;
  source: "tls-client" | "local";
  verifiedBuilds?: string[];
  verification?: string;
  http2Headers: HTTP2HeaderProfile;
};

export const PLUGIN_VERSION = catalog.version;
export const TLS_CLIENT_VERSION = catalog.tlsClientVersion;
const headerProfiles: Record<string, HTTP2HeaderProfile> =
  catalog.http2HeaderProfiles;
export const PROFILES: readonly Profile[] = catalog.profiles.map((profile) => ({
  ...profile,
  http2Headers: headerProfiles[profile.http2Headers] ?? {},
})) as Profile[];
