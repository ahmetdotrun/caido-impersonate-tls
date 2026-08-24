import { type ChildProcess, spawn } from "child_process";
import { createHash, randomBytes } from "crypto";
import { chmod, mkdir, open, readFile, rm, writeFile } from "fs/promises";
import os from "os";
import path from "path";

import type { Result, TransportStatus } from "shared";

import type { BackendSDK } from "./types";

const START_TIMEOUT_MS = 10_000;
const STOP_TIMEOUT_MS = 5_000;
const HEARTBEAT_INTERVAL_MS = 5_000;
const TRANSPORT_VERSION = "0.1.4";
const BINARY_NAME = "caido-impersonate-transport";

type ReadyEvent = {
  event: "ready";
  port: number;
  version: string;
};

export type TransportRequestEvent =
  | {
      event: "request";
      id: string;
      outcome: "succeeded";
      statusCode: number;
      protocol: string;
      durationMs: number;
    }
  | {
      event: "request";
      id: string;
      outcome: "failed";
      error: string;
      durationMs: number;
    };

type RuntimeFiles = {
  tokenPath: string;
  ownerPath: string;
};

export class TransportService {
  private child: ChildProcess | undefined;
  private startInFlight: Promise<Result<TransportStatus>> | undefined;
  private heartbeatTimer: Timeout | undefined;
  private runtimeFiles: RuntimeFiles | undefined;
  private token: string | undefined;
  private status: TransportStatus;

  public constructor(
    private readonly sdk: BackendSDK,
    private readonly onRequestEvent: (event: TransportRequestEvent) => void,
  ) {
    this.status = {
      state: "idle",
      version: TRANSPORT_VERSION,
      platform: this.platformKey() ?? "unsupported",
    };
  }

  public getStatus(): TransportStatus {
    return { ...this.status };
  }

  public getConnectionDetails(): { port: number; token: string } | undefined {
    if (
      this.status.state !== "running" ||
      this.status.port === undefined ||
      this.token === undefined
    ) {
      return undefined;
    }

    return { port: this.status.port, token: this.token };
  }

  public async start(): Promise<Result<TransportStatus>> {
    if (this.startInFlight !== undefined) {
      return this.startInFlight;
    }

    const operation = this.startInternal();
    this.startInFlight = operation;
    try {
      return await operation;
    } finally {
      if (this.startInFlight === operation) {
        this.startInFlight = undefined;
      }
    }
  }

  private async startInternal(): Promise<Result<TransportStatus>> {
    if (this.status.state === "running") {
      return { kind: "Ok", value: this.getStatus() };
    }

    const platform = this.platformKey();
    if (platform === undefined) {
      return this.fail(
        "unsupported",
        `No bundled transport for ${os.platform()}/${os.arch()}`,
      );
    }

    this.setStatus({ state: "starting", platform, error: undefined });

    try {
      const binaryPath = await this.installBinary(platform);
      const token = randomBytes(32).toString("hex");
      const runtimeFiles = await this.prepareRuntimeFiles(token);
      this.runtimeFiles = runtimeFiles;
      const child = spawn(
        binaryPath,
        [
          "--listen",
          "127.0.0.1:0",
          "--token-file",
          runtimeFiles.tokenPath,
          "--owner-file",
          runtimeFiles.ownerPath,
        ],
        {
          stdio: ["ignore", "pipe", "pipe"],
        },
      );

      this.child = child;
      this.token = token;
      this.startHeartbeat(runtimeFiles.ownerPath);

      const ready = await this.waitUntilReady(child);
      if (ready.version !== TRANSPORT_VERSION) {
        throw new Error(
          `Transport version mismatch: expected ${TRANSPORT_VERSION}, received ${ready.version}`,
        );
      }
      if (this.child !== child) {
        throw new Error("Transport exited immediately after readiness");
      }
      await rm(runtimeFiles.tokenPath, { force: true });
      this.setStatus({
        state: "running",
        platform,
        port: ready.port,
        version: ready.version,
        error: undefined,
      });

      return { kind: "Ok", value: this.getStatus() };
    } catch (error) {
      await this.terminateChild();
      return this.fail("error", String(error));
    }
  }

  public async stop(): Promise<Result<TransportStatus>> {
    if (this.startInFlight !== undefined) {
      await this.startInFlight;
    }

    if (this.child === undefined) {
      this.token = undefined;
      await this.cleanupRuntimeFiles();
      this.setStatus({ state: "idle", port: undefined, error: undefined });
      return { kind: "Ok", value: this.getStatus() };
    }

    this.setStatus({ state: "stopping", error: undefined });
    await this.terminateChild();
    this.token = undefined;
    this.setStatus({ state: "idle", port: undefined, error: undefined });
    return { kind: "Ok", value: this.getStatus() };
  }

