# The README screenshots: vhs docs/tapes/screens.tape (or scripts/screens.sh).
Output "/tmp/herdr-linear-kickoff-screens.gif"
Set FontSize 15
Set Width 1280
Set Height 800
Source docs/tapes/setup.tape

# The list and the preview: sub-issues, an existing worktree, comments.
Sleep 4s
Screenshot docs/img/list.png

# Search: the loaded tabs at once, then Linear's own search.
Type "/"
Sleep 1s
Type "checkout"
Sleep 5s
Screenshot docs/img/search.png
Escape
Sleep 1s

# Where to start an issue that has no worktree yet.
Down 5
Sleep 1s
Enter
Sleep 3s
Screenshot docs/img/repo.png
Escape
Sleep 1s

# Start with another agent, this time only.
Type "A"
Sleep 3s
Screenshot docs/img/agent.png
Escape
Sleep 1s

# Workspaces, teams and projects.
Type "p"
Sleep 3s
Screenshot docs/img/workspaces.png
Escape
Sleep 1s

# A new issue.
Type "n"
Sleep 2s
Type "Add rate limit headers to the API responses"
Tab
Type "So clients can back off before they hit 429."
Tab
Tab
Tab
Sleep 2s
Enter
Sleep 1s
Screenshot docs/img/new.png
