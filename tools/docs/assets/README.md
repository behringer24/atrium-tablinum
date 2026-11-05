# Branding assets

- `behringer24-logo.png`: the logo of behringer24 (Andreas Behringer), taken from www.behringer24.de and used with the owner's permission to brand the PDF versions of the user guides. It is a brand asset of its owner and is **not** covered by the Elastic License 2.0 of this repository. Do not reuse it elsewhere without permission.
- The Atrium icon in the PDFs is `internal/mcp/assets/icon.svg`, the same file the server serves as its favicon.

Every PDF carries the branding the same way: a header block with the Atrium icon and the behringer24 logo on the first page, a running header with both on every following page, and a footer with the names and the page number. The layout is in `tools/docs/build.mjs`.
