import type { SDK } from "caido:plugin";
import type {
  ActivityEntry,
  Profile,
  Result,
  Settings,
  Spec,
  TransportStatus,
} from "shared";

import { ActivityStore } from "./activity";
import { isKnownProfile, PROFILES } from "./profiles";
import { SettingsStore } from "./settings";
import { TransportService } from "./transport";
import type { BackendSDK } from "./types";
import { createUpstreamHandler } from "./upstream";

const settingsStore = new SettingsStore();
let transport: TransportService | undefined;
let activity: ActivityStore | undefined;

function getSettings(_sdk: BackendSDK): Result<Settings> {
  return { kind: "Ok", value: settingsStore.get() };
}

async function updateSettings(
  sdk: BackendSDK,
  settings: Settings,
): Promise<Result<Settings>> {
  if (
    typeof settings.enabled !== "boolean" ||
    typeof settings.autoStart !== "boolean"
  ) {
    return { kind: "Error", error: "Invalid boolean settings" };
  }

  if (isKnownProfile(settings.defaultProfile) === false) {
    return {
      kind: "Error",
      error: `Unknown transport profile: ${settings.defaultProfile}`,
    };
  }

  if (settings.headerMode !== "preserve") {
    return {
      kind: "Error",
      error: "Only preserve header mode is available in the current release",
    };
  }

  try {
    const saved = await settingsStore.save(sdk, settings);
    if (settings.enabled && settings.autoStart && transport !== undefined) {
      await transport.start();
    }
    return { kind: "Ok", value: saved };
  } catch (error) {
    return { kind: "Error", error: String(error) };
  }
}

function getStatus(_sdk: BackendSDK): Result<TransportStatus> {
  if (transport === undefined) {
    return { kind: "Error", error: "Transport service is not initialized" };
  }

  return { kind: "Ok", value: transport.getStatus() };
}

function getProfiles(_sdk: BackendSDK): Result<Profile[]> {
  return { kind: "Ok", value: [...PROFILES] };
}

function getActivity(_sdk: BackendSDK): Result<ActivityEntry[]> {
  if (activity === undefined) {
    return { kind: "Error", error: "Activity store is not initialized" };
  }
  return { kind: "Ok", value: activity.list() };
}

function clearActivity(_sdk: BackendSDK): Result<boolean> {
  if (activity === undefined) {
    return { kind: "Error", error: "Activity store is not initialized" };
  }
  activity.clear();
  return { kind: "Ok", value: true };
}

async function startTransport(
  _sdk: BackendSDK,
): Promise<Result<TransportStatus>> {
  if (transport === undefined) {
    return { kind: "Error", error: "Transport service is not initialized" };
  }

  return transport.start();
}

async function stopTransport(
  _sdk: BackendSDK,
): Promise<Result<TransportStatus>> {
  if (transport === undefined) {
    return { kind: "Error", error: "Transport service is not initialized" };
  }

  return transport.stop();
}

export async function init(sdk: SDK<Spec>): Promise<void> {
  const backendSDK: BackendSDK = sdk;
  const activityStore = new ActivityStore(backendSDK);
  activity = activityStore;
  transport = new TransportService(backendSDK, (event) =>
    activityStore.complete(event),
  );

  sdk.api.register("getSettings", getSettings);
  sdk.api.register("updateSettings", updateSettings);
  sdk.api.register("getStatus", getStatus);
  sdk.api.register("getProfiles", getProfiles);
  sdk.api.register("getActivity", getActivity);
  sdk.api.register("clearActivity", clearActivity);
  sdk.api.register("startTransport", startTransport);
  sdk.api.register("stopTransport", stopTransport);

  await settingsStore.load(backendSDK);
  sdk.events.onUpstream(
    createUpstreamHandler(transport, () => settingsStore.get(), activityStore),
  );

  const settings = settingsStore.get();
  if (settings.enabled && settings.autoStart) {
    const result = await transport.start();
    if (result.kind === "Error") {
      sdk.console.error(`[Impersonate TLS] ${result.error}`);
    }
  }
}
