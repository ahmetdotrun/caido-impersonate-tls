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
3. Open **Impersonate TLS**, confirm that the transport is running, and select the profile measured for your actual browser build. The default **Chrome 152** profile and **Chrome for Testing 152 (Linux)** have different ClientHello extensions despite sharing a major version. The latter matches the Linux Chrome for Testing 152.0.7977.54 and 152.0.7977.82 builds tested with agent-browser; compare the destination-observed TLS and HTTP/2 fingerprints after a browser or profile change.
4. In **Settings → Upstream Plugins**, add a rule for this plugin. Use `*` to include every domain or a narrower domain pattern for selective routing.
5. Send requests normally from Proxy, Replay, Automate, or workflows.
6. Check the **Activity** tab for routing state, profile, response status, protocol, duration, transport errors, and Chrome identity mismatches.

Activity is memory-only and limited to 250 entries. Paths, queries, headers, bodies, cookies, and internal tokens are never logged.

## Current scope

- Local Chrome 152 and Linux Chrome for Testing 152 profiles plus pinned `tls-client` v1.15.1 profiles for Chrome 146/144, Firefox 148/147, Safari iOS 26.0/18.5, and OkHttp 4.10 on Android 13.
- HTTPS negotiates HTTP/2 or HTTP/1.1 through ALPN; plain HTTP uses HTTP/1.1.
- Chrome profiles permute ClientHello extension order on each new handshake, matching modern Chromium behaviour while keeping their JA4 and HTTP/2 identity stable.
- Certificate verification and fail-closed transport behavior.
- Original request headers and response content encodings preserved where possible.
- RFC 6455 WebSockets use the selected TLS profile with an HTTP/1.1 Upgrade handshake and a bidirectional tunnel.
- Uploads and responses stream with bounded relay memory. **Maximum upload (MiB)** defaults to `0` (no plugin size cap); a positive value rejects oversized requests with HTTP 413. Settings saved by earlier versions migrate automatically. Changes apply to new requests without restarting the transport. For chunked uploads, the limit is enforced as bytes arrive, so a rejected upload can have reached the origin partially.
- Active transfers have no fixed lifetime. Connections have a 30-second dial timeout, requests have a 60-second header wait after the upload is sent, and ordinary transfers have a five-minute inactivity timeout. Closing the Caido-to-relay connection or shutting down the transport cancels upstream work; propagation of a browser disconnect also depends on Caido. WebSockets retain their open-ended lifetime.
- HTTP requests and WebSockets each have 128 active slots. Another 128 requests can wait for up to 10 seconds; overload returns HTTP 503 and is reported in Activity when the request has been authenticated and identified.
- Private per-request loopback connections.
- Linux x86_64 only.

## Limitations

- Caido or the origin can impose additional timeouts, buffering, or size limits independently of this transport. In controlled checks on Caido 0.58.3, finite 100-second event streams completed through this relay but reached the browser only after completion. Short streaming probes also received no incremental data using Caido's built-in transport, with either its V1 or experimental V2 stack. Browser aborts did not close the controlled origin stream within ten seconds. Removing relay timeouts does **not** make browser SSE/streaming or cancellation propagation reliable through that Caido build. No direct-route workaround is applied.
- Each Caido-to-plugin connection handles one HTTP request or one WebSocket lifetime.
- Browser profiles are captured snapshots and may trail current release channels.
- The plugin preserves supplied HTTP headers; it does not rewrite the User-Agent or generate a browser-coherent header set, so headers must remain aligned with the selected profile.
- HTTP/3/QUIC and custom ClientHello or JA3/JA4_r import are not implemented.

Use this plugin only on systems you are authorized to test.

## Compatibility checks

Run `go test -race ./...` from `transport/` for local HTTP/1.1 and HTTP/2 origins, certificate verification, streaming, cancellation, large uploads, overload, WebSockets, cookies, redirects, and compressed responses.

`pnpm test:browser` uses a running **disposable** agent-browser engagement image and the configured Caido proxy. Set `COMPAT_CONTAINER` to a container labelled `io.caido.compat.disposable=true`, with its dashboard published on `127.0.0.1`. Set `COMPAT_ORIGIN_HOST` to a local IP reachable from Caido, and `COMPAT_EXPECTED_SOURCE` to Caido's source IP for that connection. Ensure the disposable container resolves the private proxy hostname and trusts Caido's CA. The test serves only generated data on a random URL, verifies the proxy source address, exercises CLI/dashboard session continuity, cookies, redirects, compression, ranges, 70 MiB uploads/downloads, WebSockets, cancellation, and 100-second streams, then restarts only the labelled container to check persistent profile recovery. It closes its test sessions and fixture listener; remove the disposable container afterward. Caido retains the generated traffic in its selected project.

The browser check deliberately fails if Caido buffers event streams or fails to propagate cancellation; a passing relay unit suite does not substitute for this end-to-end result. Set `COMPAT_SKIP_LARGE_TRANSFERS=1` for a targeted rerun without repeating the 70 MiB transfers; this is not a full-suite pass.

## Credits and license

The design was informed by TLSMask, PortSwigger's bypass-bot-detection, WafRift, and tls-client's Chrome 152 profile proposal.

The project is MIT licensed. Transport build metadata and the license texts supplied by linked dependencies are bundled under `assets/licenses/`; dependency authors retain their respective rights.
