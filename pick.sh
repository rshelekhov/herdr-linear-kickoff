#!/usr/bin/env bash
# Popup: выбрать задачу Linear → worktree на её ветке → install → claude с промптом
DIR="$(cd "$(dirname "$0")" && pwd)"
. "$DIR/lib.sh"

die() {
  printf '\n\033[31m%s\033[0m\n\nНажми любую клавишу…' "$*"
  read -rsn1
  exit 1
}

load_settings
load_secret || die "Нет Linear API key. Положи его в $SECRET_FILE (LINEAR_API_KEY=...), права 600."

ws="${LW_SOURCE_WORKSPACE:-}"
[ -z "$ws" ] && ws=$(context_workspace)
[ -n "$ws" ] || die "Не удалось определить текущий workspace."
cwd=$(workspace_cwd "$ws")
repo=$(git -C "${cwd:-/nonexistent}" rev-parse --show-toplevel 2>/dev/null) ||
  die "Текущий workspace не в git-репозитории: ${cwd:-?}"

printf 'Загружаю задачи Linear…'
cache="$STATE_DIR/issues.json"
resp=$(linear_issues) || die "Linear API: $(printf '%s' "$resp" | head -c 300)"
if printf '%s' "$resp" | jq -e '.errors' >/dev/null 2>&1; then
  die "Linear API: $(printf '%s' "$resp" | jq -r '.errors[0].message')"
fi
printf '%s' "$resp" >"$cache"
printf '\r\033[K'

list=$(jq -r '.data.viewer.assignedIssues.nodes[] | [.identifier, .state.name, .title] | @tsv' "$cache")
[ -n "$list" ] || die "Нет назначенных незакрытых задач."

sel=$(printf '%s\n' "$list" | fzf \
  --delimiter=$'\t' --with-nth=1,2,3 --tabstop=2 \
  --prompt='Linear › ' \
  --header="$(basename "$repo") · Enter — worktree + claude · Esc — отмена" \
  --preview "bash '$DIR/preview.sh' {1} '$cache'" \
  --preview-window='right,50%,wrap' \
  --layout=reverse) || exit 0

id=$(printf '%s' "$sel" | cut -f1)
issue=$(jq -c --arg id "$id" '.data.viewer.assignedIssues.nodes[] | select(.identifier == $id)' "$cache")
title=$(printf '%s' "$issue" | jq -r '.title')
branch=$(printf '%s' "$issue" | jq -r '.branchName // empty')
[ -n "$branch" ] || die "У задачи $id нет branchName."

# Промпт для claude
mkdir -p "$STATE_DIR/prompts"
pf="$STATE_DIR/prompts/$id.md"
printf '%s' "$issue" | jq -r --arg suffix "$PROMPT_SUFFIX" '
  "Задача \(.identifier): \(.title)\n\(.url)\n\nОписание:\n\(.description // "(без описания)")\n\n\($suffix)"
' >"$pf"

printf 'Ветка %s…\n' "$branch"

# Уже есть checkout этой ветки → открыть его, иначе создать
existing=$("$HERDR" worktree list --workspace "$ws" |
  jq -r --arg b "$branch" '.result.worktrees[] | select(.branch == $b) | .path' | head -1)

if [ -n "$existing" ]; then
  out=$("$HERDR" worktree open --workspace "$ws" --path "$existing" --label "$id" --focus 2>&1) ||
    die "worktree open: $out"
  created=0
else
  base_args=()
  if [ -n "$BASE_REF" ]; then
    if [ "${FETCH_BEFORE_CREATE:-1}" = 1 ] && [[ "$BASE_REF" == origin/* ]]; then
      git -C "$repo" fetch --quiet origin || true
    fi
    # Если ветка уже есть на remote — брать её, а не BASE_REF
    if git -C "$repo" rev-parse --verify --quiet "refs/remotes/origin/$branch" >/dev/null; then
      base_args=(--base "origin/$branch")
    else
      base_args=(--base "$BASE_REF")
    fi
  fi
  out=$("$HERDR" worktree create --workspace "$ws" --branch "$branch" "${base_args[@]}" --label "$id" --focus 2>&1) ||
    die "worktree create: $out"
  created=1
fi

new_ws=$(printf '%s' "$out" | jq -r '.result.workspace.workspace_id // empty')
pane=$(printf '%s' "$out" | jq -r '.result.root_pane.pane_id // empty')
[ -n "$new_ws" ] || die "Неожиданный ответ herdr: $out"
"$HERDR" workspace rename "$new_ws" "$id" >/dev/null 2>&1 || true

if [ "$created" = 1 ] && [ -n "$pane" ]; then
  cmd=""
  if [ -n "$INSTALL_CMD" ]; then
    # bun/npm/pnpm install — только если есть package.json
    if [[ "$INSTALL_CMD" =~ ^(bun|npm|pnpm|yarn)\  ]]; then
      cmd="[ -f package.json ] && $INSTALL_CMD; "
    else
      cmd="$INSTALL_CMD; "
    fi
  fi
  cmd+="$CLAUDE_CMD \"\$(cat '$pf')\""
  "$HERDR" pane run "$pane" "$cmd" >/dev/null
fi

exit 0
