/** stylelint over the whole bundle at once, plus the one rule stylelint cannot
 *  express.
 *
 *  Running stylelint per file would miss the finding it was installed for.
 *  `no-duplicate-selectors` works within a stylesheet, and the console's
 *  duplicates were *across* two of them — 27 selectors declared in both
 *  `styles.css` and `console.css`, the winner decided by import order alone. So
 *  the sheets are concatenated in exactly the order `main.tsx` imports them,
 *  linted as one document, and every warning's line number is mapped back to the
 *  file it came from. That is also the document the browser actually resolves,
 *  which makes it the honest thing to lint.
 *
 *  Usage: node scripts/lint-css.cjs */

const path = require("path");
const stylelint = require("stylelint");
const { parseStylesheet, sheetOrder, read, specificity } = require("./css-parse.cjs");

const root = path.resolve(__dirname, "..");
const config = require("../.stylelintrc.cjs");

/** Lengths that must be said in the console's own vocabulary.
 *
 *  Not a ban on `px` — the console is full of legitimate ones: hairlines, track
 *  lists, the floor a workbench pane needs before it stops being usable. It is a
 *  ban on the ones that have a name. Type comes from the nine-step scale and
 *  space from the twelve-step one, and both are already clean: there is not one
 *  raw `font-size` in the codebase and there is one raw `padding`. This keeps it
 *  that way, which is the only time a rule like this is cheap.
 *
 *  A pixel or less is allowed anywhere: `margin: -1px` pulling a border back over
 *  a hairline is not a spacing decision, it is the hairline. */
const SPELLED_OUT = /^(font-size|letter-spacing|gap|row-gap|column-gap|(padding|margin|scroll-padding|scroll-margin)(-(top|right|bottom|left|inline|block)(-(start|end))?)?)$/;

const sheets = sheetOrder(root);
let bundle = "";
const map = [];
for (const sheet of sheets) {
  let text = read(root, sheet);
  // Every sheet contributes whole lines, so the next one starts at column 1 and
  // the map stays a straight index. Without this a file with no final newline
  // shifts every location after it by one.
  if (!text.endsWith("\n")) text += "\n";
  const count = text.split("\n").length - 1;
  for (let line = 1; line <= count; line++) map.push({ sheet, line });
  bundle += text;
}
const where = (line) => {
  const at = map[line - 1];
  return at ? `${at.sheet}:${at.line}` : `bundle:${line}`;
};

const problems = [];

/** Declarations that cannot reach a single element, because every selector in
 *  their rule is redeclared later with the same property.
 *
 *  This is the rule `no-duplicate-selectors` is reaching for and cannot state.
 *  stylelint compares whole selector lists, so it sees `.sidebar` written in two
 *  files and misses `.a, .b { padding }` followed by `.a { padding }` and
 *  `.b { padding }` — the same waste, spelled differently. And where it does
 *  fire it names the rule rather than the declarations, so a duplicated selector
 *  whose two bodies declare different properties reads as a fault when it is
 *  composition.
 *
 *  Composition is the reason this is stated per declaration rather than per
 *  rule. `.limit-cell small, .limit-cell strong { font-family: var(--font-data) }`
 *  followed by `.limit-cell small { font-family: var(--font-label) }` is not a
 *  mistake: the group still dresses `strong`. It is only waste when *nothing* is
 *  left — when the later rules cover every selector the earlier one had. Eleven
 *  of this codebase's twenty-eight redeclarations are the legitimate shape, and
 *  a rule that could not tell them apart would be turned off within a week.
 *
 *  A later rule counts as covering an earlier one only at equal or greater
 *  specificity and in the same media context, which is the part that is
 *  decidable from the text. Whether two *different* selectors can match the same
 *  element is not, so this deliberately only follows a selector to itself. */
{
  // A control character, so a selector or a media query can never contain one and
  // two different keys can never collide by punctuation.
  const SEP = String.fromCharCode(31);
  const norm = (text) => text.replace(/\s+/g, " ").trim();
  const atLeast = (a, b) => a[0] !== b[0] ? a[0] > b[0] : a[1] !== b[1] ? a[1] > b[1] : a[2] >= b[2];

  const rules = [];
  for (const sheet of sheets) {
    for (const rule of parseStylesheet(read(root, sheet), sheet)) {
      if (!rule.keyframe) rules.push({ ...rule, order: rules.length });
    }
  }

  // Every place a given (selector, media context, property) is declared, in the
  // order the browser reads them.
  const declared = new Map();
  for (const rule of rules) {
    const context = rule.media.map(norm).join(" and ");
    for (const selector of rule.selectors) {
      const at = specificity(selector);
      for (const decl of rule.decls) {
        const key = `${norm(selector)}${SEP}${context}${SEP}${decl.prop}`;
        if (!declared.has(key)) declared.set(key, []);
        declared.get(key).push({ rule, decl, at });
      }
    }
  }

  for (const rule of rules) {
    const context = rule.media.map(norm).join(" and ");
    for (const decl of rule.decls) {
      const covered = rule.selectors.every((selector) => {
        const key = `${norm(selector)}${SEP}${context}${SEP}${decl.prop}`;
        const mine = specificity(selector);
        return declared.get(key).some(
          (other) =>
            other.rule.order > rule.order &&
            atLeast(other.at, mine) &&
            // `!important` is only beaten by another one.
            (other.decl.important || !decl.important)
        );
      });
      if (!covered) continue;
      problems.push(
        `${rule.file}:${rule.line}  ${rule.selector} { ${decl.prop} } — overridden for every element it matches`
      );
    }
  }
}

for (const sheet of sheets) {
  for (const rule of parseStylesheet(read(root, sheet), sheet)) {
    for (const decl of rule.decls) {
      if (decl.prop.startsWith("--") || !SPELLED_OUT.test(decl.prop)) continue;
      for (const match of decl.value.matchAll(/(?<![\w.#-])(-?\d*\.?\d+)px\b/g)) {
        if (Math.abs(Number(match[1])) <= 1) continue;
        problems.push(`${sheet}:${rule.line}  ${decl.prop}: ${decl.value}  — ${match[0]} has a name in tokens.css`);
      }
    }
  }
}

stylelint
  .lint({ code: bundle, config, codeFilename: path.join(root, "src/bundle.css") })
  .then((result) => {
    for (const warning of result.results[0]?.warnings ?? []) {
      // "first used at line 6460" is a line in the bundle, which is a document
      // nobody can open. Point at the file instead.
      const text = warning.text.replace(/line (\d+)/g, (_, line) => where(Number(line)));
      problems.push(`${where(warning.line)}  ${text}`);
    }
    for (const problem of problems.sort()) console.error(problem);
    if (problems.length) {
      console.error(`\n${problems.length} CSS ${problems.length === 1 ? "problem" : "problems"}.`);
      process.exit(1);
    }
    console.log(`lint-css: ${sheets.length} sheets, ${bundle.split("\n").length} lines, clean.`);
  })
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
