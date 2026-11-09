import type { Connection, RequestSpec, RequestSpecRaw } from "caido:utils";
import type { Settings } from "shared";

import type { ActivityStore } from "./activity";
import type { RejectionService } from "./rejection";
import type { TransportService } from "./transport";
import type { BackendSDK } from "./types";

const INTERNAL_HEADERS = {
  token: "X-Caido-Impersonate-Token",
  scheme: "X-Caido-Impersonate-Scheme",
  host: "X-Caido-Impersonate-Host",
  port: "X-Caido-Impersonate-Port",
  profile: "X-Caido-Impersonate-Profile",
  trace: "X-Caido-Impersonate-Trace",
  maxBody: "X-Caido-Impersonate-Max-Body-Bytes",
} as const;

export function createUpstreamHandler(
  transport: TransportService,
  getSettings: () => Settings,
  activity: ActivityStore,
  rejection: RejectionService,
): (
  sdk: BackendSDK,
  request: RequestSpecRaw,
) => Promise<
  | {
      connection: Connection;
      request: RequestSpec;
    }
  | undefined
> {
  return async (sdk, request) => {
    const spec = request.toSpec();
    const reject = async (): Promise<{
      connection: Connection;
      request: RequestSpec;
    }> => ({
      connection: await rejection.connect(),
      request: spec,
    });
    let settings: Settings;
    try {
      settings = getSettings();
    } catch {
      return reject();
    }
    if (settings.enabled === false) {
      if (spec.getHeader("X-Morpheus-Egress") !== undefined) {
        return reject();
      }
      return undefined;
    }

    const scheme = request.getTls() ? "https" : "http";
    const host = request.getHost();
    const port = request.getPort();
    const entry = activity.begin({
      host,
      method: spec.getMethod(),
      port,
      profile: settings.defaultProfile,
      scheme,
    });

    const details = transport.getConnectionDetails();
    if (details === undefined) {
      const message =
        "Impersonate TLS transport is unavailable; refusing to send an unshaped request";
      activity.fail(entry.id, message);
      return reject();
    }

    spec.setHeader(INTERNAL_HEADERS.token, details.token);
    spec.setHeader(INTERNAL_HEADERS.scheme, scheme);
    spec.setHeader(INTERNAL_HEADERS.host, host);
    spec.setHeader(INTERNAL_HEADERS.port, String(port));
    spec.setHeader(INTERNAL_HEADERS.profile, settings.defaultProfile);
    spec.setHeader(INTERNAL_HEADERS.trace, entry.id);
    spec.setHeader(
      INTERNAL_HEADERS.maxBody,
      String(settings.maximumUploadMiB * 1024 * 1024),
    );

    try {
      const connection = await sdk.net.connect(
        `http://127.0.0.1:${details.port}`,
      );

      return { connection, request: spec };
    } catch (error) {
      activity.fail(entry.id, String(error));
      return reject();
    }
  };
}
