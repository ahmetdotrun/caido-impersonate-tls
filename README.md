# Impersonate TLS for Caido

Impersonate TLS is a Caido plugin that applies browser-like TLS and HTTP transport profiles to selected traffic.

> [!IMPORTANT]
> This is an early development release with a Linux x86_64 transport only.

## How it works

The plugin uses Caido's `onUpstream` hook to route matching requests through a bundled, token-authenticated transport on `127.0.0.1`. The transport removes its private routing headers and connects to the target using the selected `tls-client` profile.

```text
Caido Proxy / Replay / Automate / workflow
                    │ matching Upstream Plugin rule
                    ▼
        Impersonate TLS backend plugin
                    │ authenticated loopback request
                    ▼
          bundled native transport
                    │ selected TLS + HTTP profile
                    ▼
                  target
```

The backend starts the transport on an ephemeral loopback port and passes a random 256-bit token through a one-time owner-only file. An ownership heartbeat stops orphaned processes after plugin unload. The transport authenticates requests before opening a target connection, strips every internal header, rejects CONNECT, verifies target certificates, and fails closed when unavailable.

Before execution, the backend verifies the bundled binary against its packaged SHA-256 checksum and atomically installs it in Caido's private plugin directory with owner-only permissions. Reloading the same version does not overwrite a still-running executable. There is no runtime downloader or updater.

This design does not create a Caido HTTP/SOCKS proxy, expose a general proxy endpoint, or require a separately managed process.

## Install and use

