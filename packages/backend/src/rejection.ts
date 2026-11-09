import type { Connection } from "caido:utils";

import { TransportService } from "./transport";
import type { BackendSDK } from "./types";

// Caido treats a thrown upstream-hook exception as permission to go direct.
// Keep a separate native responder alive when the forwarding helper is stopped.
export class RejectionService {
  private readonly service: TransportService;

  public constructor(private readonly sdk: BackendSDK) {
    this.service = new TransportService(
      sdk,
      () => {},
      () => {},
      true,
    );
  }

  public async start(): Promise<void> {
    const result = await this.service.start();
    if (result.kind === "Error")
      throw new Error("Private rejection transport could not start");
  }

  public async connect(): Promise<Connection> {
    await this.start();
    const details = this.service.getConnectionDetails();
    if (details === undefined)
      throw new Error("Private rejection transport is unavailable");
    return this.sdk.net.connect(`http://127.0.0.1:${details.port}`);
  }

  public async close(): Promise<void> {
    await this.service.stop();
  }
}
