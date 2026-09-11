import { randomBytes } from "crypto";
import { mkdir, open, readFile, rename, rm } from "fs/promises";
import path from "path";

import type { Settings } from "shared";

import { isKnownProfile } from "./profiles";
import { SerialQueue } from "./serial";
import type { BackendSDK } from "./types";

const SETTINGS_FILE = "settings.json";

const DEFAULT_SETTINGS: Settings = {
  enabled: true,
  autoStart: true,
  defaultProfile: "chrome_152",
  headerMode: "preserve",
  maximumUploadMiB: 0,
};

export class SettingsStore {
  private settings: Settings | undefined;
  private readonly operations = new SerialQueue();
  private loadError = "Settings have not been loaded";

  public get(): Settings {
    if (this.settings === undefined) {
      throw new Error(this.loadError);
    }
    return { ...this.settings };
  }

  public load(sdk: BackendSDK): Promise<Settings> {
    return this.operations.run(() => this.loadInternal(sdk));
  }

  private async loadInternal(sdk: BackendSDK): Promise<Settings> {
    const settingsPath = this.getPath(sdk);

    try {
      const content = await readFile(settingsPath, "utf8");
      this.settings = this.parse(JSON.parse(content) as unknown);
    } catch (error) {
      if (this.isMissingFile(error)) {
        this.settings = { ...DEFAULT_SETTINGS };
      } else {
        // A damaged or unreadable file is not permission to change the profile,
        // upload policy, or enablement. Preserve the file and require repair.
        this.settings = undefined;
        this.loadError = `Failed to load settings: ${String(error)}`;
        throw new Error(this.loadError);
      }
    }

    return this.get();
  }

  public async save(sdk: BackendSDK, settings: Settings): Promise<Settings> {
    const snapshot = this.parse(settings);
    return this.operations.run(() => this.saveInternal(sdk, snapshot));
  }

  private async saveInternal(
    sdk: BackendSDK,
    settings: Settings,
  ): Promise<Settings> {
    const settingsPath = this.getPath(sdk);
    const temporaryPath = `${settingsPath}.${randomBytes(16).toString("hex")}.tmp`;

    await mkdir(path.dirname(settingsPath), { recursive: true });
    try {
      const file = await open(temporaryPath, "wx", 0o600);
      try {
        await file.writeFile(JSON.stringify(settings, null, 2));
      } finally {
        await file.close();
      }
      await rename(temporaryPath, settingsPath);
    } finally {
      await rm(temporaryPath, { force: true });
    }

    this.settings = { ...settings };
    sdk.api.send("settings:updated", this.get());
    return this.get();
  }

  private getPath(sdk: BackendSDK): string {
    return path.join(sdk.meta.path(), SETTINGS_FILE);
  }

  private isMissingFile(error: unknown): boolean {
    if (typeof error !== "object" || error === null || !("code" in error)) {
      return false;
    }

    return error.code === "ENOENT";
  }

  private parse(value: unknown): Settings {
    if (typeof value !== "object" || value === null) {
      throw new Error("Settings file must contain an object");
    }

    const candidate = value as Partial<Settings>;
    if (
      typeof candidate.enabled !== "boolean" ||
      typeof candidate.autoStart !== "boolean" ||
      typeof candidate.defaultProfile !== "string" ||
      isKnownProfile(candidate.defaultProfile) === false ||
      candidate.headerMode !== "preserve" ||
      (candidate.maximumUploadMiB !== undefined &&
        (Number.isSafeInteger(candidate.maximumUploadMiB) === false ||
          candidate.maximumUploadMiB < 0 ||
          candidate.maximumUploadMiB > 1_048_576))
    ) {
      throw new Error("Settings file contains invalid values");
    }

    return {
      enabled: candidate.enabled,
      autoStart: candidate.autoStart,
      defaultProfile: candidate.defaultProfile,
      headerMode: candidate.headerMode,
      maximumUploadMiB: candidate.maximumUploadMiB ?? 0,
    };
  }
}
