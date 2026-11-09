import type { SDK } from "caido:plugin";
import type {
  ActivityEntry,
  EgressProfiles,
  EgressSummary,
  Profile,
  Result,
  Settings,
  Spec,
  TransportStatus,
} from "shared";

import { ActivityStore } from "./activity";
import { EgressStore } from "./egress";
import { isKnownProfile, PROFILES } from "./profiles";
import { RejectionService } from "./rejection";
import { refreshUpstreamRules } from "./routing";
import { SettingsStore } from "./settings";
import { TransportService } from "./transport";
import type { BackendSDK } from "./types";
import { createUpstreamHandler } from "./upstream";

const settingsStore = new SettingsStore();
const egressStore = new EgressStore();
let transport: TransportService | undefined;
let activity: ActivityStore | undefined;
let rejection: RejectionService | undefined;

async function setEgress(
  sdk: BackendSDK,
  profiles: EgressProfiles,
): Promise<Result<EgressSummary>> {
  try {
    return { kind: "Ok", value: await egressStore.set(sdk, profiles) };
  } catch (error) {
    return { kind: "Error", error: String(error) };
  }
}

async function getEgress(sdk: BackendSDK): Promise<Result<EgressSummary>> {
  try {
    return { kind: "Ok", value: await egressStore.get(sdk) };
  } catch (error) {
    return { kind: "Error", error: String(error) };
  }
}

function getSettings(_sdk: BackendSDK): Result<Settings> {
  try {
    return { kind: "Ok", value: settingsStore.get() };
  } catch (error) {
    return { kind: "Error", error: String(error) };
  }
}

async function updateSettings(
  sdk: BackendSDK,
  settings: Settings,
): Promise<Result<Settings>> {
  if (typeof settings !== "object" || settings === null) {
    return { kind: "Error", error: "Settings must be an object" };
  }
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

  if (
    Number.isSafeInteger(settings.maximumUploadMiB) === false ||
    settings.maximumUploadMiB < 0 ||
    settings.maximumUploadMiB > 1_048_576
  ) {
    return {
      kind: "Error",
      error:
        "Upload limit must be a whole number from 0 to 1048576 MiB (0 means unlimited)",
    };
  }

  try {
    const saved = await settingsStore.save(sdk, settings);
    if (saved.enabled && saved.autoStart && transport !== undefined) {
      const started = await transport.start();
      if (started.kind === "Error") {
        return {
          kind: "Error",
          error: `Settings were saved, but the transport could not start: ${started.error}`,
        };
      }
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
  await rejection?.close();
  rejection = new RejectionService(backendSDK);
  const activityStore = new ActivityStore(backendSDK);
  activity = activityStore;
  transport = new TransportService(
    backendSDK,
    (event) => activityStore.complete(event),
    (reason) => activityStore.interruptPending(reason),
  );

  sdk.api.register("getSettings", getSettings);
  sdk.api.register("updateSettings", updateSettings);
  sdk.api.register("getStatus", getStatus);
  sdk.api.register("getProfiles", getProfiles);
  sdk.api.register("getActivity", getActivity);
  sdk.api.register("clearActivity", clearActivity);
  sdk.api.register("startTransport", startTransport);
  sdk.api.register("stopTransport", stopTransport);
  sdk.api.register("set-egress", setEgress);
  sdk.api.register("get-egress", getEgress);

  sdk.events.onUpstream(
    createUpstreamHandler(
      transport,
      () => settingsStore.get(),
      activityStore,
      rejection,
    ),
  );

  try {
    await rejection.start();
  } catch {
    transport.setRoutingError(
      "Private rejection transport could not start; safe rejection is unavailable",
    );
    sdk.console.error(
      "[Impersonate TLS] Private rejection transport could not start",
    );
  }

  try {
    await refreshUpstreamRules(backendSDK);
  } catch (error) {
    const message = `Upstream rule refresh failed; re-save the rule in Caido Network settings: ${String(error)}`;
    transport.setRoutingError(message);
    sdk.console.error(`[Impersonate TLS] ${message}`);
  }

  try {
    await settingsStore.load(backendSDK);
  } catch (error) {
    sdk.console.error(`[Impersonate TLS] ${String(error)}`);
    return;
  }

  try {
    await egressStore.load(backendSDK);
  } catch {
    sdk.console.error(
      "[Impersonate TLS] Egress configuration is unavailable; named egress requests will fail closed",
    );
  }

  const settings = settingsStore.get();
  if (settings.enabled && settings.autoStart) {
    const result = await transport.start();
    if (result.kind === "Error") {
      sdk.console.error(`[Impersonate TLS] ${result.error}`);
    }
  }
}
