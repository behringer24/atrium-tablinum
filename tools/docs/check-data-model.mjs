// Checks docs/data-model.html against the migrations: tables, columns in order, column types,
// foreign keys with their ON DELETE action, explicitly created indexes and the migration number
// the page claims. Exits with 1 and names every difference. Needs only Node.js:
//   node tools/docs/check-data-model.mjs
import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..", "..");
const migDir = path.join(root, "internal", "db", "migrations");
const page = path.join(root, "docs", "data-model.html");

// The Up part of every migration in version order. Version 2 is the Go migration in db.go that
// creates `chunks` with the configured vector dimension.
const ups = readdirSync(migDir).filter(f => f.endsWith(".sql"))
  .map(f => [parseInt(f, 10), readFileSync(path.join(migDir, f), "utf8").split("-- +goose Down")[0]]);
const goSrc = readFileSync(path.join(root, "internal", "db", "db.go"), "utf8");
const goStart = goSrc.indexOf("CREATE TABLE chunks");
if (goStart < 0) throw new Error("internal/db/db.go: CREATE TABLE chunks not found");
ups.push([2, goSrc.slice(goStart, goSrc.indexOf("`, dim))", goStart)).replace("%d", "N")]);
ups.sort((a, b) => a[0] - b[0]);

// Splits at commas outside parentheses and quotes.
function splitTop(s) {
  const out = [];
  let depth = 0, cur = "", quoted = false;
  for (const ch of s) {
    if (ch === "'") quoted = !quoted;
    if (!quoted && ch === "(") depth++;
    if (!quoted && ch === ")") depth--;
    if (!quoted && depth === 0 && ch === ",") { out.push(cur.trim()); cur = ""; continue; }
    cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

// The text inside the first parenthesis of a statement.
function parens(s) {
  const start = s.indexOf("(");
  let depth = 0;
  for (let i = start; i < s.length; i++) {
    if (s[i] === "(") depth++;
    if (s[i] === ")" && --depth === 0) return s.slice(start + 1, i);
  }
  throw new Error(`unbalanced parentheses: ${s.slice(0, 60)}`);
}

function column(def) {
  const m = def.match(/^(\w+)\s+(\w+(?:\(\w+\))?(?:\[\])?)/);
  const ref = def.match(/REFERENCES\s+(\w+)\((\w+)\)(?:\s+ON DELETE\s+(SET NULL|CASCADE))?/i);
  return { n: m[1], t: m[2].toLowerCase(), fk: ref ? { t: ref[1], c: ref[2], del: ref[3] ? ref[3].toUpperCase() : "NO ACTION" } : null };
}

const schema = {}, indexes = {};
for (const [, up] of ups) {
  for (const raw of up.replace(/--[^\n]*/g, "").split(/;\s*\n/)) {
    const s = raw.trim();
    let m;
    if ((m = s.match(/^CREATE TABLE (\w+)/))) {
      schema[m[1]] = {};
      for (const def of splitTop(parens(s))) {
        if (/^(UNIQUE|PRIMARY|CHECK|CONSTRAINT|FOREIGN)\b/i.test(def)) continue;
        const c = column(def);
        schema[m[1]][c.n] = c;
      }
    } else if ((m = s.match(/^CREATE (?:UNIQUE )?INDEX (\w+)\s+ON (\w+)/))) {
      (indexes[m[2]] ||= []).push(m[1]);
    } else if ((m = s.match(/^ALTER TABLE (\w+)\s+([\s\S]*)$/))) {
      for (const part of splitTop(m[2])) {
        let a;
        if ((a = part.match(/^ADD COLUMN\s+([\s\S]*)$/i))) { const c = column(a[1]); schema[m[1]][c.n] = c; }
        else if ((a = part.match(/^DROP COLUMN\s+(\w+)/i))) delete schema[m[1]][a[1]];
        else if ((a = part.match(/^ADD CONSTRAINT \w+\s+FOREIGN KEY \((\w+)\) REFERENCES (\w+)\((\w+)\)(?:\s+ON DELETE\s+(SET NULL|CASCADE))?/i)))
          schema[m[1]][a[1]].fk = { t: a[2], c: a[3], del: a[4] ? a[4].toUpperCase() : "NO ACTION" };
      }
    }
  }
}

// The data block of the page is plain JavaScript between two markers.
const html = readFileSync(page, "utf8");
const data = html.slice(html.indexOf("/*DATA-START*/") + "/*DATA-START*/".length, html.indexOf("/*DATA-END*/"));
const { SCHEMA_VERSION, TABLES, AREAS, LAYOUT } = new Function(`${data}; return { SCHEMA_VERSION, TABLES, AREAS, LAYOUT };`)();

const problems = [];
const latest = Math.max(...ups.map(([v]) => v));
if (SCHEMA_VERSION !== latest) problems.push(`page describes migration ${SCHEMA_VERSION}, latest is ${latest}`);

const onPage = Object.fromEntries(TABLES.map(t => [t.name, t]));
for (const t of Object.keys(schema)) if (!onPage[t]) problems.push(`table missing on the page: ${t}`);
for (const t of Object.keys(onPage)) if (!schema[t]) problems.push(`table on the page but not in the migrations: ${t}`);

for (const [name, cols] of Object.entries(schema)) {
  const t = onPage[name];
  if (!t) continue;
  const want = Object.keys(cols), have = t.cols.map(c => c.n);
  if (want.join() !== have.join()) problems.push(`${name}: columns differ\n  migrations: ${want.join(", ")}\n  page:       ${have.join(", ")}`);
  for (const c of t.cols) {
    const m = cols[c.n];
    if (!m) continue;
    if (m.t !== c.t.toLowerCase()) problems.push(`${name}.${c.n}: type ${m.t}, page says ${c.t}`);
    const fk = x => x ? `${x.t}.${x.c} ON DELETE ${x.del}` : "no foreign key";
    if (fk(m.fk) !== fk(c.fk)) problems.push(`${name}.${c.n}: ${fk(m.fk)}, page says ${fk(c.fk)}`);
  }
  const wantIdx = (indexes[name] || []).sort().join(", "), haveIdx = t.idx.filter(i => !i.impl).map(i => i.n).sort().join(", ");
  if (wantIdx !== haveIdx) problems.push(`${name}: indexes [${wantIdx}], page says [${haveIdx}]`);
}

const placed = LAYOUT.flat().flatMap(a => a.sub.flat());
for (const t of TABLES) {
  const n = placed.filter(x => x === t.name).length;
  if (n !== 1) problems.push(`${t.name}: placed ${n} times in LAYOUT`);
  if (!AREAS.some(a => a.id === t.area)) problems.push(`${t.name}: unknown area ${t.area}`);
}

const nCols = Object.values(schema).reduce((s, c) => s + Object.keys(c).length, 0);
const nFks = Object.values(schema).reduce((s, c) => s + Object.values(c).filter(x => x.fk).length, 0);
console.log(`migrations 1-${latest}: ${Object.keys(schema).length} tables, ${nCols} columns, ${nFks} foreign keys, ${Object.values(indexes).flat().length} indexes`);
if (problems.length) {
  console.error(`docs/data-model.html is out of date:\n- ${problems.join("\n- ")}`);
  process.exit(1);
}
console.log("docs/data-model.html matches the migrations");
