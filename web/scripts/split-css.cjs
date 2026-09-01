/** The one-shot that cut `styles.css` and `console.css` into `base`, `shell` and
 *  `pages`. Kept because the split it performed is the thing the next person will
 *  want to argue with, and an argument needs the rule that was applied.
 *
 *  It moves text, never rewrites it: every block is emitted byte for byte with the
 *  comment above it, in the order it was read. That is what lets
 *  `check-css-order.cjs` prove the result is the same cascade — a script that
 *  reformatted as it went would produce a diff nobody could check.
 *
 *  Usage: node scripts/split-css.cjs [--write] */

const fs = require("fs");
const path = require("path");

const root = path.resolve(__dirname, "..");
const write = process.argv.includes("--write");

/** The frame the console is drawn in: the rail, the pane, the overlays that
 *  belong to the window rather than to a page, and the two screens that exist
 *  before there is a shell at all. Everything here would still be needed if every
 *  page were deleted. */
const SHELL = [
  "app-shell", "sidebar", "wordmark", "nav-group", "nav-item", "rail-search", "account",
  "workspace", "main-pane", "page-header", "page-actions", "page-skeleton",
  "mobile-header", "mobile-scrim", "drawer-scrim",
  "palette", "sheet", "toast", "confirm-dialog", "confirm-layer", "confirm-scrim",
  "crash-panel", "shortcut",
  "auth-shell", "auth-panel", "auth-form", "login-panel", "boot-line", "boot-sequence", "setup-rail"
];

/** Not components — the document itself, and the two utilities that describe a
 *  relationship to the reader rather than a thing on the page. */
const BASE = ["sr-only", "skip-link", "muted"];

/** `.wordmark` is in SHELL and `.wordmark--auth` has to go with it, so prefixes
 *  match at a name boundary: `--`, `__`, or the end of the name. Plain
 *  `startsWith` would drag `.account__menu` along with `.account` correctly and
 *  `.notice-line` along with `.notice` wrongly. */
const inSet = (set, name) => set.some((p) => name === p || name.startsWith(`${p}--`) || name.startsWith(`${p}__`) || name.startsWith(`${p}-`) && set.includes(name.slice(0, p.length)));

/** Blocks whose selector list spans two destinations. There are four, they are
 *  all a shared rule that happens to name one element type, and splitting a rule
 *  in two to file the halves separately would turn one fact into two that can
 *  drift. Each is placed whole, with the reason. */
const PLACED = [
  // Typographic defaults for the monospace faces; the one class is a `<pre>` in
  // all but name.
  [/^code,\s*pre,/, "base"],
  // The action strip beside a page title, and the same strip everywhere else.
  [/^\.page-header__actions,/, "shell"],
  // The label above a control, whether the control is wrapped in a fieldset.
  [/^\.field > span,/, "pages"],
  // Page and panel titles set in the display face — one rule, three depths.
  [/^\.page-header h1,/, "shell"]
];

const destOf = (selector) => {
  const match = selector.match(/^[^\s>+~]*?\.([-A-Za-z0-9_]+)/);
  // No class anywhere in the leading compound: an element, `*`, or the global
  // focus rule. All of those describe the document, not a component.
  if (!match) return "base";
  const name = match[1];
  if (inSet(BASE, name)) return "base";
  if (inSet(SHELL, name)) return "shell";
  return "pages";
};

/* ---- a text-level reader, because the split has to preserve bytes ---- */

const skipWs = (text, i) => {
  while (i < text.length && /\s/.test(text[i])) i++;
  return i;
};

/** Top-level blocks of a stylesheet, each carrying the span of its own text and
 *  of the comment written above it. */
function blocks(text, from = 0, to = text.length) {
  const out = [];
  let i = skipWs(text, from);
  let lead = i;
  while (i < to) {
    if (text[i] === "/" && text[i + 1] === "*") {
      const close = text.indexOf("*/", i + 2) + 2;
      const after = skipWs(text, close);
      // A blank line between a comment and the next block means the comment is
      // about the file or the section, not about the block.
      if (text.slice(close, after).split("\n").length > 2) {
        out.push({ kind: "comment", lead, start: lead, end: close, text: text.slice(lead, close) });
        lead = after;
      }
      i = after;
      continue;
    }
    const brace = text.indexOf("{", i);
    if (brace < 0 || brace >= to) break;
    let depth = 0;
    let j = brace;
    for (; j < to; j++) {
      if (text[j] === "/" && text[j + 1] === "*") j = text.indexOf("*/", j + 2) + 1;
      else if (text[j] === "{") depth++;
      else if (text[j] === "}" && --depth === 0) break;
    }
    const end = j + 1;
    const prelude = text.slice(i, brace).trim();
    out.push({
      kind: prelude.startsWith("@media") ? "media" : prelude.startsWith("@keyframes") ? "keyframes" : "rule",
      lead,
      start: i,
      end,
      prelude,
      body: text.slice(brace + 1, end - 1),
      text: text.slice(lead, end)
    });
    i = skipWs(text, end);
    lead = i;
  }
  return out;
}

