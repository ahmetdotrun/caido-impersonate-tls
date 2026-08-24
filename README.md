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

Before execution, the backend verifies the bundled binary against its packaged SHA-256 checksum and copies it to Caido's private plugin directory with owner-only permissions. There is no runtime downloader or updater.

This design does not create a Caido HTTP/SOCKS proxy, expose a general proxy endpoint, or require a separately managed process.

## Install and use

1. Download `plugin_package.zip` from the latest release.
2. Install the ZIP from Caido's Plugins page.
3. Open **Impersonate TLS** and confirm that the transport is running.
4. In **Settings → Upstream Plugins**, add a rule for this plugin. Use `*` to include every domain or a narrower domain pattern for selective routing.
5. Send requests normally from Proxy, Replay, Automate, or workflows.
6. Check the **Activity** tab for routing state, profile, response status, protocol, duration, or transport errors.

Activity is memory-only and limited to 250 entries. Paths, queries, headers, bodies, cookies, and internal tokens are never logged.

## Current scope

- Pinned `tls-client` v1.15.1 profiles for Chrome 146/144, Firefox 148/147, Safari iOS 26.0/18.5, and OkHttp 4.10 on Android 13.
- HTTPS negotiates HTTP/2 or HTTP/1.1 through ALPN; plain HTTP uses HTTP/1.1.
- Chrome profiles permute ClientHello extension order on each new handshake, matching modern Chromium behaviour while keeping their JA4 and HTTP/2 identity stable.
- Certificate verification and fail-closed transport behavior.
- Original request headers and response content encodings preserved where possible.
- Private per-request loopback connections.
- Linux x86_64 only.

## Limitations

- Request bodies are buffered with a 64 MiB limit.
- Each Caido-to-plugin connection handles one request.
- Profile versions follow the pinned transport library and may trail current browser release channels.
- The plugin preserves supplied HTTP headers; it does not rewrite the User-Agent or generate a browser-coherent header set, so headers must remain aligned with the selected profile.
- HTTP/3/QUIC, WebSockets, and custom ClientHello or JA3/JA4_r import are not implemented.

Use this plugin only on systems you are authorized to test.

## Credits and license

The design was informed by TLSMask, PortSwigger's bypass-bot-detection, and WafRift.

The project is MIT licensed. Transport build metadata and the license texts supplied by linked dependencies are bundled under `assets/licenses/`; dependency authors retain their respective rights.
