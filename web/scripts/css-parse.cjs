/** A small CSS reader, shared by the four checks in this directory.
 *
 *  It is not a general parser and does not try to be. The console's stylesheets
 *  use exactly two at-rules — `@media` and `@keyframes` — no nesting, no
 *  `@supports`, no `@import`. That is verifiable in one grep and it is what makes
 *  a hand-written reader honest here: it either understands every construct in
 *  the files it is pointed at, or it throws.
 *
 *  Everything downstream wants the same three things, so they are produced once:
 *  a flat list of rules with their media context, the custom properties each file
 *  declares and uses, and the class names each selector mentions. */

const fs = require("fs");
const path = require("path");

/** Advance past a comment or a run of whitespace; returns the new index. */
function skipTrivia(text, i) {
  for (;;) {
    while (i < text.length && /\s/.test(text[i])) i++;
    if (text[i] === "/" && text[i + 1] === "*") {
      const end = text.indexOf("*/", i + 2);
      if (end < 0) throw new Error("unterminated comment");
      i = end + 2;
      continue;
    }
    return i;
  }
}

/** Find the index just past the `}` closing the block that opens at `open`.
 *  Strings and comments are stepped over so a brace inside either is not counted. */
function endOfBlock(text, open) {
  let depth = 0;
  let i = open;
  while (i < text.length) {
    const c = text[i];
    if (c === "/" && text[i + 1] === "*") {
      const end = text.indexOf("*/", i + 2);
      i = end < 0 ? text.length : end + 2;
      continue;
    }
    if (c === '"' || c === "'") {
      i++;
      while (i < text.length && text[i] !== c) i += text[i] === "\\" ? 2 : 1;
      i++;
      continue;
    }
    if (c === "{") depth++;
    if (c === "}") {
      depth--;
      if (depth === 0) return i + 1;
    }
    i++;
  }
  throw new Error("unterminated block");
}

/** Split a declaration body on top-level semicolons. `var()`, `calc()` and
 *  `url()` all nest parentheses and `content` holds strings, so neither can be
 *  split on naively. */
function splitDeclarations(body) {
  const parts = [];
  let depth = 0;
  let start = 0;
  let i = 0;
  while (i < body.length) {
    const c = body[i];
    if (c === "/" && body[i + 1] === "*") {
      const end = body.indexOf("*/", i + 2);
      i = end < 0 ? body.length : end + 2;
      continue;
    }
    if (c === '"' || c === "'") {
      i++;
      while (i < body.length && body[i] !== c) i += body[i] === "\\" ? 2 : 1;
      i++;
      continue;
    }
    if (c === "(") depth++;
    if (c === ")") depth--;
    if (c === ";" && depth === 0) {
      parts.push(body.slice(start, i));
      start = i + 1;
    }
    i++;
  }
  parts.push(body.slice(start));
  return parts;
}

