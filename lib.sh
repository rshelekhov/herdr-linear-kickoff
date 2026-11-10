# shellcheck shell=bash
# Общие функции плагина rs.linear-kickoff. Подключается через `. lib.sh`.
set -uo pipefail

HERDR="${HERDR_BIN_PATH:-herdr}"
PLUGIN_ID="${HERDR_PLUGIN_ID:-rs.linear-kickoff}"
CONFIG_DIR="${HERDR_PLUGIN_CONFIG_DIR:-$("$HERDR" plugin config-dir "$PLUGIN_ID")}"
STATE_DIR="${HERDR_PLUGIN_STATE_DIR:-$CONFIG_DIR/../state}"
SECRET_FILE="$CONFIG_DIR/secret.env"
SETTINGS_FILE="$CONFIG_DIR/settings.env"

mkdir -p "$STATE_DIR"

ensure_settings() {
  [ -f "$SETTINGS_FILE" ] && return 0
  cat >"$SETTINGS_FILE" <<'EOF'
# Настройки плагина rs.linear-kickoff (секрет — в secret.env, не здесь)

# Команда после создания worktree; пусто — не запускать
INSTALL_CMD="bun install"

# База для новой ветки; пусто — HEAD текущего checkout (поведение herdr по умолчанию)
# Например: BASE_REF="origin/main"
BASE_REF=""

# Если BASE_REF начинается с origin/ — сделать git fetch перед созданием
FETCH_BEFORE_CREATE=1

CLAUDE_CMD="claude"
PROMPT_SUFFIX="Изучи задачу и предложи план, прежде чем писать код."
EOF
}

load_settings() {
  ensure_settings
  INSTALL_CMD="bun install"; BASE_REF=""; FETCH_BEFORE_CREATE=1
  CLAUDE_CMD="claude"; PROMPT_SUFFIX="Изучи задачу и предложи план, прежде чем писать код."
  # shellcheck disable=SC1090
  . "$SETTINGS_FILE"
}

load_secret() {
  [ -f "$SECRET_FILE" ] || return 1
  local mode
  mode=$(stat -f '%Lp' "$SECRET_FILE" 2>/dev/null || stat -c '%a' "$SECRET_FILE")
  [ "$mode" = "600" ] || chmod 600 "$SECRET_FILE"
  # shellcheck disable=SC1090
  . "$SECRET_FILE"
  [ -n "${LINEAR_API_KEY:-}" ]
}

linear_issues() {
  local q
  q='query { viewer { assignedIssues(first: 150, orderBy: updatedAt, filter: { state: { type: { nin: ["completed", "canceled"] } } }) { nodes { identifier title url branchName description priorityLabel state { name } team { key } } } } }'
  curl -sS --fail-with-body https://api.linear.app/graphql \
    -H "Authorization: $LINEAR_API_KEY" \
    -H 'Content-Type: application/json' \
    --data "$(jq -n --arg q "$q" '{query: $q}')"
}

# Workspace, из которого вызвали плагин
context_workspace() {
  local ws=""
  if [ -n "${HERDR_PLUGIN_CONTEXT_JSON:-}" ]; then
    ws=$(printf '%s' "$HERDR_PLUGIN_CONTEXT_JSON" | jq -r '.workspace_id // .workspace.workspace_id // .workspace.id // empty' 2>/dev/null)
  fi
  [ -z "$ws" ] && ws="${HERDR_WORKSPACE_ID:-}"
  [ -z "$ws" ] && ws=$("$HERDR" workspace list | jq -r '.result.workspaces[] | select(.focused) | .workspace_id' | head -1)
  printf '%s' "$ws"
}

workspace_cwd() {
  "$HERDR" pane list --workspace "$1" |
    jq -r '.result.panes | (map(select(.focused))[0] // .[0]) | .foreground_cwd // .cwd // empty'
}
