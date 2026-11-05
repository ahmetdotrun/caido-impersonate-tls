#!/bin/sh
# Host orchestration only. All project code executes inside disposable containers.
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
artifacts=$(CDPATH= cd -- "${1:-$repo/dist}" && pwd)
test -s "$artifacts/plugin_package.zip"
fixture_image=${CAIDO_RESTART_FIXTURE_IMAGE:-impersonate-tls-caido-test}
caido_image=caido/caido@sha256:cc33697ec4a13552bb6b4e92ac966eba44c95f2896df78c8ca2e9a73677ae5b3
prefix="impersonate-tls-restart-$(date +%s)-$$"
fixture="$prefix-fixture"
caido="$prefix-caido"
volume="$prefix-data"
label="io.caido.impersonate-tls.test=$prefix"

cleanup() {
  status=$?
  trap - EXIT
  if [ "$status" -ne 0 ]; then
    docker logs --tail 50 "$caido" >&2 2>/dev/null || true
    docker logs --tail 20 "$fixture" >&2 2>/dev/null || true
  fi
  docker rm -f "$caido" "$fixture" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker volume create --label "$label" "$volume" >/dev/null
docker run --rm --network none --user root --cap-drop ALL --cap-add CHOWN \
  --security-opt no-new-privileges --mount "type=volume,src=$volume,dst=/test-data" \
  --entrypoint sh "$caido_image" -c 'chown caido:caido /test-data'
docker run --detach --name "$fixture" --label "$label" --network none \
  --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=bind,src=$artifacts,dst=/artifacts,readonly" "$fixture_image" >/dev/null
# The fixture owns the network namespace so its origin survives Caido restarts.
docker run --detach --name "$caido" --label "$label" \
  --network "container:$fixture" --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=volume,src=$volume,dst=/test-data" "$caido_image" \
  caido-cli --no-renderer-sandbox --no-open --no-sync --allow-guests \
  --data-path /test-data --listen 127.0.0.1:8080 >/dev/null

probe() { docker exec "$fixture" node /fixture/restart.mjs "$@"; }
probe wait-origin
# Trust only this fixture's generated public CA inside the disposable instance.
docker exec "$fixture" cat /tmp/origin-ca.pem | \
  docker exec --interactive --user root "$caido" sh -c 'cat >> /etc/ssl/certs/ca-certificates.crt'
probe setup
probe assert-shaped
for iteration in 1 2; do
  docker restart --timeout 2 "$caido" >/dev/null
  probe login
  probe assert-shaped
done
probe disable-rule
docker restart --timeout 2 "$caido" >/dev/null
probe login
probe assert-disabled-rule
probe enable-rule
probe disable-shaping
docker restart --timeout 2 "$caido" >/dev/null
probe login
probe assert-disabled-shaping
probe enable-shaping
probe assert-shaped
printf '%s\n' 'Caido 0.59.0 persistent restart regression passed.'
