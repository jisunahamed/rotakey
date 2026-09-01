/** Proof that an extraction moved code rather than rewrote it.
 *
 *  `App.tsx` leaves this project a few hundred lines at a time, and a commit that
 *  says "pure moves" is asking a reviewer to skip reading the diff — five hundred
 *  deleted lines and five hundred added ones, in a different order, in files that
 *  did not exist before. That is exactly the diff a small edit hides in, and
 *  `tsc` cannot tell the difference: a function that quietly lost a guard clause
 *  typechecks perfectly.
 *
 *  So the claim is checked instead of asserted. Every top-level declaration in the
 *  named directory is matched by name against the one it replaced in the file it
 *  came from, read back out of git, and compared twice:
 *
 *  1. The code, ignoring the `export` an extraction has to add. This must be
 *     identical line for line. Anything else is not a move.
 *
 *  2. The comment written above it. A docblock explains why the code is the way it
 *     is, and paraphrasing one while moving it is how a reason for a decision
 *     turns into a description of the code. A declaration that had no comment and
 *     gained one is reported separately rather than failed — that is the one
 *     addition an extraction can honestly make, because a helper that used to sit
 *     beside its only caller now has to introduce itself.
 *
 *  Usage — the ref is the commit the code moved *out of*:
 *
 *      node scripts/check-moved.cjs <ref> [source] [directory]
 *      node scripts/check-moved.cjs eaff6f2c web/src/App.tsx src/lib
 */

const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");

const ref = process.argv[2] || "HEAD";
const source = process.argv[3] || "web/src/App.tsx";
const directory = process.argv[4] || "src/lib";

const before = execFileSync("git", ["show", `${ref}:${source}`], {
  cwd: path.resolve(__dirname, "../.."),
  encoding: "utf8",
  maxBuffer: 64 * 1024 * 1024,
});

const isComment = (line) => /^\s*(\/\*|\*|\/\/)/.test(line);

/** Every top-level `function`, `const` and `let`, taken with the comment block
 *  written directly above it — a docblock belongs to the declaration it
 *  introduces, not to the one it happens to follow.
 *
 *  `type`, `class` and `export default` end a block without starting a comparable
 *  one. Without them the last declaration before a type alias swallows it, and the
 *  comparison reports a difference that is really a missing boundary. */
function declarations(text) {
  const lines = text.split("\n");
  const named = /^(?:export\s+)?(?:function|const|let)\s+([A-Za-z_$][\w$]*)/;
  const other = /^(?:export\s+default\b|(?:export\s+)?(?:type|class|interface|enum)\s)/;

  const starts = [];
  for (let index = 0; index < lines.length; index += 1) {
    const match = named.exec(lines[index]);
    if (!match && !other.test(lines[index])) continue;
    let top = index;
    while (top > 0 && isComment(lines[top - 1])) top -= 1;
    starts.push({ name: match ? match[1] : null, top });
  }

  const found = new Map();
  for (let n = 0; n < starts.length; n += 1) {
    if (!starts[n].name) continue;
    const end = n + 1 < starts.length ? starts[n + 1].top : lines.length;
    found.set(starts[n].name, lines.slice(starts[n].top, end).join("\n").trim());
  }
  return found;
}

/** The declaration without its leading comment and without the `export` an
 *  extraction adds — everything a reader would call "the code". */
const code = (text) => text.split("\n").filter((line) => !isComment(line)).join("\n").replace(/^export\s+/gm, "").trim();
const lead = (text) => text.split("\n").filter(isComment).join("\n").trim();

const old = declarations(before);
const problems = [];
const introduced = [];
let checked = 0;
// Counted rather than derived from `problems.length`, which also holds the
// was/now lines under each one — a two-line diff is one declaration, not two.
let failed = 0;

const root = path.resolve(__dirname, "..", directory);
for (const file of fs.readdirSync(root).sort()) {
  if (!file.endsWith(".ts") && !file.endsWith(".tsx")) continue;
  for (const [name, now] of declarations(fs.readFileSync(path.join(root, file), "utf8"))) {
    const was = old.get(name);
    // A declaration written fresh in the new file is not a move and has nothing to
    // compare against. It is named rather than ignored, because "pure moves" and a
    // new function in the same commit is a claim the commit message has to answer.
    if (was === undefined) { failed += 1; problems.push(`  ${file}: ${name} — no declaration by this name in ${ref}:${source}`); continue; }
    checked += 1;

    if (code(now) !== code(was)) {
      failed += 1;
      problems.push(`  ${file}: ${name} — the code changed`);
      const a = code(was).split("\n");
      const b = code(now).split("\n");
      for (let line = 0; line < Math.max(a.length, b.length); line += 1) {
        if (a[line] !== b[line]) problems.push(`    was: ${a[line] ?? "(nothing)"}\n    now: ${b[line] ?? "(nothing)"}`);
      }
      continue;
    }

    if (lead(now) === lead(was)) continue;
    if (lead(was) === "") { introduced.push(`  ${file}: ${name}`); continue; }
    failed += 1;
    problems.push(`  ${file}: ${name} — its comment was rewritten rather than moved`);
    problems.push(lead(was).replace(/^/gm, "    was: "));
    problems.push(lead(now).replace(/^/gm, "    now: "));
  }
}

const say = failed ? (line) => console.error(line) : (line) => console.log(line);
say(`${checked} declarations in ${directory} compared against ${ref}:${source}.`);

if (failed) {
  console.error(problems.join("\n"));
  console.error(`\nmoved: ${failed} ${failed === 1 ? "declaration is" : "declarations are"} not a move. Either put them back or stop calling this commit one.`);
  process.exit(1);
}

console.log("Every line of code identical, and every comment that existed moved verbatim.");
if (introduced.length) {
  console.log(`${introduced.length} of them had no comment and gained one:`);
  console.log(introduced.join("\n"));
}
