#!/bin/sh
# Builds the PDF versions of the user guides (docs/user/*.md -> docs/user/*.pdf).
# Needs Node.js and Chrome, Chromium or Edge (set CHROME_BIN if it is not found).
# Markdown is the source of truth; commit the PDFs together with the Markdown.
set -e
cd "$(dirname "$0")/../tools/docs"
npm ci --silent
node build.mjs "$@"
