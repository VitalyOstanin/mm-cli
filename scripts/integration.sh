#!/usr/bin/env bash
# Run the integration tests against a throwaway Mattermost server in a
# container (podman, or docker when podman is missing).
#
#   scripts/integration.sh            start a server, run the tests, remove it
#   scripts/integration.sh --keep     leave the server running afterwards
#
# MM_TEST_IMAGE overrides the image, MM_TEST_PORT the host port (8065).
# MM_TEST_SCHEDULED=available|unavailable states whether the server has
# scheduled messages (mattermost-preview has no license: unavailable); unset,
# the scheduled-message test is skipped on a server without them.
# Without Go on the host the tests run in a golang container that shares the
# server container's network, so the server is still at 127.0.0.1.
# MM_TEST_GO_IMAGE overrides that image; keep its version in step with go.mod.
set -euo pipefail

cd "$(dirname "$0")/.."

image=${MM_TEST_IMAGE:-docker.io/mattermost/mattermost-preview:12.1.0}
port=${MM_TEST_PORT:-8065}
name=mm-cli-integration
keep=false
[[ ${1:-} == --keep ]] && keep=true

engine=podman
command -v podman >/dev/null || engine=docker

cleanup() {
	if ! $keep; then
		"$engine" rm -f "$name" >/dev/null 2>&1 || true
	fi
}
trap cleanup EXIT

if ! "$engine" container exists "$name" 2>/dev/null; then
	echo "==> starting $image (the first pull is about 1 GB)"
	"$engine" run -d --name "$name" -p "127.0.0.1:$port:8065" "$image" >/dev/null
fi

url="http://127.0.0.1:$port"
echo "==> waiting for $url (up to 5 minutes)"
for _ in $(seq 1 150); do
	if curl -fsS --max-time 5 "$url/api/v4/system/ping" >/dev/null 2>&1; then
		break
	fi
	sleep 2
done
curl -fsS --max-time 5 "$url/api/v4/system/ping" >/dev/null

test_cmd=(go test -tags integration -count=1 -timeout 10m -v ./internal/integration/)
echo "==> ${test_cmd[*]}"
if command -v go >/dev/null; then
	MM_TEST_URL=$url "${test_cmd[@]}"
else
	"$engine" run --rm --network "container:$name" \
		-v "$PWD:/src" -w /src \
		-v mm-cli-go-mod:/go/pkg/mod -v mm-cli-go-build:/root/.cache/go-build \
		-e MM_TEST_URL=http://127.0.0.1:8065 -e MM_TEST_SCHEDULED \
		"${MM_TEST_GO_IMAGE:-docker.io/library/golang:1.26.10-bookworm}" "${test_cmd[@]}"
fi
