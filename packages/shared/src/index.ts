import type { DefinePluginPackageSpec } from "@caido/sdk-shared";

export type Result<T> =
  { kind: "Ok"; value: T } | { kind: "Error"; error: string };

export type TransportState =
  "idle" | "starting" | "running" | "stopping" | "error" | "unsupported";

export type TransportStatus = {
  state: TransportState;
  version: string;
  platform: string;
  port?: number;
  error?: string;
};

export type HeaderMode = "preserve" | "fill" | "enforce";

export type Settings = {
  enabled: boolean;
  autoStart: boolean;
  defaultProfile: string;
  headerMode: HeaderMode;
};

export type Profile = {
  id: string;
  label: string;
  family: "chromium" | "firefox" | "safari" | "mobile";
};

export type ActivityState = "routed" | "succeeded" | "failed";

export type ActivityEntry = {
  id: string;
  startedAt: number;
  completedAt?: number;
  state: ActivityState;
  method: string;
  scheme: "http" | "https";
  host: string;
  port: number;
  profile: string;
  statusCode?: number;
  protocol?: string;
  durationMs?: number;
  error?: string;
  warning?: string;
};

export type API = {
  getSettings: () => Result<Settings>;
  updateSettings: (settings: Settings) => Promise<Result<Settings>>;
  getStatus: () => Result<TransportStatus>;
  getProfiles: () => Result<Profile[]>;
  getActivity: () => Result<ActivityEntry[]>;
  clearActivity: () => Result<boolean>;
  startTransport: () => Promise<Result<TransportStatus>>;
  stopTransport: () => Promise<Result<TransportStatus>>;
};

export type Events = {
  "transport:status": (status: TransportStatus) => void;
  "settings:updated": (settings: Settings) => void;
  "activity:updated": (entry: ActivityEntry) => void;
  "activity:cleared": () => void;
};

export type Spec = DefinePluginPackageSpec<{
  manifestId: "impersonate-tls";
  api: API;
  events: Events;
}>;
