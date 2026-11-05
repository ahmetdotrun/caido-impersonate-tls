# Transport reference

[Back to README](../README.md)

## Request flow

```text
Caido matching Upstream Plugin rule
  → plugin backend
  → private loopback relay
  → target using the selected TLS and HTTP profile
```

The backend uses Caido's `onUpstream` hook to route matching requests to a bundled Go helper. Each private connection handles one HTTP request or one WebSocket lifetime. This applies to matching traffic from Proxy, Replay, Automate, and workflows.

The helper binds to an ephemeral port on `127.0.0.1` and authenticates requests with a random 256-bit token. It removes private routing headers before forwarding and verifies target certificates. If shaping is enabled and the helper is unavailable, matching requests fail rather than falling back to an unshaped connection.

Before execution, the backend verifies the binary against its packaged SHA-256 checksum and installs it atomically with owner-only permissions. A one-time token file and an ownership heartbeat manage the process. Reloading the same version does not overwrite an executing binary. There is no runtime downloader or updater. See the [security policy](../SECURITY.md) for the runtime boundary.

## Settings

| Setting | Default | Effect |
| --- | --- | --- |
| Enable shaping | On | Apply the selected profile to matching Upstream Plugin rules. |
| Start with plugin | On | Launch the helper when Caido loads the plugin. |
| Transport profile | Chrome for Testing 157 (Linux, PSK) | Select the destination TLS and HTTP identity. Saved selections survive updates. |
| Maximum upload (MiB) | `0` | `0` means no plugin size cap; a positive value rejects oversized requests with HTTP 413. |

Upload-limit changes apply to new requests without restarting. Chunked uploads are checked as bytes arrive, so part of a rejected upload may already have reached the origin. Older settings without an upload limit migrate to `0`.

Settings reads/writes and start/stop operations execute in order. Settings files are replaced atomically. A damaged or unreadable file stays on disk and blocks routing. Use **Prepare replacement settings** to create a disabled draft, then review and save it to repair the file. A failed automatic startup is reported separately from a successful settings write.

## Protocol behavior

- HTTPS negotiates HTTP/2 or HTTP/1.1 through ALPN; plain HTTP uses HTTP/1.1.
- Request headers and response content encodings are preserved where possible.
- RFC 6455 WebSockets use the selected TLS profile with an HTTP/1.1 Upgrade handshake and a bidirectional tunnel.
- Informational responses, including `100 Continue` and `103 Early Hints`, are forwarded. `OPTIONS *` keeps its server-wide target.
- Chrome 110+ and Brave profiles randomize ClientHello extension order for each new handshake. Older snapshots retain their captured order. HTTP/1.1 Upgrade clients omit HTTP/2 application settings.
- HTTP/3/QUIC and custom ClientHello or JA3/JA4 imports are not implemented.

The [compatibility guide](compatibility.md) explains measured profiles and the behavior Caido can change before a request reaches the relay.

## Timeouts and capacity

Uploads and responses stream with bounded relay memory. Active transfers have no fixed total lifetime.

| Limit | Value |
| --- | --- |
| Connection dial | 30 seconds |
| Response-header wait after upload | 60 seconds |
| Ordinary-transfer inactivity | 5 minutes |
| Active HTTP requests | 128 |
| Active WebSockets | 128 |
| Waiting requests | 128, for up to 10 seconds |
| Pooled clients | 256 |

Queue or client-pool exhaustion returns HTTP 503. Overload is reported in Activity when the request can be authenticated and identified. WebSocket lifetimes remain open-ended.

Clients are pooled by origin, profile, and HTTP/WebSocket mode. A stalled TLS handshake cannot hold up unrelated origins. Only idle clients can be evicted; response bodies and WebSockets retain their client lease until close.

Closing a Caido-to-relay connection or stopping the helper cancels its upstream work. Queued bodyless HTTP requests can also release their position on disconnect. Queued uploads and WebSocket handshakes retain the bounded queue timeout because disconnect monitoring must not consume unread payload. Browser-disconnect propagation depends on Caido.

## Activity and privacy

Activity is kept in memory, capped at 250 entries. It shows target host, method, profile, routing state, response status, protocol, duration, and identity warnings. Stopping or losing the helper marks pending entries as interrupted.

Paths, queries, headers, bodies, cookies, and transport tokens are not recorded in Activity.

## Credits and licenses

The design was informed by TLSMask, PortSwigger's bypass-bot-detection, WafRift, and tls-client's Chrome 152 profile proposal.

The project is [MIT licensed](../LICENSE). Transport build metadata and license texts supplied by linked dependencies are included in the plugin package under `assets/licenses/`; dependency authors retain their rights.
