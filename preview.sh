#!/usr/bin/env bash
# fzf preview: карточка задачи из кэша
jq -r --arg id "$1" '
  .data.viewer.assignedIssues.nodes[] | select(.identifier == $id) |
  "\(.identifier) · \(.state.name) · \(.priorityLabel // "")\n\(.title)\n\n\(.url)\nветка: \(.branchName)\n\n\(.description // "(без описания)")"
' "$2"
