# SPEC — Linear plugin for the herdr TUI

Name: **herdr-linear-kickoff** (plugin id `rs.linear-kickoff`).

## Why

One key: pick a Linear issue → get a worktree on the right branch → an agent
(claude/codex) starts in it already knowing the whole issue (description, comments)
and gets to work. No manual "read issue ABC-964".

Non-goals: a full Linear client (creating issues with attachments, projects, cycles,
an OAuth app, integrations with other plugins).

## Platform and stack

- herdr TUI ≥ 0.9.5, macOS and Linux.
- Go + bubbletea/lipgloss, a single binary.
- Install: `herdr plugin install owner/repo`. `[[build]]` runs
  `scripts/build.sh`: `go build`, or, without Go, downloads the binary from GitHub
  Releases and verifies its SHA-256 (goreleaser).
- Linear GraphQL API, authenticated with a personal API key.

## v1 features

### 1. API keys and workspaces
- One personal API key per Linear workspace (a key belongs to one). No workspace yet →
  the popup asks for a key (hidden input); `p` → "Add a workspace…" adds another.
- Checked with a `viewer` query, which also says the workspace (organization id, name, URL key).
- Storage: macOS Keychain; Linux — Secret Service, falling back to a `0600` file
  in `HERDR_PLUGIN_CONFIG_DIR`; one entry per workspace. The single key of earlier versions
  moves to its workspace on first run.
- `LINEAR_API_KEY` from the environment stands in when no key is saved; a key that answers
  for another workspace than the one it's used for is refused.
- `state/workspaces.json` (nothing secret): the workspaces, the one last used, and the one
  last used per repo. The popup opens on the workspace of the repo it's opened from (last
  used there, else last used anywhere); `p` lists the workspaces to switch; starting an
  issue in a repo ties the repo to the workspace.
- Lists, scope and caches are per workspace (`state/ws/<id>/`); answers from a workspace
  switched away from are dropped.
- Settings: the current workspace's key — replace it, or remove the workspace.
  `logout` removes every key.