1. Download `plugin_package.zip` from the latest release.
2. Install the ZIP from Caido's Plugins page.
3. Open **Impersonate TLS**, confirm that the transport is running, and select the profile measured for your actual browser build. New installations default to **Chrome for Testing 157 (Linux, PSK)**, measured against 157.0.8103.38 with upstream [vercel-labs/agent-browser](https://github.com/vercel-labs/agent-browser) 0.41.2. Existing saved selections are preserved. Profile details distinguish measured builds from upstream snapshots; a matching major version alone does not establish parity.
4. In **Settings → Upstream Plugins**, add a rule for this plugin. Use `*` to include every domain or a narrower domain pattern for selective routing.
5. Send requests normally from Proxy, Replay, Automate, or workflows.
6. Check the **Activity** tab for routing state, profile, response status, protocol, duration, transport errors, and Chrome identity mismatches.

Activity is memory-only and limited to 250 entries. Paths, queries, headers, bodies, cookies, and internal tokens are never logged.

## Current scope

- All 83 named profiles from pinned `tls-client` v1.16.0, including Chrome, Brave, Firefox, Safari, Opera, OkHttp and application-specific snapshots, plus Linux Chrome for Testing 152 and 157 variants: 86 selections in total. Search the selector to find a profile. Legacy snapshots are available for reproducibility; their availability does not establish parity with a current browser.
- HTTPS negotiates HTTP/2 or HTTP/1.1 through ALPN; plain HTTP uses HTTP/1.1.
- Modern Chrome (110+) and Brave profiles permute ClientHello extension order on each new handshake. Older snapshots retain their captured order. Chromium WebSocket clients omit HTTP/2 application settings when negotiating HTTP/1.1.
- Certificate verification and fail-closed transport behavior.
- Original request headers and response content encodings preserved where possible.
- RFC 6455 WebSockets use the selected TLS profile with an HTTP/1.1 Upgrade handshake and a bidirectional tunnel.
- Uploads and responses stream with bounded relay memory. **Maximum upload (MiB)** defaults to `0` (no plugin size cap); a positive value rejects oversized requests with HTTP 413. Settings saved by earlier versions migrate automatically. Changes apply to new requests without restarting the transport. For chunked uploads, the limit is enforced as bytes arrive, so a rejected upload can have reached the origin partially.
- Active transfers have no fixed lifetime. Connections have a 30-second dial timeout, requests have a 60-second header wait after the upload is sent, and ordinary transfers have a five-minute inactivity timeout. Closing the Caido-to-relay connection or shutting down the transport cancels upstream work; propagation of a browser disconnect also depends on Caido. Bodyless HTTP requests also release their queue position on disconnect. Queued uploads and WebSocket handshakes retain the bounded queue timeout because monitoring them must not consume unread payload. WebSockets retain their open-ended lifetime.
- Clients are pooled by origin, profile, and HTTP/WebSocket mode so a stalled TLS handshake cannot block unrelated sites. The pool holds at most 256 clients, evicts only idle clients, and returns HTTP 503 if all are active. Response bodies and WebSockets keep their client lease until close.
- Settings reads/writes and transport start/stop operations execute in order. Settings are replaced atomically; damaged or unreadable settings remain on disk and block routing. **Prepare replacement settings** creates a disabled draft; review and save it to repair the file. A failed automatic startup is reported separately from a successful settings write.
- HTTP requests and WebSockets each have 128 active slots. Another 128 requests can wait for up to 10 seconds; overload returns HTTP 503 and is reported in Activity when the request has been authenticated and identified.
- Private per-request loopback connections.
- Informational responses, including `100 Continue` and `103 Early Hints`, are forwarded. `OPTIONS *` retains its server-wide target. Stopping or losing the helper marks pending activity as interrupted.
- Linux x86_64 only.

## Limitations

- Caido or the origin can impose additional timeouts, buffering, or size limits independently of this transport. In controlled checks on Caido 0.58.3, finite 100-second event streams completed through this relay but reached the browser only after completion. Short streaming probes also received no incremental data using Caido's built-in transport, with either its V1 or experimental V2 stack. Browser aborts did not close the controlled origin stream within ten seconds. Removing relay timeouts does **not** make browser SSE/streaming or cancellation propagation reliable through that Caido build. No direct-route workaround is applied.
- Each Caido-to-plugin connection handles one HTTP request or one WebSocket lifetime.
- Browser profiles are captured snapshots and may trail current release channels.
- The plugin preserves supplied HTTP headers; it does not rewrite the User-Agent or generate a browser-coherent header set, so headers must remain aligned with the selected profile.
- In isolated Caido 0.59.0 checks, Chrome 157 negotiated HTTP/1.1 with Caido using both network stacks and omitted the `Priority` header present on a direct HTTP/2 navigation. The destination TLS and HTTP/2 settings and remaining ordered headers matched the direct capture. Supplying the captured headers through Caido CONNECT preserved `Priority` and the complete header order. The relay cannot reconstruct a header the browser did not send.
- Identity warnings check browser family/version, platform, Chromium Client Hints and mobile identity when supplied. Missing headers or a lack of warnings do not prove browser parity. HTTP and WebSocket clients have separate TLS session caches; cross-mode session resumption is unverified.
- HTTP/3/QUIC and custom ClientHello or JA3/JA4_r import are not implemented.

Use this plugin only on systems you are authorized to test.

## Compatibility checks

Build and verify the plugin entirely in Docker:

```sh
docker build --target artifact --output type=local,dest=dist .
```

This includes Go race tests and a ZIP check that executes the packaged helper's `--version` and verifies its SHA-256, manifest, catalog and package versions. The profile registry is shared by TypeScript and Go in `transport/internal/relay/catalog.json`; every advertised profile is checked against a verified local TLS origin.

Run the independent browser-to-relay comparison with pinned upstream agent-browser **0.41.2** and Chrome for Testing **157.0.8103.38**:

```sh
docker build -f tests/browser/Dockerfile -t impersonate-tls-browser-test .
docker run --rm --network none --shm-size=1g --cap-drop ALL --security-opt no-new-privileges impersonate-tls-browser-test
```

Both downloads are SHA-256 pinned in `tests/browser/versions.json`. The test uses a generated local CA with certificate verification enabled and a loopback-only diagnostic origin. It compares ClientHello ciphers/extensions, signature algorithms, groups, key shares, ALPN, application settings, certificate compression, exact trust-anchor order, HTTP/2 settings/window and ordered request headers. It covers fresh/reused HTTP/2, resumed HTTP/2 for the PSK variant, and a separate cold WebSocket origin. This verifies the relay directly; it does not certify a separately configured Caido instance or claim complete browser indistinguishability. GitHub Actions runs it separately from packaging.

The release was also checked in a fresh, offline Caido 0.59.0 Docker instance: ZIP installation, automatic helper startup, all 86 selector entries, profile search and persistence after reload, private-header removal, completed Activity rows, HTTP uploads and verified HTTPS through CONNECT. Browser-to-Caido and relay-to-origin certificates were verified using disposable CAs. The browser-facing protocol difference above prevents a claim of full end-to-end browser parity.

Run `go test -race ./...` from `transport/` for local HTTP/1.1 and HTTP/2 origins using the production client configuration, certificate verification and rejection, slow-TLS origin isolation, bounded client leases, streaming, cancellation (including queued bodyless requests), large uploads, overload, WebSockets, cookies, redirects, and compressed responses. `pnpm test:backend` checks settings races and transport lifecycle ordering against the compiled backend and bundled executable; build the transport assets first. The Docker verification stage runs both suites, type checking, lint, and unused-code checks before packaging.

`pnpm test:browser` uses a running **disposable** agent-browser engagement image and the configured Caido proxy. Set `COMPAT_CONTAINER` to a container labelled `io.caido.compat.disposable=true`, with its dashboard published on `127.0.0.1`. Set `COMPAT_ORIGIN_HOST` to a local IP reachable from Caido, and `COMPAT_EXPECTED_SOURCE` to Caido's source IP for that connection. Ensure the disposable container resolves the private proxy hostname and trusts Caido's CA. The test serves only generated data on a random URL, verifies the proxy source address, exercises CLI/dashboard session continuity, cookies, redirects, compression, ranges, 70 MiB uploads/downloads, WebSockets, cancellation, and 100-second streams, then restarts only the labelled container to check persistent profile recovery. It closes its test sessions and fixture listener; remove the disposable container afterward. Caido retains the generated traffic in its selected project.

Both browser entry points also navigate to `https://example.com/` and check for the expected content in a secure context. Override `COMPAT_HTTPS_URL` and `COMPAT_HTTPS_TEXT` together to use another trusted HTTPS page. This smoke check covers browser-to-Caido certificate trust; the local Go tests cover origin certificate verification and HTTP/2 negotiation. It is not a TLS fingerprint parity measurement.

The browser check deliberately fails if Caido buffers event streams or fails to propagate cancellation; a passing relay unit suite does not substitute for this end-to-end result. Set `COMPAT_SKIP_LARGE_TRANSFERS=1` for a targeted rerun without repeating the 70 MiB transfers; this is not a full-suite pass.

## Credits and license

The design was informed by TLSMask, PortSwigger's bypass-bot-detection, WafRift, and tls-client's Chrome 152 profile proposal.

The project is MIT licensed. Transport build metadata and the license texts supplied by linked dependencies are bundled under `assets/licenses/`; dependency authors retain their respective rights.
