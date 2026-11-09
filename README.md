# Impersonate TLS for Caido

Apply browser-like TLS and HTTP profiles to selected Caido traffic.

**Linux x86_64 only.**

## How it works

```text
Caido Proxy / Replay / Automate / Workflows
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

## Install

1. [Download the latest plugin ZIP](https://github.com/ahmetdotrun/caido-impersonate-tls/releases/latest/download/plugin_package.zip).
2. In Caido, open **Plugins → Install Package** and select the ZIP.
3. Open **Impersonate TLS**, confirm the status is **Running**, and choose a profile.
4. In **Settings → Network → Upstream Plugins**, add this plugin. Set the included hosts to `*` for all domains, or use a narrower pattern.
5. Send traffic through Proxy, Replay, Automate, or workflows. Check the plugin's **Activity** tab for results.

## Features

- **86 profiles** covering Chrome, Brave, Firefox, Safari, Opera, OkHttp, and application snapshots.
- HTTPS with HTTP/2 or HTTP/1.1, plain HTTP, and WebSockets.
- [Named egress profiles](docs/egress.md) for authenticated HTTP/SOCKS5 proxies, with isolated connections and TLS sessions.
- Activity showing the selected profile, response status, protocol, duration, and identity warnings.
- A bundled transport that starts automatically, verifies target certificates, and blocks matching requests if it becomes unavailable.

New installations default to **Chrome for Testing 157 (Linux, PSK)**. Existing saved selections are preserved. The profile selector shows which browser builds have been measured.

## Limitations

- Request headers stay as supplied. Match the profile to your browser, User-Agent, and Client Hints.
- Caido can affect headers, streaming, and cancellation. A selected profile alone does not establish an exact browser match.
- HTTP/3 and custom ClientHello or JA3/JA4 imports are not supported.

## Build

Build and verify everything in Docker:

```sh
docker build --target artifact --output type=local,dest=dist .
```

The installable package is written to `dist/plugin_package.zip`.

## Documentation

- [Profiles and compatibility](docs/compatibility.md) — measured builds and known Caido behavior.
- [Transport reference](docs/transport.md) — settings, limits, architecture, and Activity privacy.
- [Development and testing](docs/development.md) — source layout, Docker checks, and browser fixtures.
- [Security policy](SECURITY.md)

## License

[MIT](LICENSE). Built on [tls-client](https://github.com/bogdanfinn/tls-client).
