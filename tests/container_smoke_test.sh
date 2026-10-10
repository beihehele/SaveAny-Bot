#!/bin/sh
set -eu

if [ "$#" -ne 5 ]; then
    echo 'usage: container_smoke_test.sh IMAGE BUILDER_IMAGE CORE_TEST_BINARY DATABASE_TEST_BINARY COMMIT' >&2
    exit 2
fi
image=$1
builder=$2
core_binary=$3
database_binary=$4
commit=$5
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
fixture=$(mktemp -d "${TMPDIR:-/tmp}/saveany-container-XXXXXX")
name="saveany-smoke-$(basename "$fixture")"
server_pid=
cleanup() {
    for suffix in badurl unavailable cancelled; do
        docker rm -f "$name-$suffix" >/dev/null 2>&1 || true
    done
    if [ -n "$server_pid" ]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    # This path is owned by mktemp above, never a supplied deployment directory.
    case "$fixture" in */saveany-container-*) rm -rf -- "$fixture" ;; esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

docker run --rm --network none --entrypoint sh "$builder" -ec \
    'test ! -e /app/config.toml; test ! -e /app/tmp-container-smoke'
docker run --rm --network none --entrypoint /app/saveany-bot "$image" version > "$fixture/version"
grep -F "Commit: $commit" "$fixture/version"
grep -F 'linux/amd64' "$fixture/version"
for tool in ffmpeg ffprobe; do
    docker run --rm --network none --entrypoint "$tool" "$image" -version > "$fixture/$tool"
done
docker run --rm --network none --entrypoint yt-dlp "$image" --version

printf 'mounted original configuration\n' > "$fixture/config.toml"
cp "$fixture/config.toml" "$fixture/expected-config"
status=0
timeout 20s docker run --name "$name-badurl" --network none \
    --mount "type=bind,source=$fixture/config.toml,target=/app/config.toml,readonly" \
    -e CONFIG_URL=file:///invalid "$image" > "$fixture/badurl.log" 2>&1 || status=$?
test "$status" -eq 1
grep -F 'CONFIG_URL must be an HTTP(S) URL' "$fixture/badurl.log"
cmp "$fixture/config.toml" "$fixture/expected-config"

python3 "$script_dir/container_config_fixture.py" "$fixture" > "$fixture/server.log" 2>&1 &
server_pid=$!
attempts=0
until [ -s "$fixture/port" ]; do
    attempts=$((attempts + 1))
    test "$attempts" -lt 100
    kill -0 "$server_pid"
    sleep 0.1
done
port=$(cat "$fixture/port")
case "$port" in ''|*[!0-9]*) exit 1 ;; esac
status=0
timeout 20s docker run --name "$name-unavailable" --network host \
    --mount "type=bind,source=$fixture/config.toml,target=/app/config.toml,readonly" \
    -e "CONFIG_URL=http://127.0.0.1:$port/unavailable" "$image" > "$fixture/unavailable.log" 2>&1 || status=$?
test "$status" -eq 1
grep -F 'status code 503' "$fixture/unavailable.log"
cmp "$fixture/config.toml" "$fixture/expected-config"

docker run -d --name "$name-cancelled" --network host \
    --mount "type=bind,source=$fixture/config.toml,target=/app/config.toml,readonly" \
    -e "CONFIG_URL=http://127.0.0.1:$port/blocked" "$image" > "$fixture/container-id"
attempts=0
until [ -s "$fixture/request-ready" ]; do
    attempts=$((attempts + 1))
    test "$attempts" -lt 100
    test "$(docker inspect --format '{{.State.Running}}' "$name-cancelled")" = true
    sleep 0.1
done
timeout 15s docker stop --timeout 10 "$name-cancelled"
test "$(docker inspect --format '{{.State.ExitCode}}' "$name-cancelled")" = 0
test "$(docker inspect --format '{{.State.OOMKilled}}' "$name-cancelled")" = false
docker logs "$name-cancelled" > "$fixture/cancelled.log" 2>&1
grep -F 'Startup cancelled' "$fixture/cancelled.log"
cmp "$fixture/config.toml" "$fixture/expected-config"

# These are the project's real worker/hook and recovery tests inside the
# shipped Alpine runtime. They need no Telegram accounts or external services.
docker run --rm --network none \
    --mount "type=bind,source=$core_binary,target=/app/core-tests,readonly" \
    --entrypoint /app/core-tests "$image" -test.v -test.timeout=90s \
    -test.run '^(TestIdleWorkersStopOnServiceCancellation|TestShutdownCancelsIndependentTasksAndKeepsCancelHook|TestWorkerSuccessAndFailureHooks|TestHookTimeoutPreservesCauseAndReleasesWorker|TestHookCancellationStopsDescendant|TestTerminalHookKeepsShutdownGraceAndShorterBudget)$'
docker run --rm --network none \
    --mount "type=bind,source=$database_binary,target=/app/database-tests,readonly" \
    --entrypoint /app/database-tests "$image" -test.v -test.timeout=90s \
    -test.run '^TestStoppedDeploymentBackupUpgradeAndRestore$'
echo 'container startup, worker shutdown, hooks and synthetic recovery passed'
