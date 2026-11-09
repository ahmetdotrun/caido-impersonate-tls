# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM golang@sha256:e401dae1bf814e29204a8cb7915682e1780951e609ca0dd8865ee1937f510c48 AS origin
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
WORKDIR /src/transport
COPY transport/go.mod transport/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download && go mod verify
COPY transport/ ./
RUN --network=none --mount=type=cache,target=/go/pkg/mod \
    go build -o /out/fingerprint ./cmd/fingerprint

FROM node@sha256:752ea8a2f758c34002a0461bd9f1cee4f9a3c36d48494586f60ffce1fc708e0e
COPY --from=origin /out/fingerprint /usr/local/bin/fingerprint
COPY tests/caido/*.mjs /fixture/
USER node
CMD ["node", "/fixture/restart.mjs", "serve"]