/* ---- classify ---- */

const files = ["src/styles.css", "src/console.css"];
const out = { base: [], shell: [], pages: [] };
const split = [];
const animations = new Map();

for (const file of files) {
  const text = fs.readFileSync(path.join(root, file), "utf8");
  for (const block of blocks(text)) {
    if (block.kind === "comment") continue; // file and section headers are rewritten
    if (block.kind === "rule") {
      // A comment can sit between two selectors in a list, so it comes out before
      // the list is split — otherwise the comment reads as a selector of its own.
      const list = block.prelude.replace(/\/\*[\s\S]*?\*\//g, " ").split(/,\s*/).map((s) => s.trim()).filter(Boolean);
      const placed = PLACED.find(([pattern]) => pattern.test(list.join(", ")));
      const dests = new Set(list.map(destOf));
      if (dests.size > 1 && !placed) split.push(`${file} +${list.join(", ").slice(0, 70)} -> ${[...dests].join(" & ")}`);
      const dest = placed ? placed[1] : destOf(list[0]);
      out[dest].push(block.text);
      for (const [, name] of block.body.matchAll(/animation:[^;]*?\b([a-z][\w-]*)\b(?=[^;]*(?:s|ms)\b)/g)) {
        animations.set(name, dest);
      }
      for (const [, name] of block.body.matchAll(/animation-name:\s*([\w-]+)/g)) animations.set(name, dest);
      continue;
    }
    if (block.kind === "keyframes") {
      out[animations.get(block.prelude.replace(/^@keyframes\s+/, "")) || "pages"].push(block.text);
      continue;
    }
    // A media block holds rules of every kind, so it is cut apart and rebuilt
    // once per destination that had something in it. The children keep their
    // order and their indentation; only the wrapper is new.
    const inner = { base: [], shell: [], pages: [] };
    for (const child of blocks(block.body)) {
      if (child.kind === "comment") continue;
      const list = child.prelude.replace(/\/\*[\s\S]*?\*\//g, " ").split(/,\s*/).map((s) => s.trim()).filter(Boolean);
      const placed = PLACED.find(([pattern]) => pattern.test(list.join(", ")));
      inner[child.kind === "rule" ? (placed ? placed[1] : destOf(list[0])) : "pages"].push(child.text);
    }
    for (const dest of ["base", "shell", "pages"]) {
      if (inner[dest].length) out[dest].push(`${block.prelude} {\n${inner[dest].join("\n\n")}\n}`);
    }
  }
}

const HEADERS = {
  base: `/* The document, before any of it is Rotakey.
 *
 * The reset, the element defaults, the one focus ring, and the two utilities that
 * describe a relationship to the reader rather than a thing on the page. Nothing
 * here names a component, and nothing here should: a rule that knows about a
 * provider or a route belongs in one of the files after this one.
 */`,
  shell: `/* The frame the console is drawn in.
 *
 * The rail, the pane, the page header, and the overlays that belong to the window
 * rather than to any page — the palette, the sheet, the toasts, the confirm
 * dialog, the crash panel. The sign-in and first-run screens are here too: they
 * are the shell before there is a shell.
 *
 * Everything in this file would still be needed if every page were deleted, which
 * is the test for whether a rule belongs here.
 */`,
  pages: `/* Everything that belongs to one page, and the components that predate the kit.
 *
 * This file is the residue of the split and it is meant to shrink. As each page
 * moves onto \`ui/\`, its rules leave here and do not come back; the shared
 * components near the top — the button, the field, the row, the status dot — go
 * the same way as the primitives that replace them land.
 *
 * Read a rule here as "not yet a primitive" rather than as a decision.
 */`
};

for (const dest of ["base", "shell", "pages"]) {
  const body = `${HEADERS[dest]}\n\n${out[dest].join("\n\n")}\n`;
  const at = path.join(root, `src/${dest}.css`);
  if (write) fs.writeFileSync(at, body);
  console.log(`${dest}.css  ${body.split("\n").length} lines, ${out[dest].length} blocks`);
}
for (const line of split) console.log(`  mixed: ${line}`);
if (!write) console.log("\nDry run. Pass --write to emit.");
