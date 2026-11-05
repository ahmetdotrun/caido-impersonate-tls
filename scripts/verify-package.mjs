import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { chmod, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import { crc32, inflateRawSync } from "node:zlib";

const exec = promisify(execFile);
const root = new URL("../", import.meta.url);
const json = async (file) =>
  JSON.parse(await readFile(new URL(file, root), "utf8"));
const catalog = await json("transport/internal/relay/catalog.json");
for (const file of [
  "package.json",
  "packages/backend/package.json",
  "packages/frontend/package.json",
  "packages/shared/package.json",
]) {
  assert.equal(
    (await json(file)).version,
    catalog.version,
    `Version mismatch in ${file}`,
  );
}
const goMod = await readFile(new URL("transport/go.mod", root), "utf8");
assert(
  goMod.includes(
    `github.com/bogdanfinn/tls-client v${catalog.tlsClientVersion}`,
  ),
  "Catalog dependency version is stale",
);

// Read central-directory sizes, including archives that use data descriptors.
const zip = await readFile(
  new URL(process.argv[2] ?? "dist/plugin_package.zip", root),
);
let end = -1;
for (let i = zip.length - 22; i >= Math.max(0, zip.length - 65557); i--) {
  if (
    zip.readUInt32LE(i) === 0x06054b50 &&
    i + 22 + zip.readUInt16LE(i + 20) === zip.length
  ) {
    end = i;
    break;
  }
}
assert(end >= 0, "Invalid ZIP directory");
const entries = new Map();
let offset = zip.readUInt32LE(end + 16);
for (let i = 0; i < zip.readUInt16LE(end + 10); i++) {
  assert.equal(zip.readUInt32LE(offset), 0x02014b50);
  const method = zip.readUInt16LE(offset + 10);
  const size = zip.readUInt32LE(offset + 20);
  const rawSize = zip.readUInt32LE(offset + 24);
  const nameLength = zip.readUInt16LE(offset + 28);
  const name = zip.subarray(offset + 46, offset + 46 + nameLength).toString();
  const local = zip.readUInt32LE(offset + 42);
  assert.equal(zip.readUInt32LE(local), 0x04034b50);
  const start =
    local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
  assert(
    start + size <= zip.length && rawSize <= 128 * 1024 * 1024,
    "Invalid ZIP entry size",
  );
  assert(method === 0 || method === 8, "Unsupported ZIP compression");
  assert(!entries.has(name), `Duplicate ZIP entry ${name}`);
  const compressed = zip.subarray(start, start + size);
  const data =
    method === 8
      ? inflateRawSync(compressed, { maxOutputLength: 128 * 1024 * 1024 })
      : compressed;
  assert.equal(data.length, rawSize);
  assert.equal(
    crc32(data),
    zip.readUInt32LE(offset + 16),
    `ZIP CRC mismatch: ${name}`,
  );
  entries.set(name, data);
  offset +=
    46 +
    nameLength +
    zip.readUInt16LE(offset + 30) +
    zip.readUInt16LE(offset + 32);
}
const manifest = JSON.parse(entries.get("manifest.json"));
assert.equal(
  manifest.version,
  catalog.version,
  "Packaged manifest version is stale",
);
const prefix = "impersonate-tls-backend/assets/";
const relative = "linux-amd64/caido-impersonate-transport";
const binary = entries.get(prefix + relative);
assert(binary, "Transport executable missing from package");
const checksums = entries.get(prefix + "checksums.sha256")?.toString();
const checksum = checksums
  ?.split("\n")
  .find((line) => line.endsWith(`  ${relative}`))
  ?.split(/\s+/)[0];
assert.equal(
  createHash("sha256").update(binary).digest("hex"),
  checksum,
  "Packaged transport checksum mismatch",
);
const directory = await mkdtemp(path.join(tmpdir(), "impersonate-package-"));
try {
  const executable = path.join(directory, "transport");
  await writeFile(executable, binary, { mode: 0o700 });
  await chmod(executable, 0o700);
  const { stdout } = await exec(executable, ["--version"], { timeout: 5000 });
  assert.equal(
    stdout.trim(),
    catalog.version,
    "Packaged transport version is stale",
  );
} finally {
  await rm(directory, { recursive: true, force: true });
}
console.log(
  `Verified plugin ${catalog.version}: manifest, executable, catalog and SHA-256 agree`,
);
