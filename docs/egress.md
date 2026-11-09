# Named egress profiles

[Back to README](../README.md)

This feature is available in current source builds; the published v0.3.0 ZIP predates it.

Add `X-Morpheus-Egress: office` to choose an upstream proxy by name. Names are caller-defined, case-sensitive labels, independent of Caido request colors and TLS profile names. Spaces and Unicode are supported; control characters and surrounding whitespace are rejected.

The native transport removes the selector before sending the request. Caido keeps it in the stored request, so Replay and Automate use the same route. This works for HTTP, HTTPS, and WebSocket handshakes through an enabled, matching Upstream Plugin rule.

## Configure through backend calls

Call `set-egress` through `caido-client plugin-call`, passing the complete map as **one JSON argument**:

```json
{
  "office": {"proxy": "http://username:password@proxy.example:8080"},
  "another route": {"proxy": "socks5h://username:password@proxy.example:1080"}
}
```

`set-egress` replaces the entire map atomically. Omitted names are removed; `{}` clears it. Supply credentials again when replacing an authenticated profile. Percent-encode reserved characters in usernames and passwords. URLs accept only `http://` and `socks5h://`, an optional port and credentials, and no path, query, or fragment. Default ports are 80 and 1080, respectively. The current limits are 256 names, 128 characters per name, and 4096 characters per proxy URL.

Read the redacted configuration with:

```sh
caido-client plugin-call impersonate-tls get-egress
```

Both calls return the usual plugin `Result`. For the CLI, check `.result.kind` and read `.result.value`. The returned map contains names and proxy endpoints, never usernames or passwords:

```json
{
  "office": {"proxy": "http://proxy.example:8080"},
  "another route": {"proxy": "socks5h://proxy.example:1080"}
}
```

The backend stores credentials in `private/egress.json` under its plugin data directory, with file mode `0600` and directory mode `0700`. Configuration survives Caido/plugin restarts. The plugin does not emit credentials to events, Activity, logs, or API responses. Invalid updates preserve the previous map; `set-egress` can replace a damaged file.

## Routing and isolation

- No selector: direct egress with the existing TLS/HTTP profile.
- One known name: HTTP CONNECT or SOCKS5 with destination DNS resolved by the proxy. Only the proxy endpoint itself is resolved locally.
- Unknown, empty, or duplicate selector: HTTP 400. An unavailable or invalid stored configuration returns HTTP 503.
- Unreachable or rejecting proxy: HTTP 502 with an egress error. There is no direct fallback.
- Shaping disabled: requests carrying the selector are rejected instead of bypassing the transport.

Each name has separate connections and TLS session caches, even if two names use the same proxy. Clients are also separated by destination, TLS profile, and HTTP/WebSocket mode. Changing a proxy creates a new client/cache for subsequent requests; already-running transfers continue on their original connection. Configuration updates take effect without restarting the helper.

The profile currently contains only `proxy`; a per-egress TLS profile override is reserved for a later change. The existing selected TLS profile applies today.

Docker tests use authenticated loopback proxies with different source addresses and a hostname resolvable only by the proxy. They cover HTTP, WebSockets, Replay, Automate, selector retention/removal, restart persistence, errors without direct fallback, and connection/TLS-session isolation. See [development and testing](development.md).
