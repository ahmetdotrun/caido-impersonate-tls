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

The native transport binds only to loopback, selects an ephemeral port, requires a random per-process token, strips all private routing headers, rejects CONNECT, and verifies target certificates. The token is exchanged through a mode-`0600` one-time file that the helper removes before readiness. A separate ownership heartbeat makes the helper exit after the plugin stops refreshing it.

## Authorized use

This project is intended for authorized application-security assessment. Users are responsible for respecting target scope, rate limits, applicable law, and program rules.
