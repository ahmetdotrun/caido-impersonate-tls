import type { ActivityEntry } from "shared";

import type { TransportRequestEvent } from "./transport";
import type { BackendSDK } from "./types";

const MAX_ACTIVITY_ENTRIES = 250;
const MAX_ERROR_LENGTH = 500;

type BeginActivity = Pick<
  ActivityEntry,
  "host" | "method" | "port" | "profile" | "scheme"
>;

export class ActivityStore {
  private entries: ActivityEntry[] = [];
  private sequence = 0;

  public constructor(private readonly sdk: BackendSDK) {}

  public list(): ActivityEntry[] {
    return this.entries.map((entry) => ({ ...entry }));
  }

  public begin(input: BeginActivity): ActivityEntry {
    this.sequence += 1;
    const entry: ActivityEntry = {
      ...input,
      id: `${Date.now().toString(36)}-${this.sequence.toString(36)}`,
      startedAt: Date.now(),
      state: "routed",
    };

    this.entries.unshift(entry);
    if (this.entries.length > MAX_ACTIVITY_ENTRIES) {
      this.entries.length = MAX_ACTIVITY_ENTRIES;
    }
    this.publish(entry);
    return { ...entry };
  }

  public complete(event: TransportRequestEvent): void {
    const entry = this.entries.find((candidate) => candidate.id === event.id);
    if (entry === undefined) {
      return;
    }

    entry.completedAt = Date.now();
    entry.durationMs = event.durationMs;
    if (event.outcome === "succeeded") {
      entry.state = "succeeded";
      entry.statusCode = event.statusCode;
      entry.protocol = event.protocol;
      entry.error = undefined;
    } else {
      entry.state = "failed";
      entry.error = this.truncate(event.error);
    }
    this.publish(entry);
  }

  public fail(id: string, error: string): void {
    const entry = this.entries.find((candidate) => candidate.id === id);
    if (entry === undefined) {
      return;
    }

    entry.state = "failed";
    entry.completedAt = Date.now();
    entry.durationMs = entry.completedAt - entry.startedAt;
    entry.error = this.truncate(error);
    this.publish(entry);
  }

  public clear(): void {
    this.entries = [];
    this.sdk.api.send("activity:cleared");
  }

  private truncate(error: string): string {
    return error.length > MAX_ERROR_LENGTH
      ? `${error.slice(0, MAX_ERROR_LENGTH - 1)}…`
      : error;
  }

  private publish(entry: ActivityEntry): void {
    this.sdk.api.send("activity:updated", { ...entry });
  }
}