  private async installBinary(platform: string): Promise<string> {
    const extension = os.platform() === "win32" ? ".exe" : "";
    const filename = `${BINARY_NAME}${extension}`;
    const relativePath = `${platform}/${filename}`;
    const assetPath = path.join(this.sdk.meta.assetsPath(), relativePath);

    const binary = await readFile(assetPath);
    await this.verifyChecksum(binary, relativePath);

    const installDirectory = path.join(
      this.sdk.meta.path(),
      "bin",
      TRANSPORT_VERSION,
      platform,
    );
    const installedPath = path.join(installDirectory, filename);

    await mkdir(installDirectory, { recursive: true });
    await writeFile(installedPath, binary, { mode: 0o700 });
    if (os.platform() !== "win32") {
      await chmod(installedPath, 0o700);
    }

    return installedPath;
  }

  private async prepareRuntimeFiles(token: string): Promise<RuntimeFiles> {
    const runtimeDirectory = path.join(this.sdk.meta.path(), "runtime");
    const nonce = randomBytes(16).toString("hex");
    const tokenPath = path.join(runtimeDirectory, `${nonce}.token`);
    const ownerPath = path.join(runtimeDirectory, `${nonce}.owner`);

    await mkdir(runtimeDirectory, { recursive: true, mode: 0o700 });
    if (os.platform() !== "win32") {
      await chmod(runtimeDirectory, 0o700);
    }

    try {
      await this.writeExclusive(tokenPath, `${token}\n`);
      await this.writeExclusive(ownerPath, `${Date.now()}\n`);
    } catch (error) {
      await Promise.all([
        rm(tokenPath, { force: true }),
        rm(ownerPath, { force: true }),
      ]);
      throw error;
    }

    return { tokenPath, ownerPath };
  }

  private async writeExclusive(filePath: string, data: string): Promise<void> {
    const file = await open(filePath, "wx", 0o600);
    try {
      await file.writeFile(data);
    } finally {
      await file.close();
    }
  }

  private async verifyChecksum(
    binary: QuickJS.ArrayBufferView,
    relativePath: string,
  ): Promise<void> {
    const checksumPath = path.join(
      this.sdk.meta.assetsPath(),
      "checksums.sha256",
    );
    const checksumFile = await readFile(checksumPath, "utf8");
    const line = checksumFile
      .split("\n")
      .find((candidate) => candidate.endsWith(`  ${relativePath}`));

    if (line === undefined) {
      throw new Error(`Missing checksum for ${relativePath}`);
    }

    const expected = line.split(/\s+/u)[0];
    if (expected === undefined || /^[a-f0-9]{64}$/u.test(expected) === false) {
      throw new Error(`Invalid checksum entry for ${relativePath}`);
    }

    const actual = createHash("sha256").update(binary).digest("hex");
    if (actual !== expected) {
      throw new Error(`Checksum mismatch for ${relativePath}`);
    }
  }

  private waitUntilReady(child: ChildProcess): Promise<ReadyEvent> {
    return new Promise((resolve, reject) => {
      let stdout = "";
      let settled = false;

      const settle = (callback: () => void): void => {
        if (settled === false) {
          settled = true;
          clearTimeout(timeout);
          callback();
        }
      };

      const timeout = setTimeout(() => {
        settle(() => reject(new Error("Transport startup timed out")));
      }, START_TIMEOUT_MS);

      child.stdout?.on("data", (data) => {
        stdout += String(data);
        const lines = stdout.split("\n");
        stdout = lines.pop() ?? "";

        for (const line of lines) {
          const event = this.parseReadyEvent(line);
          if (event !== undefined) {
            settle(() => resolve(event));
            continue;
          }

          const requestEvent = this.parseRequestEvent(line);
          if (requestEvent !== undefined) {
            this.onRequestEvent(requestEvent);
          } else if (line.trim() !== "") {
            this.sdk.console.log(`[Impersonate TLS transport] ${line}`);
          }
        }
      });

      child.stderr?.on("data", (data) => {
        const message = String(data).trim();
        if (message !== "") {
          this.sdk.console.error(`[Impersonate TLS transport] ${message}`);
        }
      });

      child.once("error", (error) => {
        settle(() => reject(error));
      });

      child.once("close", (code, signal) => {
        const wasStarting = this.status.state === "starting";
        this.child = undefined;
        this.token = undefined;
        void this.cleanupRuntimeFiles();

        const exitReason =
          code === null ? `signal ${String(signal)}` : `code ${code}`;

        if (wasStarting) {
          settle(() =>
            reject(
              new Error(`Transport exited during startup with ${exitReason}`),
            ),
          );
        } else if (this.status.state !== "stopping") {
          this.setStatus({
            state: "error",
            port: undefined,
            error: `Transport exited with ${exitReason}`,
          });
        }
      });
    });
  }

