#!/bin/sh
set -eu

project_dir=$(CDPATH= cd "$(dirname "$0")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -f "$fixture"/*; rmdir "$fixture"' EXIT
trap 'exit 1' HUP INT TERM

cat > "$fixture/bot" <<'BOT'
#!/bin/sh
printf '%s\n' "$#" "$@" > "$RECORD"
exit "${BOT_EXIT:-0}"
BOT
chmod +x "$fixture/bot"
sed "s|/app/saveany-bot|$fixture/bot|g" "$project_dir/entrypoint.sh" > "$fixture/entrypoint.sh"
export RECORD="$fixture/record"
printf 'original configuration\n' > "$fixture/config.toml"
cp "$fixture/config.toml" "$fixture/expected"
chmod 444 "$fixture/config.toml"

CONFIG_URL= sh "$fixture/entrypoint.sh"
printf '0\n' > "$fixture/expected-record"
cmp "$RECORD" "$fixture/expected-record"

url='https://example.invalid/config?token=fixture-secret&other=value'
CONFIG_URL="$url" sh "$fixture/entrypoint.sh" > "$fixture/output"
printf '2\n--config\n%s\n' "$url" > "$fixture/expected-record"
cmp "$RECORD" "$fixture/expected-record"
if grep -F 'fixture-secret' "$fixture/output"; then
    echo 'entrypoint exposed the URL' >&2
    exit 1
fi
cmp "$fixture/config.toml" "$fixture/expected"

status=0
CONFIG_URL="$url" BOT_EXIT=17 sh "$fixture/entrypoint.sh" > "$fixture/output" || status=$?
test "$status" -eq 17
cmp "$fixture/config.toml" "$fixture/expected"

rm "$RECORD"
status=0
CONFIG_URL='file:///config.toml' sh "$fixture/entrypoint.sh" > "$fixture/output" || status=$?
test "$status" -eq 1
test ! -e "$RECORD"
cmp "$fixture/config.toml" "$fixture/expected"
echo 'entrypoint contracts passed'