/** One declaration, with comments stripped and `!important` lifted out. */
function readDeclaration(chunk) {
  const clean = chunk.replace(/\/\*[\s\S]*?\*\//g, "").trim();
  if (!clean) return null;
  const colon = clean.indexOf(":");
  if (colon < 0) return null;
  const prop = clean.slice(0, colon).trim();
  let value = clean.slice(colon + 1).trim();
  const important = /!\s*important$/i.test(value);
  if (important) value = value.replace(/!\s*important$/i, "").trim();
  return { prop, value, important };
}

/** Split a selector list on top-level commas — `:is(a, b)` and `:where(a, b)`
 *  both hold commas that do not separate selectors. */
function splitSelectors(list) {
  const out = [];
  let depth = 0;
  let start = 0;
  for (let i = 0; i < list.length; i++) {
    const c = list[i];
    if (c === "(" || c === "[") depth++;
    if (c === ")" || c === "]") depth--;
    if (c === "," && depth === 0) {
      out.push(list.slice(start, i));
      start = i + 1;
    }
  }
  out.push(list.slice(start));
  return out.map((s) => s.replace(/\/\*[\s\S]*?\*\//g, "").trim()).filter(Boolean);
}

/** Specificity as the usual triple. Only ever compared for equality here, so the
 *  usual caveats about `:not()` and `:where()` matter less than they look —
 *  `:where()` contributing zero is the one that has to be right, because the
 *  console's global focus rule depends on it, and it is. */
function specificity(selector) {
  let s = selector;
  // `:where(...)` adds nothing at all; drop it whole.
  for (;;) {
    const next = s.replace(/:where\((?:[^()]|\([^()]*\))*\)/g, " ");
    if (next === s) break;
    s = next;
  }
  // `:is()`, `:not()` and `:has()` take the specificity of their strongest
  // argument. Close enough to unwrap them and count what is inside.
  s = s.replace(/:(?:is|not|has|matches|any)\(/g, " (");
  const ids = (s.match(/#[A-Za-z0-9_-]+/g) || []).length;
  const classes =
    (s.match(/\.[A-Za-z0-9_-]+/g) || []).length +
    (s.match(/\[[^\]]*\]/g) || []).length +
    (s.match(/:{1}(?![:])[A-Za-z-]+/g) || []).length;
  const elements =
    (s.replace(/\[[^\]]*\]/g, " ").replace(/::?[A-Za-z-]+/g, " ").match(/(^|[\s>+~(])([a-zA-Z][a-zA-Z0-9-]*)/g) || [])
      .length;
  return `${ids},${classes},${elements}`;
}

/** Every class name a selector mentions, including inside `:is()`/`:not()`. */
function classesIn(selector) {
  return (selector.match(/\.(-?[A-Za-z_][A-Za-z0-9_-]*)/g) || []).map((c) => c.slice(1));
}

/** Read one stylesheet into a flat list of rules in source order.
 *
 *  `@media` is flattened: its children become ordinary rules carrying the
 *  condition. That is what the cascade does with them anyway, and it means the
 *  checks downstream never have to walk a tree. */
function parseStylesheet(text, file) {
  const rules = [];
  const lineAt = (index) => text.slice(0, index).split("\n").length;

  const walk = (from, to, media) => {
    let i = skipTrivia(text, from);
    while (i < to) {
      const brace = text.indexOf("{", i);
      const semi = text.indexOf(";", i);
      if (brace < 0 || brace >= to) {
        if (text.slice(i, to).trim()) throw new Error(`${file}: trailing text at line ${lineAt(i)}`);
        return;
      }
      // A statement at-rule, e.g. `@charset "utf-8";`. None exist today, but
      // silently swallowing one as a selector would be worse than a throw.
      if (semi >= 0 && semi < brace) {
        const statement = text.slice(i, semi).trim();
        if (!statement.startsWith("@")) throw new Error(`${file}: stray ';' at line ${lineAt(i)}`);
        i = skipTrivia(text, semi + 1);
        continue;
      }
      const prelude = text.slice(i, brace).replace(/\/\*[\s\S]*?\*\//g, "").trim();
      const end = endOfBlock(text, brace);
      const body = text.slice(brace + 1, end - 1);
      const line = lineAt(i);

      if (prelude.startsWith("@media")) {
        walk(brace + 1, end - 1, [...media, prelude.slice(6).trim()]);
      } else if (prelude.startsWith("@keyframes")) {
        // Keyframe steps are not part of the cascade and never collide with a
        // rule, so they are kept only so their `var()` uses are still seen.
        walk(brace + 1, end - 1, [...media, prelude]);
      } else if (prelude.startsWith("@")) {
        throw new Error(`${file}: unhandled at-rule "${prelude.split(/\s/)[0]}" at line ${line}`);
      } else {
        const selectors = splitSelectors(prelude);
        const decls = splitDeclarations(body).map(readDeclaration).filter(Boolean);
        rules.push({
          file,
          line,
          media,
          keyframe: media.some((m) => m.startsWith("@keyframes")),
          selectors,
          selector: selectors.join(", "),
          decls
        });
      }
      i = skipTrivia(text, end);
    }
  };

  walk(0, text.length, []);
  return rules;
}

/** The stylesheets, in the order `main.tsx` imports them. Order is precedence in
 *  this project — there is no `@layer` — so every check that concatenates has to
 *  concatenate in exactly this order or it is checking a different cascade. */
function sheetOrder(root) {
  const main = fs.readFileSync(path.join(root, "src/main.tsx"), "utf8");
  const found = [];
  for (const match of main.matchAll(/^import\s+"(\.[^"]+\.css)"/gm)) {
    found.push(match[1].replace(/^\.\//, "src/"));
  }
  if (found.length === 0) throw new Error("main.tsx imports no local stylesheets");
  return found;
}

/** Every stylesheet under `src/`, whether or not the shell imports it — the
 *  kitchen sink's is loaded from a dev-only branch and still has to obey the
 *  same rules. */
function allSheets(root) {
  const out = [];
  const walk = (dir) => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) walk(full);
      else if (entry.name.endsWith(".css")) out.push(path.relative(root, full).split(path.sep).join("/"));
    }
  };
  walk(path.join(root, "src"));
  return out;
}

function read(root, relative) {
  return fs.readFileSync(path.join(root, relative), "utf8");
}

module.exports = { parseStylesheet, splitSelectors, specificity, classesIn, sheetOrder, allSheets, read };
