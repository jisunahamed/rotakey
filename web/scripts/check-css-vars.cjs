/** Every custom property a stylesheet reads must be one it can be sure of.
 *
 *  The plan asked for "every `var(--x)` is declared in `tokens.css`", which would
 *  have caught the `--main-pad-*` bug that broke the playground for a year. This
 *  is stronger, because a later bug got through that rule: `--mobile-header-height`
 *  *was* declared, on `.app-shell`, and read by a drawer in `primitives.css` — a
 *  primitive that renders wherever it is put, including the kitchen sink, where
 *  there is no shell. Off the shell the `var()` resolved to nothing, `inset` was
 *  thrown away whole for being invalid at computed-value time, and a `fixed`
 *  drawer sat nine thousand pixels down the page.
 *
 *  So the rule is about scope, not spelling: a sheet may read what `tokens.css`
 *  declares globally, or what it declares itself. Anything else is a sheet
 *  depending on an ancestor it cannot see, which is exactly the failure above. */

const path = require("path");
const { parseStylesheet, allSheets, read } = require("./css-parse.cjs");

const root = path.resolve(__dirname, "..");
const TOKENS = "src/tokens.css";

/** Properties written onto a node by JavaScript. Each one is a track list a
 *  component takes as a prop, so the stylesheet genuinely cannot declare it. */
const FROM_SCRIPT = new Set(["--ui-table-columns", "--ui-workbench-columns"]);

const declaredIn = new Map();
const usedIn = [];

for (const file of allSheets(root)) {
  const rules = parseStylesheet(read(root, file), file);
  const declared = new Set();
  for (const rule of rules) {
    for (const decl of rule.decls) {
      if (decl.prop.startsWith("--")) declared.add(decl.prop);
      for (const match of decl.value.matchAll(/var\(\s*(--[A-Za-z0-9_-]+)/g)) {
        usedIn.push({ file, line: rule.line, name: match[1], selector: rule.selector });
      }
    }
  }
  declaredIn.set(file, declared);
}

const globals = declaredIn.get(TOKENS);
if (!globals) throw new Error(`${TOKENS} not found`);

const problems = [];
for (const use of usedIn) {
  if (FROM_SCRIPT.has(use.name)) continue;
  if (globals.has(use.name)) continue;
  if (declaredIn.get(use.file).has(use.name)) continue;
  problems.push(use);
}

/** Tokens nothing reads. Not a failure — a token can be declared ahead of the
 *  page that will use it — but a growing list here is a stylesheet keeping
 *  vocabulary it has stopped speaking. */
const read_ = new Set(usedIn.map((u) => u.name));
const unused = [...globals].filter((name) => !read_.has(name));

for (const problem of problems) {
  console.error(`${problem.file}:${problem.line}  ${problem.name} is not declared in tokens.css or in this file`);
  console.error(`  used by: ${problem.selector}`);
}
if (unused.length) console.log(`tokens.css declares ${unused.length} unread: ${unused.join(" ")}`);

if (problems.length) {
  console.error(`\n${problems.length} out-of-scope custom ${problems.length === 1 ? "property" : "properties"}.`);
  process.exit(1);
}
console.log(`css-vars: ${usedIn.length} reads across ${declaredIn.size} sheets, all in scope.`);
