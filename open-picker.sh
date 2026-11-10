#!/usr/bin/env bash
# Действие-хоткей: открыть popup с выбором задачи
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/lib.sh"
ws=$(context_workspace)
exec "$HERDR" plugin pane open --plugin "$PLUGIN_ID" --entrypoint picker --placement popup \
  --width 85% --height 80% ${ws:+--workspace "$ws"} --env "LW_SOURCE_WORKSPACE=$ws"
