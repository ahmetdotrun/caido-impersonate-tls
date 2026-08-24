# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

FROM golang@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS transport-deps

ENV PATH="/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin" \
    GOTOOLCHAIN=local \
    CGO_ENABLED=0
WORKDIR /src/transport

COPY transport/go.mod transport/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download && go mod verify

FROM transport-deps AS transport-build

COPY LICENSE /src/LICENSE
COPY transport/ ./
RUN --network=none --mount=type=cache,target=/go/pkg/mod \
    go test ./...
RUN --network=none --mount=type=cache,target=/go/pkg/mod \
    GOOS=linux GOARCH=amd64 go build \
      -trimpath \
      -ldflags="-s -w -buildid=" \
      -o /out/linux-amd64/caido-impersonate-transport \
      ./cmd/transport
RUN --network=none --mount=type=cache,target=/go/pkg/mod set -eu; \
    mkdir -p /out/licenses; \
    cp /src/LICENSE /out/licenses/PROJECT_LICENSE.txt; \
    cp /usr/local/go/LICENSE /out/licenses/GO_LICENSE.txt; \
    go version -m /out/linux-amd64/caido-impersonate-transport \
      > /out/licenses/TRANSPORT_BUILDINFO.txt; \
    copy_license() { \
      module="$1"; \
      target="$2"; \
      directory="$(go list -m -f '{{.Dir}}' "$module")"; \
      license="$(find "$directory" -maxdepth 1 -type f \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' \) | sort | head -n 1)"; \
      test -n "$license"; \
      cp "$license" "/out/licenses/$target"; \
    }; \
    copy_license github.com/andybalholm/brotli ANDYBALHOLM_BROTLI.txt; \
    copy_license github.com/bdandy/go-errors BDANDY_GO_ERRORS.txt; \
    copy_license github.com/bdandy/go-socks4 BDANDY_GO_SOCKS4.txt; \
    copy_license github.com/bogdanfinn/quic-go-utls BOGDANFINN_QUIC_GO_UTLS.txt; \
    copy_license github.com/bogdanfinn/tls-client BOGDANFINN_TLS_CLIENT.txt; \
    copy_license github.com/bogdanfinn/utls BOGDANFINN_UTLS.txt; \
    copy_license github.com/bogdanfinn/websocket BOGDANFINN_WEBSOCKET.txt; \
    copy_license github.com/klauspost/compress KLAUSPOST_COMPRESS.txt; \
    copy_license github.com/quic-go/qpack QUIC_GO_QPACK.txt; \
    copy_license github.com/tam7t/hpkp TAM7T_HPKP.txt; \
    copy_license golang.org/x/crypto GOLANG_X_CRYPTO.txt; \
    copy_license golang.org/x/net GOLANG_X_NET.txt; \
    copy_license golang.org/x/sys GOLANG_X_SYS.txt; \
    copy_license golang.org/x/text GOLANG_X_TEXT.txt
RUN --network=none set -eu; \
    mkdir -p /tmp/transport-smoke; \
    chmod 700 /tmp/transport-smoke; \
    printf '%064d\n' 0 > /tmp/transport-smoke/token; \
    printf 'owner\n' > /tmp/transport-smoke/owner; \
    chmod 600 /tmp/transport-smoke/token /tmp/transport-smoke/owner; \
    /out/linux-amd64/caido-impersonate-transport \
      --listen 127.0.0.1:0 \
      --token-file /tmp/transport-smoke/token \
      --owner-file /tmp/transport-smoke/owner \
      > /tmp/transport-smoke/ready.json \
      2> /tmp/transport-smoke/stderr.log & \
    transport_pid=$!; \
    attempt=0; \
    while ! grep -q '"event":"ready"' /tmp/transport-smoke/ready.json; do \
      kill -0 "$transport_pid"; \
      attempt=$((attempt + 1)); \
      test "$attempt" -lt 50; \
      sleep 0.1; \
    done; \
    test ! -e /tmp/transport-smoke/token; \
    kill -TERM "$transport_pid"; \
    wait "$transport_pid"

FROM node@sha256:752ea8a2f758c34002a0461bd9f1cee4f9a3c36d48494586f60ffce1fc708e0e AS plugin-deps

ENV COREPACK_HOME=/opt/corepack \
    PNPM_HOME=/opt/pnpm \
    npm_config_cache=/opt/npm-cache
WORKDIR /src

RUN mkdir -p /opt/corepack-bin && \
    corepack enable --install-directory /opt/corepack-bin
ENV PATH="/opt/corepack-bin:${PATH}"

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY packages/backend/package.json packages/backend/package.json
COPY packages/frontend/package.json packages/frontend/package.json
COPY packages/shared/package.json packages/shared/package.json
RUN --mount=type=cache,target=/opt/pnpm/store \
    pnpm install --frozen-lockfile --ignore-scripts

FROM plugin-deps AS verify

COPY . .
COPY --from=transport-build \
  /out/linux-amd64/caido-impersonate-transport \
  /src/assets/transport/linux-amd64/caido-impersonate-transport
COPY --from=transport-build /out/licenses /src/assets/transport/licenses
RUN cd /src/assets/transport && \
    sha256sum linux-amd64/caido-impersonate-transport > checksums.sha256

RUN --network=none pnpm typecheck
RUN --network=none pnpm lint
RUN --network=none pnpm knip
RUN --network=none pnpm build

FROM scratch AS artifact

COPY --from=verify /src/dist/plugin_package.zip /plugin_package.zip
