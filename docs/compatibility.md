# Profiles and compatibility

[Back to README](../README.md)

## Choose a profile

Use the selector's search to find the browser, version, and platform that match your traffic. New installations default to **Chrome for Testing 157 (Linux, PSK)**; updating the plugin preserves existing saved selections.

The catalog contains all 83 named profiles from pinned `tls-client` 1.16.0 and three local Chrome for Testing variants: 86 selections in total. Some upstream names are aliases. These are snapshots, including older builds, rather than a list of current browser releases.

Profile details distinguish measured builds from upstream snapshots. A matching major version alone is not enough to establish identical browser behavior. The complete registry is in [catalog.json](../transport/internal/relay/catalog.json).

## Measured Chrome for Testing builds

| Profile | Linux browser build | Verification scope |
| --- | --- | --- |
| `chrome_157_cft` | 157.0.8103.38 | Fresh/reused HTTP/2 and a cold WebSocket origin. |
| `chrome_157_cft_PSK` | 157.0.8103.38 | Fresh/reused/resumed HTTP/2 and a cold WebSocket origin. |
| `chrome_152_cft` | 152.0.7977.54 and 152.0.7977.82 | Historical destination TLS and HTTP/2 captures; the current automated comparison targets 157. |

The Chrome 157 fixture uses official [vercel-labs/agent-browser](https://github.com/vercel-labs/agent-browser) **0.41.2**. It explicitly selects Chrome for Testing **157.0.8103.38** with `--executable-path` and runs in headed mode. It does not rely on whichever browser agent-browser discovers by default. Both downloads are SHA-256 pinned in [versions.json](../tests/browser/versions.json).

The comparison checks ClientHello ciphers and extensions, signature algorithms, groups, key shares, ALPN, application settings, certificate compression, trust-anchor order, HTTP/2 settings and window, and ordered request headers. The PSK checks require the destination server to accept TLS session resumption. A generated local CA is trusted with certificate verification enabled.

HTTP and WebSocket clients have separate TLS session caches. Resumption across those modes has not been verified. Other catalog profiles receive functional TLS checks, which do not establish a fingerprint match to a real browser.

## Headers and identity warnings

The plugin preserves supplied headers. It does not rewrite the User-Agent or generate Client Hints to match a profile.

When those headers are present, Activity can warn about disagreements in browser family, version, platform, Chromium Client Hints, or mobile identity. Missing headers or a lack of warnings do not prove a match.

## Observed Caido behavior

### Persisted upstream rules after restart

Caido **0.59.0** loads saved upstream rules before starting backend plugins. In a persistent Docker instance with the released v0.3.0 plugin, a rule with include `*` and exclude `localhost` remained enabled after restart but stopped invoking the plugin. The helper reported running while the controlled origin received Caido's ClientHello: 31 ciphers and no GREASE. Re-saving the rule restored the selected Chrome profile: 16 ciphers, GREASE, and HTTP/2.

The current source works around this startup ordering issue through the supported backend GraphQL SDK. After registering its upstream hook, it reasserts `enabled: true` once for each of its already-enabled rules. It does not toggle them off, create rules, or write filters or rank. Disabled rules and other plugins' rules are skipped. This also runs when shaping or automatic helper startup is disabled, so enabling shaping later uses a valid binding.

If refreshing fails, the backend logs the error and the plugin UI displays a warning alongside helper status. Re-save the rule in **Network → Upstream**, or reload the plugin to retry. A running helper alone does not prove that Caido is routing traffic through it. Verify a controlled destination and Activity before relying on the profile.

The persistent restart regression checks destination ClientHello and HTTP/2, completed Activity, preserved filters and rank, and disabled/excluded traffic. The issue was reproduced with the old ZIP and repaired by the startup refresh; this is a plugin workaround, not a change to Caido's startup order.

### Upstream-hook errors

Caido **0.59.0** logged a thrown upstream-hook exception and then sent the request using its own transport in a controlled test. A thrown error does not block routing in that build. The current backend instead returns a private connection serving HTTP 503 when settings cannot be read, the helper is stopped/unreachable, or a named egress request arrives while shaping is disabled. That listener never forwards requests. Restart tests verify this behavior for HTTP and WebSocket handshakes.

### Browser-facing protocol and Priority

In isolated Caido **0.59.0** checks, Chrome 157 negotiated HTTP/1.1 with Caido using both network stacks. Its requests omitted the `Priority` header sent on direct HTTP/2 navigation. The destination TLS and HTTP/2 settings and remaining ordered headers matched the direct capture.

Supplying the captured headers through Caido CONNECT preserved `Priority` and the complete header order. The relay cannot reconstruct a header the browser did not send. These results do not establish full browser parity through Caido.

### Streaming and cancellation

In controlled Caido **0.58.3** checks:

- Finite 100-second event streams completed through the relay but reached the browser only after completion.
- Short streaming probes also received no incremental data through Caido's built-in transport, with either network stack.
- Browser aborts did not close the controlled origin stream within ten seconds.

The relay streams data and cancels work when its own connection closes, but propagation to and from the browser depends on Caido. Removing relay timeouts does not resolve that build's buffering or cancellation behavior. The plugin does not apply a direct-route workaround.

## Validation of v0.3.0

The packaged helper passed eight Chrome 157 comparisons. A fresh, offline Caido **0.59.0** Docker instance was also checked for ZIP installation, automatic startup, all selector entries, profile search and persistence, stop/start, private-header removal, completed Activity rows, HTTP uploads, and verified HTTPS through CONNECT. Disposable CAs were used for certificate verification on both connections.

See [development and testing](development.md) for repeatable checks. Caido and the origin can impose their own timeouts, buffering, or size limits independently of the relay.
