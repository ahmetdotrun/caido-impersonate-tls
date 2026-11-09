import { randomBytes } from "crypto";
import { chmod, mkdir, open, readdir, readFile, rename, rm } from "fs/promises";
import path from "path";
import { URL } from "url";

import type { EgressProfiles, EgressSummary } from "shared";

import { SerialQueue } from "./serial";
import type { BackendSDK } from "./types";

const FILE = "egress.json";

export function egressFilePath(sdk: BackendSDK): string {
  return path.join(sdk.meta.path(), "private", FILE);
}

function parseProfiles(value: unknown): EgressProfiles {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Egress profiles must be an object keyed by name");
  }
  const entries = Object.entries(value);
  if (entries.length > 256)
    throw new Error("At most 256 egress profiles are supported");
  return Object.fromEntries(
    entries.map(([name, profile]: [string, unknown]) => {
      const characters = Array.from(name);
      if (
        name.trim() !== name ||
        name === "" ||
        characters.length > 128 ||
        characters.some(
          (character) =>
            character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127,
        )
      ) {
        throw new Error(
          "Egress names must be 1–128 characters without control characters or surrounding whitespace",
        );
      }
      if (
        typeof profile !== "object" ||
        profile === null ||
        Array.isArray(profile) ||
        Object.keys(profile).length !== 1 ||
        !("proxy" in profile) ||
        typeof profile.proxy !== "string"
      ) {
        throw new Error("Each egress profile must contain only a proxy URL");
      }
      const raw = profile.proxy;
      let proxy: URL;
      try {
        if (
          raw.length > 4096 ||
          /\s/u.test(raw) ||
          raw.includes("?") ||
          raw.includes("#")
        )
          throw new Error();
        proxy = new URL(raw);
        decodeURIComponent(proxy.username);
        decodeURIComponent(proxy.password);
      } catch {
        throw new Error("Invalid egress proxy URL");
      }
      if (
        (proxy.protocol !== "http:" && proxy.protocol !== "socks5h:") ||
        proxy.hostname === "" ||
        proxy.hostname.includes("%") ||
        proxy.hostname.includes("\\") ||
        proxy.href.length > 4096 ||
        proxy.search !== "" ||
        proxy.hash !== "" ||
        (proxy.pathname !== "" && proxy.pathname !== "/") ||
        (proxy.port !== "" &&
          (!/^\d+$/u.test(proxy.port) ||
            Number(proxy.port) < 1 ||
            Number(proxy.port) > 65535))
      ) {
        throw new Error(
          "Egress proxies must use http:// or socks5h:// with a host, optional credentials and port, and no path, query or fragment",
        );
      }
      return [name, { proxy: proxy.href }];
    }),
  );
}

function summary(profiles: EgressProfiles): EgressSummary {
  return Object.fromEntries(
    Object.entries(profiles).map(([name, profile]) => {
      const proxy = new URL(profile.proxy);
      return [name, { proxy: `${proxy.protocol}//${proxy.host}` }];
    }),
  );
}

export class EgressStore {
  private readonly operations = new SerialQueue();

  public load(sdk: BackendSDK): Promise<EgressSummary> {
    return this.operations.run(async () => {
      await this.prepareDirectory(sdk);
      if (!(await readdir(path.dirname(egressFilePath(sdk)))).includes(FILE)) {
        await this.write(sdk, {});
      }
      return this.read(sdk);
    });
  }

  public get(sdk: BackendSDK): Promise<EgressSummary> {
    return this.operations.run(() => this.read(sdk));
  }

  public set(sdk: BackendSDK, value: unknown): Promise<EgressSummary> {
    const snapshot = parseProfiles(value);
    return this.operations.run(async () => {
      try {
        await this.prepareDirectory(sdk);
        await this.write(sdk, snapshot);
      } catch {
        throw new Error("Failed to persist egress configuration");
      }
      return summary(snapshot);
    });
  }

  private async prepareDirectory(sdk: BackendSDK): Promise<void> {
    const directory = path.dirname(egressFilePath(sdk));
    await mkdir(directory, { recursive: true, mode: 0o700 });
    await chmod(directory, 0o700);
  }

  private async read(sdk: BackendSDK): Promise<EgressSummary> {
    try {
      const filename = egressFilePath(sdk);
      await chmod(filename, 0o600);
      return summary(
        parseProfiles(JSON.parse(await readFile(filename, "utf8")) as unknown),
      );
    } catch {
      throw new Error("Stored egress configuration is unavailable or invalid");
    }
  }

  private async write(
    sdk: BackendSDK,
    profiles: EgressProfiles,
  ): Promise<void> {
    const filename = egressFilePath(sdk);
    const temporary = `${filename}.${randomBytes(16).toString("hex")}.tmp`;
    try {
      const file = await open(temporary, "wx", 0o600);
      try {
        await file.writeFile(JSON.stringify(profiles));
      } finally {
        await file.close();
      }
      await rename(temporary, filename);
    } finally {
      await rm(temporary, { force: true });
    }
  }
}