### 2. Issue list
- Tabs: **Mine** (assigned to me, open) / **All** (active: triage, todo, in progress — anyone's) /
  **Backlog** / **Recent** (created by me, assigned to me or followed, updated in the last
  30 days, any state, up to 50); `tab`/`1-4`. Counts on the tabs.
- Scope (`p`): all my teams, one team, or one project — applies to every tab, filtered by
  Linear (the 150 limit is per scope), remembered between popups.
- Grouped by state, In Progress first; within a group — priority, then updatedAt.
- A row shows: identifier, state (Linear icon/color), priority, title,
  a "has worktree" mark.
- Search as you type (id, title).
- Preview: issue fields (state, priority, assignee, project, cycle, due date — red when
  overdue —, estimate, labels), description as markdown (tables, code highlighted by
  language), sub-issues, linked pull requests (GitHub/GitLab attachments, with their state;
  `g` opens the open one, else the first), comments.
- `n`: a new issue — title (enter on it or on Create creates it), description, team, project (the team's open
  ones), status (Todo by default), priority, assignee (me by default). The defaults follow the
  scope; once created it's selected in its tab, and `enter` starts it.
- The last list is cached — the popup opens instantly and refreshes in the background.

### 2a. Sub-issues
- In the list, a sub-issue whose parent is listed sits right under it (`↳`, own state icon);
  otherwise it keeps its place.
- Preview: `↑ PARENT title` above a sub-issue's title; a parent shows `Sub-issues · done/total`
  with each sub-issue's state.
- Prompt: `{parent}` (the parent's title and description) and `{subissues}` (list with states),
  both empty when not applicable.
- `subissue_branch`: `own` (default) — each sub-issue gets its own branch and worktree;
  `parent` — it works in the parent's worktree on the parent's branch; if that worktree is
  open, the agent starts in a new tab there. Linear changes apply to the sub-issue.

### 3. Search and issue by ID
- `/` (or `:`) opens a palette over the list: fuzzy search over the loaded tabs (ID and
  title, matched letters lit, the tab shown), then — after a 300 ms pause, from 3 letters —
  Linear's own `searchIssues` (any team, closed issues, descriptions) under "In Linear".
- Typing `ABC-964` or a `linear.app/.../issue/ABC-964/...` link offers that issue directly,
  even if it isn't in any list. `enter` shows the choice in its tab (switching tab, or
  scope as for a link).
- `[[link_handlers]]`: clicking a Linear link in the terminal opens the issue in the plugin.
- An ID not found in the current workspace is looked for in the others (a link's own
  workspace first); found elsewhere, the popup switches there.

### 4. Changing state
- From the list or the card: the workflow states of the issue's team.

### 5. Start (the main flow)
0. A closed issue, one assigned to someone else, or one already in review: asked first
   ("start it anyway?"). `w` (worktree only) isn't asked.
1. Re-fetch the issue from Linear.
2. Resolve the repo: team→repo mapping, otherwise the current workspace's repo.
3. Branch name from the template (§6).
4. Worktree: existing → open and focus it; otherwise create one (§7).
   An existing one gets the agent too, unless an agent already runs there: not open
   in herdr → in the opened workspace's first pane; open with no agent in any pane →
   in a new tab; an agent in it → focus only, nothing sent (when herdr can't tell,
   it counts as busy). A sub-issue in its parent's worktree always gets a new tab.
5. Optionally: move to the team's first "started" state and assign to me
   if unassigned (setting, on by default).
6. Then the install command (if applicable, new worktree only) → the agent
   with the prompt.
7. Rename the workspace to `<ID>` (or per template).

The order means a failure at any step leaves nothing half-done: worktree first,
then Linear changes, then the agent. Every failure gets a clear message.
Once the worktree is focused herdr hides the popup (its process lives on), so any
outcome after that point — notes or a failure — is a herdr notification, and the popup exits.

### 6. Branch name template
Placeholders:

| | example |
|---|---|
| `{linear}` | `abc-964-optimize-the-web-app-for-phone` (Linear branchName) |
| `{id}` / `{ID}` | `abc-964` / `ABC-964` |
| `{team}`, `{number}` | `abc`, `964` |
| `{slug}` | `optimize-the-web-app-for-phone` (truncated to `slug_max`) |
| `{type}` | `feat` / `fix` / `chore` … |
| `{me}` | Linear username |

- Default is `{linear}`.
- `{type}`: label→type mapping (`Bug→fix`, `Feature→feat`, otherwise `type_default`);
  with `ask_type`, a short type picker follows issue selection, preselected.
- A template without `{id}`/`{ID}`/`{linear}` triggers a warning: Linear's GitHub
  integration won't link the PR to the issue automatically.

### 7. Worktree
- An issue's existing worktree is found by: the issue token on the workspace, the
  branch, the identifier inside the branch name — so a custom format doesn't create duplicates.
- The herdr workspace is named by `workspace_name` (default `{ID}`; `{id}`, `{title}` — up to
  40 characters, cut at a word —, `{team}`, `{number}`).
- Base of a new branch: `base` (default: the remote default branch, `origin/HEAD`),
  with `git fetch` first; offline — the local copy.
- Branch already on `origin` → worktree from `origin/<branch>`.
- Branch exists locally → checked out as is.
- The repo's main folder is never an issue's worktree, even when it is on the issue's
  branch (it holds other work, and git won't check the branch out twice): Start stops
  with "switch that folder to another branch and start again", without focusing it.

### 8. Agent and prompt
- Agent: `claude` / `codex` / … (setting); the launch command is configurable.
  `A` starts an issue with another installed agent (or magus manager) this time
  only.
- Prompt template with placeholders `{identifier} {title} {url} {description}
  {comments} {branch} {parent} {subissues}`; default — everything about the issue + "study the issue
  and propose a plan before writing any code".
- Issue content is passed as data (in a prompt file, explicitly marked
  "below is the issue text"), never mixed with instructions.

### 9. Install command
- Configurable (`bun install` etc.), per repo or global; runs only when
  applicable (e.g. `package.json` exists), and only in a worktree just
  created — an existing one has its packages, and may have a dev server or
  another agent using them.

### 10. Which repo an issue starts in
A team's issues may live in several repos (e.g. web, api, mobile, admin), and the
repos an issue touches only become clear while working on it. So there is no default
repo per team; the starting repo is chosen per issue, and later repos are added as the
work finds them.
- Sure, no question: the issue already has a worktree (or a branch) in a known repo;
  or a pin: `[repos]` in config (team key → path), or "always for project/team" set in
  the chooser.
- Otherwise a chooser of the team's repos (every repo ever picked for that team), with a
  guess preselected and its reason: the repo's name mentioned in the title, description
  or labels; the parent issue's worktree repo; the last repo picked for the team.
- "another folder…": fuzzy search over git repos found on the machine (open in herdr,
  picked before, and a shallow scan of `repo_roots`: ~/code, ~/work, ~/projects, ~/dev,
  ~/src, ~/github.com), or a typed path with tab completion, validated as a git repo.
  Listed under a header per root folder (the deepest `repo_roots` entry holding the
  repo, else its parent folder), groups and names sorted; rows show the path inside the root.
- The preview's start card names the repo (`in abc_billing?` — `?` = will ask).
- "clone…" in the chooser: a git URL or org/repo (via `gh` when present, so private
  repos work); cloned into `clone_root`, default next to the team's last repo, else
  ~/code. An existing folder is reused only if it is that repo; option-like URLs and
  the `ext::` transport are refused.
- Not doing: "another repo for this issue" from the plugin. When work turns out to span
  repos, the agent already in the issue's context is told (or finds) where the other
  repos are; a plugin step for it added nothing.

### 10a. Launch mode
- `launch = "prompt"` (default): the agent command with the full issue as its prompt.
- `launch = "magus"`: start magus orchestrator's manager
  (`/orchestrator:manager <id> <url>`) instead; it reads the ticket itself (Linear
  MCP) and opens child sessions in other repos. Refused before any worktree is made unless
  the agent is Claude Code (claude / claudish) and `orchestrator@…` is in Claude Code's
  installed plugins.

### 11. Sidebar tokens
- `$issue`, `$issue_state`, `$issue_title` on workspaces opened on an issue's worktree.
- herdr keeps tokens in memory only: Start links the checkout to the issue
  (state/sidebar.json); `sync` reports them again — at herdr startup (`[[startup]]`,
  refreshing every state from Linear) and on `workspace.focused` (Linear asked at most
  every 5 minutes, one request for all issues). A status change in the popup updates
  them at once; `worktree.removed` drops the link.
- `labels = false` turns it off. README gives a sample `[ui.sidebar.spaces].rows`.

### 12. Settings
- A settings screen in the TUI + a `config.toml` in `HERDR_PLUGIN_CONFIG_DIR`
  (edit either, same result).
- Keys: `branch_template`, `workspace_name`, `type_map`, `type_default`, `ask_type`, `slug_max`,
  `base`, `repos`, `install`, `agent`, `agent_cmd`, `prompt_template`,
  `start_sets_in_progress`, `start_assigns_me`, `labels`,
  `default_mode`, `subissue_branch`, `background` (`auto` = herdr's `panel_bg`, `none`, or hex).
- *About* at the bottom: the version from `herdr-plugin.toml` (embedded at build)
  with the commit go stamped, and the repo link (`enter` opens, `y` copies).

## Actions and hotkeys

| action | what it does |
|---|---|
| `open` | popup with the issue list |
| `by-id` | straight to the ID/link input |
| `logout` | delete the saved keys (every workspace) |

Plugins don't bind keys themselves — the README provides ready `[[keys.command]]` blocks.

Not an action: `herdr-linear-kickoff demo` runs the popup on two made-up workspaces
(internal/demo) — no key, no herdr, a temp HOME, and a start that only reports its
steps. It's for trying the plugin and for the README images (`scripts/screens.sh`, VHS).

## Popup keys

`enter` — Start · `A` — start with another agent · `w` — only open/create the worktree (no agent, no Linear
changes; a new one still gets its packages) · `s` — change state ·
`o` — open in browser · `y` — copy id/link · `g` — its pull request · `n` — new issue ·
`tab`/`1-4` — modes · `p` — workspace, team or project ·
`/` or `:` — search palette · `,` — settings · `r` — refresh · `?` — all keys · `esc`/`q` — close.
The footer shows only the main keys for where the cursor is.

## Security

- The key never goes to logs, prompts or state.
- Issue text is untrusted data: never executed by a shell, escaped,
  and marked as issue content in the prompt.
- Network: only `api.linear.app` and `git fetch`.
