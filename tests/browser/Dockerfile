# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM golang@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS tools
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src/transport
COPY transport/go.mod transport/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify
COPY transport/ ./
RUN --network=none --mount=type=cache,target=/go/pkg/mod \
    go build -o /out/caido-impersonate-transport ./cmd/transport && \
    go build -o /out/fingerprint ./cmd/fingerprint

FROM node@sha256:752ea8a2f758c34002a0461bd9f1cee4f9a3c36d48494586f60ffce1fc708e0e
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates unzip xvfb x11-utils libnss3-tools libnss3 libatk-bridge2.0-0 \
    libgtk-3-0 libgbm1 libasound2 libxss1 fonts-liberation && \
    rm -rf /var/lib/apt/lists/* && \
    mkdir -p /tmp/.X11-unix && chmod 1777 /tmp/.X11-unix
WORKDIR /src
COPY tests/browser/ tests/browser/
RUN node tests/browser/install.mjs
COPY --from=tools /out/ /usr/local/bin/
COPY scripts/profile-compat.mjs scripts/profile-compat.mjs
ENV CHROME_BIN=/opt/browser/chrome-linux64/chrome \
    AGENT_BROWSER_NO_VERSION_CHECK=1 DISPLAY=:99
USER node
CMD ["sh", "tests/browser/run.sh"]
