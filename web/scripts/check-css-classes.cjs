/** Which class names in the CSS can actually be reached from the code.
 *
 *  A stylesheet has no compiler. A rule for a class nothing writes any more costs
 *  nothing to keep and everything to trust: the next person reads it as a fact
 *  about the console, and half the time it is a fact about a component deleted
 *  two years ago.
 *
 *  The reason this is a script and not a grep is the console's own idiom. Names
 *  are assembled — `status-dot--${state}`, `ui-menu__item--${tone}`, the whole
 *  `is-open` family appended in a ternary — and a sweep that only matched whole
 *  literals would call 42 live rules dead and invite someone to delete them. So
 *  the strings are read with TypeScript's own scanner rather than a regex, and a
 *  fragment sitting against an interpolation is kept as a prefix or a suffix
 *  instead of a name.
 *
 *  What it cannot know, it says. A class assembled entirely from a variable is
 *  unmatchable from the text, and the count of those is printed so the result is
 *  read for what it is rather than as a clean bill of health. */

const fs = require("fs");
const path = require("path");
const ts = require("typescript");
const { parseStylesheet, allSheets, read } = require("./css-parse.cjs");

const root = path.resolve(__dirname, "..");

/** Classes no source file names, and does not have to.
 *
 *  Keep this short and keep the reason attached to each one. An entry here is a
 *  rule the check cannot see, not a rule that has been excused. */
const REACHED_ELSEWHERE = new Map([
  // Written on <html> by the pre-paint script in public/theme.js, before React.
  ["theme-dark", "public/theme.js"],
  ["theme-light", "public/theme.js"]
]);

const sources = [];
(function collect(dir) {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) collect(full);
    else if (/\.tsx?$/.test(entry.name)) sources.push(full);
  }
})(path.join(root, "src"));

const declared = new Map();
for (const file of allSheets(root)) {
  for (const rule of parseStylesheet(read(root, file), file)) {
    for (const selector of rule.selectors) {
      for (const match of selector.matchAll(/\.(-?[A-Za-z_][A-Za-z0-9_-]*)/g)) {
        if (!declared.has(match[1])) declared.set(match[1], `${file}:${rule.line}`);
      }
    }
  }
}

const exact = new Set();
const prefixes = new Set();
const suffixes = new Set();
let dynamic = 0;

/** One literal chunk of a string, and whether an interpolation touches each end.
 *
 *  A chunk with an expression against its right edge ends in a prefix rather than
 *  a whole name — but only *rather than* in the sense of "as well as", because
 *  every interpolation in this console can also produce nothing: the shape is
 *  `${selected ? " is-selected" : ""}` far more often than it is `--${tone}`. So
 *  a fragment is recorded both ways and the name it is a fragment of stays
 *  reachable. */
function harvest(text, openLeft, openRight, found) {
  const words = text.split(/\s+/);
  words.forEach((word, index) => {
    if (!word) return;
    const leftOpen = openLeft && index === 0 && !/^\s/.test(text);
    const rightOpen = openRight && index === words.length - 1 && !/\s$/.test(text);
    if (leftOpen && rightOpen) found.dynamic++;
    else {
      if (rightOpen) prefixes.add(word);
      if (leftOpen) suffixes.add(word);
      exact.add(word);
      found.words.push(word);
    }
  });
  // A token that is nothing but the expression: `${a} ${b}`, or a chunk that ends
  // at a space. There are no literal characters to match against at all.
  if (openRight && (text === "" || /\s$/.test(text))) found.dynamic++;
}

for (const file of sources) {
  const source = ts.createSourceFile(file, fs.readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
  const visit = (node) => {
    const found = { words: [], dynamic: 0 };
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) {
      harvest(node.text, false, false, found);
    } else if (ts.isTemplateExpression(node)) {
      harvest(node.head.text, false, true, found);
      node.templateSpans.forEach((span) => harvest(span.literal.text, true, !ts.isTemplateTail(span.literal), found));
    }
    // Only templates that are recognisably about class names are counted as a
    // blind spot. Every other string in the file interpolates something too, and
    // a caveat that counts every sentence with a name in it says nothing.
    if (found.dynamic && found.words.some((word) => declared.has(word))) dynamic += found.dynamic;
    ts.forEachChild(node, visit);
  };
  visit(source);
}

const reachable = (name) =>
  exact.has(name) ||
  REACHED_ELSEWHERE.has(name) ||
  [...prefixes].some((p) => name.length > p.length && name.startsWith(p)) ||
  [...suffixes].some((s) => name.length > s.length && name.endsWith(s));

const dead = [...declared].filter(([name]) => !reachable(name));

for (const [name, where] of dead) console.error(`${where}  .${name} is never written by any source file`);
if (dynamic) console.log(`css-classes: ${dynamic} fully interpolated class names — those cannot be checked from the text.`);

if (dead.length) {
  console.error(`\n${dead.length} unreachable ${dead.length === 1 ? "class" : "classes"}.`);
  process.exit(1);
}
console.log(`css-classes: ${declared.size} classes declared, all reachable.`);
