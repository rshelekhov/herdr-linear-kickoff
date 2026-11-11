#!/bin/sh
# Makes the README images (docs/img) from the demo mode with VHS
# (https://github.com/charmbracelet/vhs): made-up issues, nothing of yours.
# Run it after changing how the popup looks. Takes a few minutes.
set -eu
cd "$(dirname "$0")/.."
command -v vhs >/dev/null || { echo "screens: needs vhs (brew install vhs)" >&2; exit 1; }
go build -o bin/herdr-linear-kickoff .
vhs docs/tapes/screens.tape
vhs docs/tapes/demo.tape
rm -f /tmp/herdr-linear-kickoff-screens.gif
