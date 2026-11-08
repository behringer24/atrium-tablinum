// Builds a PDF next to every Markdown file given on the command line (default: docs/user/*.md and docs/pro/*.md).
// Markdown is the source; the PDF is generated from it with marked and the headless print
// function of Chrome or Edge. Run it with scripts/build-docs.sh.
import { Marked } from "marked";
import { spawnSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const repoURL = "https://github.com/behringer24/atrium-tablinum/blob/main/";

// Branding at the start of every PDF: the Atrium icon (the same file the server serves) and the
// logo of behringer24. Both are embedded, so the PDF needs nothing from the network.
const dataURI = (file, mime) => `data:${mime};base64,${readFileSync(file).toString("base64")}`;
const atriumIcon = dataURI(path.join(root, "internal", "mcp", "assets", "icon.svg"), "image/svg+xml");
const authorLogo = dataURI(path.join(import.meta.dirname, "assets", "behringer24-logo.png"), "image/png");
const branding = `<header class="brand">
  <div class="brand-side"><img src="${atriumIcon}" alt="Atrium"><div><b>Atrium Tablinum</b><span>Shared memory for people and AI assistants</span></div></div>
  <div class="brand-side right"><div><b>behringer24</b><span>Andreas Behringer</span></div><img src="${authorLogo}" alt="behringer24"></div>
</header>`;

function browser() {
  const candidates = [
    process.env.CHROME_BIN,
    "C:/Program Files/Google/Chrome/Application/chrome.exe",
    "C:/Program Files (x86)/Google/Chrome/Application/chrome.exe",
    "C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
    "C:/Program Files/Microsoft/Edge/Application/msedge.exe",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
    "/usr/bin/microsoft-edge",
  ].filter(Boolean);
  const found = candidates.find((c) => existsSync(c));
  if (!found) {
    console.error("No Chrome, Chromium or Edge found. Install one or set CHROME_BIN.");
    process.exit(1);
  }
  return found;
}

const slug = (text) =>
  text.toLowerCase().replace(/<[^>]+>/g, "").replace(/&[a-z]+;/g, "").replace(/[^\p{L}\p{N}\s-]/gu, "").trim().replace(/\s+/g, "-");

function rewrite(href, mdDir) {
  if (/^([a-z]+:|#)/i.test(href)) return href;
  const [file, hash = ""] = href.split("#");
  if (!file.endsWith(".md")) return href;
  const target = path.resolve(mdDir, file);
  // Guides that sit next to each other link to each other's PDF; everything else points to the repository.
  if (path.dirname(target) === mdDir) return file.replace(/\.md$/, ".pdf") + (hash ? "#" + hash : "");
  return repoURL + path.relative(root, target).split(path.sep).join("/") + (hash ? "#" + hash : "");
}

const css = `
@page { size: A4; margin: 26mm 18mm 22mm 18mm;
  @top-left { content: "Atrium Tablinum"; vertical-align: bottom; text-align: left; padding: 0 0 7mm 25px;
    font: 600 9pt system-ui, sans-serif; color: #1b3358;
    background: url("${atriumIcon}") no-repeat left bottom 6.3mm / 19px 19px, linear-gradient(#c8ccd3, #c8ccd3) no-repeat left bottom 4mm / 100% 0.6pt; }
  @top-center { content: ""; background: linear-gradient(#c8ccd3, #c8ccd3) no-repeat left bottom 4mm / 100% 0.6pt; }
  @top-right { content: "behringer24 – Andreas Behringer"; vertical-align: bottom; text-align: right; padding: 0 25px 7mm 0;
    font: 600 9pt system-ui, sans-serif; color: #1b3358;
    background: url("${authorLogo}") no-repeat right bottom 6.3mm / 19px 19px, linear-gradient(#c8ccd3, #c8ccd3) no-repeat left bottom 4mm / 100% 0.6pt; }
  @bottom-left { content: "Atrium Tablinum · behringer24 – Andreas Behringer"; font: 8.5pt system-ui, sans-serif; color: #666; }
  @bottom-right { content: counter(page) " / " counter(pages); font: 9pt system-ui, sans-serif; color: #666; } }
@page :first { @top-left { content: none; background: none; } @top-center { content: none; background: none; } @top-right { content: none; background: none; } }
.brand { display: flex; justify-content: space-between; align-items: center; padding-bottom: 10px; margin-bottom: 18px; border-bottom: 1px solid #c8ccd3; }
.brand-side { display: flex; align-items: center; gap: 10px; }
.brand-side.right { text-align: right; }
.brand img { height: 42px; width: 42px; border-radius: 8px; }
.brand b { display: block; font-size: 12pt; color: #1b3358; }
.brand span { display: block; font-size: 8.5pt; color: #555; }
html { font: 10.5pt/1.5 "Segoe UI", system-ui, -apple-system, sans-serif; color: #1c1c1c; }
body { max-width: 100%; }
h1 { font-size: 24pt; margin: 0 0 .3em; padding-bottom: .2em; border-bottom: 2px solid #1b3358; color: #1b3358; }
h2 { font-size: 16pt; margin: 1.6em 0 .4em; color: #1b3358; break-after: avoid; }
h3 { font-size: 12.5pt; margin: 1.3em 0 .3em; break-after: avoid; }
h4 { font-size: 11pt; margin: 1.1em 0 .2em; break-after: avoid; }
p, li { orphans: 3; widows: 3; }
code { font: 9pt Consolas, "Courier New", monospace; background: #f1f3f6; padding: 1px 4px; border-radius: 3px; }
pre { background: #f1f3f6; padding: 8px 10px; border-radius: 4px; overflow-wrap: anywhere; white-space: pre-wrap; break-inside: avoid; }
pre code { background: none; padding: 0; }
table { border-collapse: collapse; width: 100%; margin: .8em 0; font-size: 9.5pt; }
th, td { border: 1px solid #c8ccd3; padding: 4px 7px; text-align: left; vertical-align: top; }
th { background: #e8ecf3; }
tr { break-inside: avoid; }
blockquote { margin: .8em 0; padding: .1em 1em; border-left: 4px solid #f2b441; background: #fff8e8; }
a { color: #1b3358; }
hr { border: 0; border-top: 1px solid #c8ccd3; margin: 1.5em 0; }
`;

function render(mdFile) {
  const mdDir = path.dirname(mdFile);
  const text = readFileSync(mdFile, "utf8");
  const m = new Marked({ gfm: true });
  m.use({
    walkTokens(token) {
      if (token.type === "link") token.href = rewrite(token.href, mdDir);
    },
    renderer: {
      heading({ tokens, depth }) {
        const inner = this.parser.parseInline(tokens);
        return `<h${depth} id="${slug(inner)}">${inner}</h${depth}>\n`;
      },
    },
  });
  const title = (text.match(/^#\s+(.+)$/m) || [, path.basename(mdFile)])[1];
  return `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${title}</title><style>${css}</style></head><body>${branding}${m.parse(text)}</body></html>`;
}

const args = process.argv.slice(2);
const files = (args.length ? args : ["user", "pro"].flatMap((d) => readdirSync(path.join(root, "docs", d)).filter((f) => f.endsWith(".md")).map((f) => path.join("docs", d, f))))
  .map((f) => path.resolve(root, f));
const exe = browser();
const tmp = mkdtempSync(path.join(tmpdir(), "atrium-docs-"));
let failed = false;
try {
  for (const md of files) {
    const html = path.join(tmp, path.basename(md, ".md") + ".html");
    const pdf = md.replace(/\.md$/, ".pdf");
    writeFileSync(html, render(md));
    if (process.env.KEEP_HTML) writeFileSync(md.replace(/.md$/, ".html"), render(md)); // for a visual check
    const r = spawnSync(exe, [
      "--headless=new", "--disable-gpu", "--no-sandbox", "--no-pdf-header-footer",
      `--user-data-dir=${path.join(tmp, "profile")}`, `--print-to-pdf=${pdf}`, pathToFileURL(html).href,
    ], { stdio: "pipe" });
    if (r.status !== 0 || !existsSync(pdf)) {
      failed = true;
      console.error(`FAILED ${path.relative(root, md)}\n${r.stderr?.toString() ?? ""}`);
    } else {
      console.log(`${path.relative(root, pdf)}`);
    }
  }
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
process.exit(failed ? 1 : 0);
