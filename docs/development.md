# Development and testing

[Back to README](../README.md)

Use Docker or a VM for dependency installation, builds, and test execution. Run the Docker commands below from the repository root.

## Build the plugin

```sh
docker build --target artifact --output type=local,dest=dist .
```

The output is `dist/plugin_package.zip`. The build runs:

- Go tests and race tests against local HTTP/1.1 and HTTP/2 origins.
- A verified TLS request for every advertised profile in negotiated and forced HTTP/1.1 modes.
- Backend tests for settings, Activity, and the bundled helper's lifecycle.
- Type checking, lint, and unused-code checks.
- ZIP CRC checks, executable SHA-256 and version checks, and agreement between the manifest, catalog, dependency pin, and package versions.

Dependencies are fetched and checked before the test/build steps run with networking disabled. Lifecycle scripts are disabled during dependency installation. The helper and its license files are generated inside Docker; a separate host-side build is not needed.

## Compare the browser and relay

```sh
docker build -f tests/browser/Dockerfile -t impersonate-tls-browser-test .
docker run --rm --network none --shm-size=1g --cap-drop ALL --security-opt no-new-privileges impersonate-tls-browser-test
```

The image downloads SHA-256-pinned official agent-browser and Chrome for Testing binaries. At runtime it uses a generated local CA and a loopback diagnostic origin, with certificate verification enabled and no external network access.

It runs three comparisons for `chrome_157_cft` and five for `chrome_157_cft_PSK`: fresh/reused HTTP/2, accepted TLS resumption for the PSK variant, and a separate cold WebSocket origin. The [compatibility guide](compatibility.md) lists the measured fields and scope.

This fixture checks the relay directly. It does not configure or certify a Caido instance. The [GitHub Actions workflow](../.github/workflows/build.yml) runs packaging and browser comparisons as separate jobs.

To check an extracted packaged helper inside the browser test container, set `TRANSPORT_BIN` to its executable path before running `tests/browser/run.sh`.

## Caido browser regression harness

[scripts/browser-compat.mjs](../scripts/browser-compat.mjs) exercises a separately prepared, disposable agent-browser engagement runtime through Caido. It needs the pinned upstream CLI, a dashboard, persistent browser storage, and trust for Caido's CA. The browser comparison image above does not provide that engagement runtime.

Run the harness as `pnpm test:browser` inside a VM running Docker, or a dedicated orchestration container sharing the Docker host's network namespace. It needs Docker CLI access, access to the published dashboard at `127.0.0.1`, and an IP reachable by Caido. Use a runtime and Caido project owned by this test; the harness restarts the selected runtime and Caido retains generated traffic in its project.

| Variable | Required value |
| --- | --- |
| `COMPAT_CONTAINER` | Disposable runtime labelled `io.caido.compat.disposable=true`, running agent-browser at the version pinned in `tests/browser/versions.json`. |
| `COMPAT_ORIGIN_HOST` | IP on which the harness can listen for requests from Caido. |
| `COMPAT_EXPECTED_SOURCE` | Caido's source IP as seen by the fixture. |

Publish the runtime dashboard on host `127.0.0.1` at container port `4948`. The browser must resolve the configured proxy hostname and trust Caido's CA.

The harness verifies the proxy source address and exercises CLI/dashboard session continuity, cookies, redirects, compression, ranges, 70 MiB uploads/downloads, WebSockets, cancellation, 100-second streams, and profile recovery after restarting the labelled runtime. It closes its sessions and fixture listener; remove the disposable runtime afterward.

Both the CLI and dashboard paths visit `https://example.com/` and check its content in a secure context. Set `COMPAT_HTTPS_URL` and `COMPAT_HTTPS_TEXT` together to use another trusted HTTPS page. This checks browser-to-Caido certificate trust, not destination fingerprint parity.

The harness deliberately fails when Caido buffers streams or does not propagate cancellation. Setting `COMPAT_SKIP_LARGE_TRANSFERS=1` skips the large transfers for a targeted rerun; it does not constitute a full-suite pass. See [observed Caido behavior](compatibility.md#observed-caido-behavior).

## Source layout

| Path | Purpose |
| --- | --- |
| `packages/backend/` | Caido hooks, settings, helper lifecycle, and Activity. |
| `packages/frontend/` | Vue plugin UI. |
| `packages/shared/` | Shared types, profile catalog access, and Activity merging. |
| `transport/` | Go relay, native entry point, diagnostic origin, and transport tests. |
| `transport/internal/relay/catalog.json` | Shared profile registry and plugin/dependency version metadata. |
| `tests/backend/` | Backend regression tests. |
| `tests/browser/` | Browser test image, installer, and pinned versions. |
| `scripts/` | Compatibility harnesses and package verification. |

`dist/`, package build directories, `.cache/backend-tests/`, and generated transport assets are build output. Keep them out of commits. The published release ZIP and its checksum are distributed as GitHub release assets.