  private parseReadyEvent(line: string): ReadyEvent | undefined {
    try {
      const candidate = JSON.parse(line) as Partial<ReadyEvent>;
      if (
        candidate.event === "ready" &&
        typeof candidate.port === "number" &&
        Number.isInteger(candidate.port) &&
        candidate.port > 0 &&
        candidate.port <= 65_535 &&
        typeof candidate.version === "string"
      ) {
        return candidate as ReadyEvent;
      }
    } catch {
      return undefined;
    }

    return undefined;
  }

  private parseRequestEvent(line: string): TransportRequestEvent | undefined {
    try {
      const candidate = JSON.parse(line) as Partial<TransportRequestEvent>;
      if (
        candidate.event !== "request" ||
        typeof candidate.id !== "string" ||
        /^[a-z0-9]+-[a-z0-9]+$/u.test(candidate.id) === false ||
        (candidate.outcome !== "succeeded" && candidate.outcome !== "failed") ||
        typeof candidate.durationMs !== "number" ||
        Number.isInteger(candidate.durationMs) === false ||
        candidate.durationMs < 0
      ) {
        return undefined;
      }

      if (
        candidate.outcome === "succeeded" &&
        typeof candidate.statusCode === "number" &&
        Number.isInteger(candidate.statusCode) &&
        candidate.statusCode >= 100 &&
        candidate.statusCode <= 999 &&
        typeof candidate.protocol === "string" &&
        candidate.protocol.length > 0 &&
        candidate.protocol.length <= 32
      ) {
        return candidate as TransportRequestEvent;
      }

      if (
        candidate.outcome === "failed" &&
        typeof candidate.error === "string" &&
        candidate.error.length > 0
      ) {
        return candidate as TransportRequestEvent;
      }
    } catch {
      return undefined;
    }

    return undefined;
  }

  private async terminateChild(): Promise<void> {
    const child = this.child;
    if (child === undefined) {
      await this.cleanupRuntimeFiles();
      return;
    }

    await new Promise<void>((resolve) => {
      let settled = false;
      const finish = (): void => {
        if (settled === false) {
          settled = true;
          clearTimeout(timeout);
          resolve();
        }
      };

      const timeout = setTimeout(() => {
        child.kill("SIGKILL");
        finish();
      }, STOP_TIMEOUT_MS);

      child.once("close", finish);
      child.kill("SIGTERM");
    });

    this.child = undefined;
    await this.cleanupRuntimeFiles();
  }

  private startHeartbeat(ownerPath: string): void {
    this.stopHeartbeat();
    this.heartbeatTimer = setInterval(() => {
      void writeFile(ownerPath, `${Date.now()}\n`, { mode: 0o600 }).catch(
        (error: unknown) => {
          this.sdk.console.error(
            `[Impersonate TLS] Failed to refresh transport ownership: ${String(error)}`,
          );
        },
      );
    }, HEARTBEAT_INTERVAL_MS);
  }

  private stopHeartbeat(): void {
    if (this.heartbeatTimer !== undefined) {
      clearInterval(this.heartbeatTimer);
      this.heartbeatTimer = undefined;
    }
  }

  private async cleanupRuntimeFiles(): Promise<void> {
    this.stopHeartbeat();
    const runtimeFiles = this.runtimeFiles;
    this.runtimeFiles = undefined;
    if (runtimeFiles === undefined) {
      return;
    }

    await Promise.all([
      rm(runtimeFiles.tokenPath, { force: true }),
      rm(runtimeFiles.ownerPath, { force: true }),
    ]);
  }

  private platformKey(): string | undefined {
    const key = `${os.platform()}/${os.arch()}`;
    return new Map<string, string>([["linux/x64", "linux-amd64"]]).get(key);
  }

  private setStatus(update: Partial<TransportStatus>): void {
    this.status = { ...this.status, ...update };
    this.sdk.api.send("transport:status", this.getStatus());
  }

  private fail(
    state: "error" | "unsupported",
    error: string,
  ): Result<TransportStatus> {
    this.setStatus({ state, port: undefined, error });
    return { kind: "Error", error };
  }
}
