# Security policy

## Reporting

Please use GitHub's private vulnerability reporting feature when it is available, or email aerenkilic@pm.me before opening a public issue. Do not include live target credentials, session cookies, or sensitive captured traffic in reports.

## Release boundary

The GitHub Actions build intentionally:

- pins base images by digest;
- uses committed pnpm and Go checksum locks;
- disables npm-compatible lifecycle scripts;
- runs post-fetch verification with networking disabled;
- bundles rather than downloads the runtime transport;
- verifies the bundled transport before execution.

## Runtime boundary

The forwarding transport binds only to loopback, selects an ephemeral port, requires a random per-process token, strips all private routing headers, rejects CONNECT, and verifies target certificates. The token is exchanged through a mode-`0600` one-time file that the helper removes before readiness. A separate ownership heartbeat makes the helper exit after the plugin stops refreshing it.

A separate reject-only instance returns HTTP 503 on loopback while forwarding is stopped or disabled. It does not require authentication because it never forwards traffic or exposes configuration. Both instances use the verified bundled binary and ownership heartbeat. This avoids Caido 0.59.0's direct fallback after an upstream-hook exception.

Named egress proxy credentials are stored in a mode-`0600` file under a mode-`0700` directory. Backend reads return only names and redacted proxy endpoints. Proxy errors are sanitized before responses, logs, or Activity; separate client pools and TLS caches prevent connection/session sharing between names.

## Authorized use

This project is intended for authorized application-security assessment. Users are responsible for respecting target scope, rate limits, applicable law, and program rules.
