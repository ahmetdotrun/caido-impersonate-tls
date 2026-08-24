import { mkdir, readFile, rename, writeFile } from "fs/promises";
import path from "path";

import type { Settings } from "shared";

import { isKnownProfile } from "./profiles";
import type { BackendSDK } from "./types";

const SETTINGS_FILE = "settings.json";

const DEFAULT_SETTINGS: Settings = {
  enabled: true,
  autoStart: true,
  defaultProfile: "chrome_146",
  headerMode: "preserve",
};

export class SettingsStore {
  private settings: Settings = { ...DEFAULT_SETTINGS };

  public get(): Settings {
    return { ...this.settings };
  }

  public async load(sdk: BackendSDK): Promise<Settings> {
    const settingsPath = this.getPath(sdk);

    try {
      const content = await readFile(settingsPath, "utf8");
      this.settings = this.parse(JSON.parse(content) as unknown);
    } catch (error) {
      if (this.isMissingFile(error) === false) {
        sdk.console.error(
          `[Impersonate TLS] Failed to load settings: ${String(error)}`,
        );
      }
    }

    return this.get();
  }

  public async save(sdk: BackendSDK, settings: Settings): Promise<Settings> {
    const settingsPath = this.getPath(sdk);
    const temporaryPath = `${settingsPath}.tmp`;

    await mkdir(path.dirname(settingsPath), { recursive: true });
    await writeFile(temporaryPath, JSON.stringify(settings, null, 2));
    await rename(temporaryPath, settingsPath);

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
      candidate.headerMode !== "preserve"
    ) {
      throw new Error("Settings file contains invalid values");
    }

    return {
      enabled: candidate.enabled,
      autoStart: candidate.autoStart,
      defaultProfile: candidate.defaultProfile,
      headerMode: candidate.headerMode,
    };
  }
}
