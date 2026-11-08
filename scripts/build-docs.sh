#!/bin/sh
# Builds the PDF versions of the guides (docs/user/*.md and docs/pro/*.md -> the same name .pdf).
# Needs Node.js and Chrome, Chromium or Edge (set CHROME_BIN if it is not found).
# Markdown is the source of truth; commit the PDFs together with the Markdown.
# A PDF carries a creation time, so rebuild only what changed to keep diffs small:
#   scripts/build-docs.sh docs/user/02-author.md
set -e
cd "$(dirname "$0")/../tools/docs"
npm ci --silent
node build.mjs "$@"
