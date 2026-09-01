/** Proof that moving CSS around did not change what any element gets.
 *
 *  The split this exists for cannot be verified by looking at it. There is no
 *  backend in this environment, so most of the console's pages never render here;
 *  six thousand lines are about to be redistributed between three files and
 *  twenty-nine selectors written in both of them are about to lose the import
 *  order that was deciding between them, and "it still looks right on the two
 *  screens I can reach" is not a check.
 *
 *  What can be checked is the cascade itself, because order is precedence in this
 *  project — there is no `@layer` — and the whole of what a stylesheet does is
 *  decided by which declaration comes last among the ones that tie.
 *
 *  Three things are checked against the same bundle read back out of git:
 *
 *  1. For every selector, media context and property, the value that wins is the
 *     value that won before. This is what a fold-in has to preserve, and it is
 *     the one a careless merge breaks silently. It must come out empty.
 *
 *  2. No two rules that could argue swapped places *inside one sheet*. Two rules
 *     argue when they set the same property at the same specificity — then, and
 *     only then, the later one wins purely by being later, so moving either past
 *     the other changes the answer. Rules keep the order they were read in when
 *     a file is split, so a tie that flips without leaving its file means
 *     something reordered rules that argue. This must come out empty too.
 *
 *     Across sheets the same flip is the import order in `main.tsx` doing what it
 *     was changed to do. Those are counted and named by the boundary they cross
 *     rather than treated as faults; `--verbose` prints the pairs. A count that
 *     moves between runs is the thing to look at.
 *
 *  3. The premise the sheet order rests on: no element wears classes from two of
 *     the console's vocabularies. `ui-` is the kit, `rotor` the rotor, `is-`
 *     shared state, everything else the pages and the shell. While that holds,
 *     putting the kit above the pages cannot change what any element gets, which
 *     is the whole reason the kit is allowed to move. It is measured on every run
 *     from the `className` expressions rather than assumed, and the run says so
 *     out loud when it stops being true.
 *
 *     Most pairs cannot argue at all — `.nav-item` and `.toast-dock` both set
 *     `gap` at one class of specificity, but nothing is ever both — so they go to
 *     `couldShareAnElement` first. It answers "no" only on evidence and defaults
 *     to "yes", so a pair it clears is cleared soundly and a pair it cannot decide
 *     still lands in front of a person. How many it cleared is printed too: a
 *     filter that silently ate its input would read exactly like a clean run.
 *
 *  Usage: node scripts/check-css-order.cjs [git-ref] [--verbose]   (default HEAD) */

const { execFileSync } = require("child_process");
const fs = require("fs");
const path = require("path");
const ts = require("typescript");
const { parseStylesheet, specificity, read } = require("./css-parse.cjs");

const root = path.resolve(__dirname, "..");
const repo = path.resolve(root, "..");
const ref = process.argv[2] || "HEAD";
/** Joins the parts of a key. A control character rather than a punctuation mark:
 *  selectors hold commas, spaces, brackets and pipes, and a key that can be
 *  ambiguously split is a key that silently compares the wrong two things. */
const SEP = String.fromCharCode(31);

function fromGit(file) {
  return execFileSync("git", ["show", `${ref}:web/${file}`], { cwd: repo, encoding: "utf8", maxBuffer: 1 << 26 });
}

const norm = (s) => s.replace(/\s+/g, " ").trim();
const context = (rule) => rule.media.map(norm).join(" and ");
/** A rule's name across the two bundles. The split moves rules and merges rules
 *  whose selectors already match; it never rewrites a selector, so the selector
 *  list plus the media condition identifies one. */
const identity = (rule) => `${rule.selectors.map(norm).sort().join(", ")}${SEP}${context(rule)}`;

/** The bundle as the browser sees it: every sheet `main.tsx` imports, in that
 *  order, flattened to one list of rules. Keyframe steps are dropped — they are
 *  not part of the cascade and can never tie with a rule. */
function bundle(loadMain, load) {
  const sheets = [...loadMain().matchAll(/^import\s+"(\.[^"]+\.css)"/gm)].map((m) => m[1].replace(/^\.\//, "src/"));
  if (!sheets.length) throw new Error("main.tsx imports no local stylesheets");
  const rules = [];
  for (const sheet of sheets) {
    for (const rule of parseStylesheet(load(sheet), sheet)) if (!rule.keyframe) rules.push(rule);
  }
  return rules;
}

const before = bundle(() => fromGit("src/main.tsx"), fromGit);
const after = bundle(
  () => read(root, "src/main.tsx"),
  (f) => read(root, f)
);

/** Claim 1 — the last value for each (selector, context, property). */
function winners(rules) {
  const map = new Map();
  rules.forEach((rule) => {
    for (const selector of rule.selectors) {
      for (const decl of rule.decls) {
        const key = [norm(selector), context(rule), decl.prop].join(SEP);
        map.set(key, { value: norm(decl.value) + (decl.important ? " !important" : ""), rule });
      }
    }
  });
  return map;
}

const oldWin = winners(before);
const newWin = winners(after);
const claim1 = [];
/** "`.a .b` @(max-width: 900px)" — a rule named the way it reads in the file. */
const label = (id) => {
  const [selector, ctx] = id.split(SEP);
  return `${selector}${ctx ? ` @${ctx}` : ""}`;
};
const show = (key) => {
  const parts = key.split(SEP);
  return `${label(parts.slice(0, 2).join(SEP))} { ${parts[2]} }`;
};

for (const [key, was] of oldWin) {
  const now = newWin.get(key);
  if (!now) claim1.push(`lost   ${show(key)}: ${was.value}   (was ${was.rule.file}:${was.rule.line})`);
  else if (now.value !== was.value)
    claim1.push(`change ${show(key)}: ${was.value} -> ${now.value}   (${now.rule.file}:${now.rule.line})`);
}
for (const [key, now] of newWin) {
  if (!oldWin.has(key)) claim1.push(`new    ${show(key)}: ${now.value}   (${now.rule.file}:${now.rule.line})`);
}

/** Which of the console's vocabularies a single class name belongs to.
 *
 *  Three sheets draw components — `ui/primitives.css`, `rotor.css`, and the pages
 *  — and each names its classes with a prefix nothing else spells. `is-` is the
 *  console's shared state marker and belongs to none of them: it is only ever
 *  written in a compound beside a class that does have a home. */
const vocabularyOf = (name) =>
  name.startsWith("ui-") ? "kit" : name.startsWith("rotor") ? "rotor" : name.startsWith("is-") ? "state" : "legacy";

/** The strings a `className` expression can evaluate to.
 *
 *  Most of this console's class names are built rather than written, and reading
 *  the string literals out of the tree one at a time gets them wrong in both
 *  directions. In ``  `rotor__segment--${key.unknown ? "unknown" : key.status}` ``
 *  the literal `"unknown"` is a *value* that lands after `rotor__segment--`, not a
 *  class of its own, and `rotor__segment--` is not a class either until it has
 *  been joined. Swept up separately they read as two classes, neither of which
 *  exists, one of which looks like it comes from another vocabulary.
 *
 *  So the expression is evaluated instead. Both branches of a conditional are
 *  taken, which makes the answer wider than any one render — the direction that
 *  keeps it sound, since this is only ever used to rule a collision *out*.
 *  Anything unreadable evaluates to a single wildcard character, and a word
 *  carrying one is a name nothing here can know.
 *
 *  The one expression worth chasing further is `className` itself. A wrapper that
 *  splices its caller's classes in beside its own — `<button className={`button
 *  ${className}`}>` — otherwise reads as "this element could be wearing anything",
 *  which is true and useless: it makes `.button` unusable as evidence, and with it
 *  every pair that only `.button` could have settled. But the callers are all in
 *  this repo, so `className` resolves to the union of every string any caller
 *  passes to any component. A union across components rather than per component,
 *  which is wider than the truth and therefore still only rules collisions out. */
const WILD = String.fromCharCode(0);
/** Null during the first sweep, which is the one that collects it. */
let spliced = null;

function strings(node) {
  if (!node) return [WILD];
  if (ts.isJsxExpression(node) || ts.isParenthesizedExpression(node)) return strings(node.expression);
  if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return [node.text];
  const named = ts.isIdentifier(node) ? node.text : ts.isPropertyAccessExpression(node) ? node.name.getText() : null;
  if (named === "className") return spliced ?? [WILD];
  // The kit ends five of these in `.trim()`, to swallow the space left when the
  // caller passed nothing. Leading and trailing space is not a class either way,
  // but a call this reader does not recognise is a wildcard, and one wildcard in
  // the union is enough to make every name in it unusable.
  if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.getText() === "trim")
    return strings(node.expression.expression).map((s) => s.trim());
  // A conditional with four branches on each side of a template is 16 strings and
  // still worth having; the cap is only there so a pathological one degrades to
  // "unknown" instead of running out of memory.
  const join = (a, b) =>
    a.length * b.length > 4096 ? [WILD] : [...new Set(a.flatMap((x) => b.map((y) => x + y)))];
  if (ts.isTemplateExpression(node)) {
    let out = [node.head.text];
    for (const span of node.templateSpans) out = join(join(out, strings(span.expression)), [span.literal.text]);
    return out;
  }
  if (ts.isConditionalExpression(node)) return [...strings(node.whenTrue), ...strings(node.whenFalse)];
  if (ts.isBinaryExpression(node)) {
    const kind = node.operatorToken.kind;
    // `cond && "x"` contributes "x" or nothing at all.
    if (kind === ts.SyntaxKind.AmpersandAmpersandToken) return ["", ...strings(node.right)];
    if (kind === ts.SyntaxKind.BarBarToken || kind === ts.SyntaxKind.QuestionQuestionToken)
      return [...strings(node.left), ...strings(node.right)];
    if (kind === ts.SyntaxKind.PlusToken) return join(strings(node.left), strings(node.right));
  }
  return [WILD];
}

/** Every element the console writes a `className` on: the names it is certainly
 *  wearing, the prefixes of the names it might also be wearing, and the tag it is
 *  written on. */
function elementClassSets() {
  const files = [];
  (function collect(dir) {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name);
      if (entry.isDirectory()) collect(full);
      else if (/\.tsx?$/.test(entry.name)) files.push(full);
    }
  })(path.join(root, "src"));

  const parsed = files.map((file) => [
    file,
    ts.createSourceFile(file, fs.readFileSync(file, "utf8"), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX)
  ]);
  const sets = [];
  const open = new Set();
  const mixed = [];
  const kit = new Set(
    fs.readdirSync(path.join(root, "src/ui")).filter((f) => f.endsWith(".tsx")).map((f) => f.replace(/\.tsx$/, ""))
  );
  const isClassName = (node) =>
    (ts.isJsxAttribute(node) && node.name.getText() === "className") ||
    (ts.isPropertyAssignment(node) && node.name.getText().replace(/["']/g, "") === "className");

  // First sweep: what any caller hands to any component. A `className` that reads
  // `className` is not a caller handing something over — it is a wrapper passing
  // on what it was already handed, and counting it would feed the union back into
  // itself as a wildcard. `<Element>` in `Surface.tsx` and `Label.tsx` is a
  // capitalised local holding a tag name, and this is what tells it apart from a
  // component being called.
  const reads = (node) =>
    (ts.isIdentifier(node) || ts.isPropertyAccessExpression(node)
      ? (ts.isIdentifier(node) ? node.text : node.name.getText()) === "className"
      : false) || ts.forEachChild(node, reads) === true;
  const handed = new Set([""]);
  for (const [, source] of parsed) {
    (function visit(node) {
      const tag = node.parent?.parent?.tagName?.getText?.();
      if (isClassName(node) && tag && /^[A-Z]/.test(tag) && !reads(node.initializer)) {
        for (const text of strings(node.initializer)) handed.add(text);
      }
      ts.forEachChild(node, visit);
    })(source);
  }
  spliced = [...handed];

  for (const [file, source] of parsed) {
    const visit = (node) => {
      if (isClassName(node)) {
        // A `className` that reads a variable is only partly written here, and
        // there are two shapes of that. `` `status-dot--${state}` `` is a name
        // whose *stem* is known: whatever it turns out to be, it starts with
        // `status-dot--`, so the element's company is still enumerable and the
        // stem is kept as a pattern. `` `ui-row ${className}` `` is a wrapper
        // splicing in whatever its caller passed, and that could be any name at
        // all — so every name at such a site is marked open, and an open name is
        // never used to rule a collision out.
        const names = new Set();
        const patterns = [];
        let borrowed = false;
        for (const text of strings(node.initializer)) {
          for (const word of text.split(/\s+/)) {
            if (!word) continue;
            if (!word.includes(WILD)) names.add(word);
            else if (word.startsWith(WILD)) borrowed = true;
            else patterns.push(word.slice(0, word.indexOf(WILD)));
          }
        }
        // The same attribute read without the caller union — what this element is
        // dressed in by the file it is written in. The mixed check below is about
        // what the console *writes*, and folding in every class anyone hands to
        // any component would have it report a mix that no element has.
        const union = spliced;
        spliced = null;
        const own = new Set(strings(node.initializer).flatMap((t) => t.split(/\s+/)).filter((w) => w && !w.includes(WILD)));
        spliced = union;
        // A component's own tag says nothing about the element that lands in the
        // DOM, and neither does a `className` in a plain object; both read as
        // "could be anything", which rules nothing out.
        const written = node.parent?.parent?.tagName?.getText?.();
        const tag = written && /^[a-z]/.test(written) ? written : null;
        sets.push({ known: names, patterns, tag });
        if (borrowed) for (const name of names) open.add(name);

        // `main.tsx` now orders the kit and the rotor above the pages, so a page
        // can customise a primitive by passing a class into it. Nothing does that
        // yet — no element in the console wears two of the three vocabularies —
        // and that is what makes every cross-vocabulary pair below decidable, so
        // the claim is measured here on every run rather than assumed.
        //
        // Two arms, because there are two ways to mix: writing both vocabularies
        // on one element, and handing a primitive a class from outside its own.
        // What neither arm sees is a *page* wrapper splicing a `ui-` class it was
        // passed onto a legacy element — that would need the call graph. Today no
        // file outside `src/ui/` writes a `ui-` name at all, which is the check
        // one line below.
        if (new Set([...own].map(vocabularyOf).filter((v) => v !== "state")).size > 1) {
          mixed.push(`${path.relative(root, file)}: ${[...own].join(" ")}`);
        }
        if (!file.includes(`${path.sep}ui${path.sep}`) && [...own].some((n) => vocabularyOf(n) === "kit")) {
          mixed.push(`${path.relative(root, file)}: writes a kit class outside src/ui — ${[...own].join(" ")}`);
        }
        if (kit.has(written) && [...own].some((n) => !["kit", "state"].includes(vocabularyOf(n)))) {
          mixed.push(`${path.relative(root, file)}: <${written} className=${node.initializer?.getText?.() ?? "?"}>`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  return { sets, open, vocabulariesApart: mixed.length === 0, mixed };
}

const { sets: elements, open: borrowedNames, vocabulariesApart, mixed } = elementClassSets();
if (mixed.length) {
  console.error("Two vocabularies now share an element, so the order between their sheets is no longer free:");
  for (const line of [...new Set(mixed)].slice(0, 10)) console.error(`  ${line}`);
  console.error("");
}

/** Which vocabulary a compound selector picks out, or `"either"` when it names no
 *  class that says — a bare tag, a lone `.is-open`, or a compound that somehow
 *  spans two, all of which decide nothing and have to fall through. */
const vocabulary = (compound) => {
  const kinds = new Set(compound.classes.map(vocabularyOf).filter((v) => v !== "state"));
  return kinds.size === 1 ? [...kinds][0] : "either";
};

/** The rightmost compound of a selector — the part that describes the element the
 *  rule actually paints, as opposed to its ancestors. Splitting on combinators at
 *  bracket depth zero, because `:not(a > b)` holds one. */
function subject(selector) {
  let depth = 0;
  let start = 0;
  for (let i = 0; i < selector.length; i++) {
    const c = selector[i];
    if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    else if (depth === 0 && /[\s>+~]/.test(c)) start = i + 1;
  }
  return selector.slice(start);
}

/** What a flat compound demands: the classes it requires, the tags it will accept
 *  (`null` for any), and which box it paints. Parenthesised pseudo-classes are
 *  emptied — the classes inside `:not()` are forbidden rather than required, and
 *  reading them as requirements would rule out collisions that are real. */
function plain(compound) {
  const bare = compound.replace(/\(([^()]*)\)/g, "()");
  const pseudo = bare.match(/::[a-z-]+|:(?:before|after|first-line|first-letter)\b/);
  const tag = bare.match(/^[a-zA-Z][\w-]*/);
  return {
    classes: [...bare.matchAll(/\.(-?[A-Za-z_][A-Za-z0-9_-]*)/g)].map((m) => m[1]),
    tags: tag ? new Set([tag[0].toLowerCase()]) : null,
    box: pseudo ? pseudo[0].replace(/^:{1,2}/, "") : ""
  };
}

/** The same, but reading `:is()` and `:where()` rather than emptying them.
 *
 *  They are a list of alternatives, so what they *require* is what every branch
 *  requires and what they *allow* is what any branch allows. This matters here:
 *  the kit writes `.ui-field :where(input, select, textarea)`, which is a rule
 *  about three tags and no classes. Emptied, it reads as a rule about nothing at
 *  all, and every span on the page looks like it might be arguing with it. */
function demands(compound) {
  const lists = [...compound.matchAll(/:(?:is|where)\(([^()]*)\)/g)].map((m) => m[1]);
  const out = plain(compound.replace(/:(?:is|where)\([^()]*\)/g, ""));
  for (const list of lists) {
    const branches = list.split(/,\s*/).map((s) => s.trim()).filter(Boolean).map(plain);
    if (!branches.length) continue;
    // One untagged branch and the list accepts any tag.
    const tags = branches.every((b) => b.tags) ? new Set(branches.flatMap((b) => [...b.tags])) : null;
    if (tags) out.tags = out.tags ? new Set([...out.tags].filter((t) => tags.has(t))) : tags;
    for (const name of branches[0].classes) {
      if (branches.every((b) => b.classes.includes(name))) out.classes.push(name);
    }
    const boxes = new Set(branches.map((b) => b.box));
    if (!out.box && boxes.size === 1) out.box = [...boxes][0];
  }
  return out;
}

/** The band of viewport widths a media context is live in. Any other feature it
 *  tests only narrows that further, so two rules whose bands do not overlap can
 *  never both apply whatever else their queries say. */
function widths(context) {
  let lo = 0;
  let hi = Infinity;
  for (const [, feature, value] of context.matchAll(/\((min|max)-width:\s*(\d+)px\)/g)) {
    if (feature === "min") lo = Math.max(lo, Number(value));
    else hi = Math.min(hi, Number(value));
  }
  return [lo, hi];
}

/** Whether one element could match both rules at once. "No" is a claim and needs
 *  grounds; everything else is "yes". */
function couldShareAnElement(idA, idB) {
  const [loA, hiA] = widths(idA.split(SEP)[1] ?? "");
  const [loB, hiB] = widths(idB.split(SEP)[1] ?? "");
  if (loA > hiB || loB > hiA) return false;

  const compounds = (id) => id.split(SEP)[0].split(/,\s*/).map((s) => demands(subject(s)));
  for (const a of compounds(idA)) {
    for (const b of compounds(idB)) {
      // ::before and the element itself are two different boxes; neither rule can
      // reach the other's, whatever the order.
      if (a.box !== b.box) continue;
      // The tags both rules will accept. Empty means they disagree about what the
      // element even is, and no element is a `<td>` and an `<input>` at once.
      const tags = a.tags && b.tags ? new Set([...a.tags].filter((t) => b.tags.has(t))) : a.tags || b.tags;
      if (tags && !tags.size) continue;
      // Only while the scan above finds the vocabularies still unmixed.
      if (vocabulariesApart && vocabulary(a) !== vocabulary(b) && vocabulary(a) !== "either" && vocabulary(b) !== "either") continue;
      const both = [...new Set([...a.classes, ...b.classes])];
      // Neither rule asks for a class, so the class sets have nothing to say and
      // the tags above were the only ground available.
      if (!both.length) return true;
      // A name written where a caller's classes are also spliced in is a name
      // whose company nothing here can enumerate. Every other name's wearers are
      // exactly the sites below — including a name like `status-dot--healthy`
      // that is never written whole, because the site that builds it kept the
      // stem it was built from.
      if (both.some((name) => borrowedNames.has(name))) return true;
      const fits = (element) =>
        (!tags || !element.tag || tags.has(element.tag)) &&
        both.every((name) => element.known.has(name) || element.patterns.some((stem) => name.startsWith(stem)));
      if (elements.some(fits)) return true;
    }
  }
  return false;
}

/** Claim 2 — for every property and specificity, which of two rules declares it
 *  last. That is the only thing order decides, so it is the only thing to hold
 *  fixed. The *last* index is what matters, not the first: a selector declared in
 *  two files wins from its second appearance, and folding the second into the
 *  first is exactly the move that can carry it back past a rule it used to beat. */
function tieOrder(rules) {
  const buckets = new Map();
  rules.forEach((rule, index) => {
    const specs = new Set(rule.selectors.map(specificity));
    for (const decl of rule.decls) {
      for (const spec of specs) {
        const key = `${decl.prop}${SEP}${spec}${SEP}${decl.important ? "!" : ""}`;
        if (!buckets.has(key)) buckets.set(key, new Map());
        buckets.get(key).set(identity(rule), { index, value: norm(decl.value) });
      }
    }
  });
  const order = new Map();
  for (const [key, last] of buckets) {
    const prop = key.split(SEP)[0];
    const names = [...last.keys()].sort();
    for (let a = 0; a < names.length; a++) {
      for (let b = a + 1; b < names.length; b++) {
        // Two rules that ask for the same thing are not arguing, so which of them
        // is asked last cannot matter. Half the pairs in a bundle this size are
        // one shared value written in two places.
        if (last.get(names[a]).value === last.get(names[b]).value) continue;
        // JSON rather than a joined string: an identity already ends in a
        // separator when its rule has no media query, so any separator-based key
        // would split in the wrong place and name the wrong two rules.
        const pair = JSON.stringify([names[a], names[b]]);
        if (!order.has(pair)) order.set(pair, new Map());
        order.get(pair).set(prop, last.get(names[a]).index < last.get(names[b]).index);
      }
    }
  }
  return order;
}

const oldOrder = tieOrder(before);
const newOrder = tieOrder(after);
/** Which sheet each rule ended up in, so a swap can be named by the boundary it
 *  crosses rather than only by the two rules. Rules keep their order *within* a
 *  sheet — the splitter emits blocks in the order it read them — so every swap is
 *  a consequence of one sheet moving past another, and there are far fewer sheet
 *  pairs than rule pairs. That is the difference between a list to read and a list
 *  to reason about. */
const home = new Map(after.map((rule) => [identity(rule), rule.file.replace(/^src\//, "")]));
const claim2 = new Map();
let apart = 0;
let swaps = 0;

for (const [pair, was] of oldOrder) {
  const now = newOrder.get(pair);
  if (!now) continue; // one side was merged away — claim 1 owns that
  const flipped = [...was].filter(([prop, first]) => now.has(prop) && now.get(prop) !== first).map(([prop]) => prop);
  if (!flipped.length) continue;
  const [a, b] = JSON.parse(pair);
  if (!couldShareAnElement(a, b)) {
    apart++;
    continue;
  }
  swaps++;
  const [from, to] = [home.get(a), home.get(b)].sort();
  const crossing = from === to ? from : `${from}  ->  ${to}`;
  if (!claim2.has(crossing)) claim2.set(crossing, []);
  claim2.get(crossing).push(`  ${label(a)}  <->  ${label(b)}   over: ${flipped.join(", ")}`);
}

/* ---- the report, then the verdict ----
 *
 * A swap between two sheets is the import order in `main.tsx` doing what it was
 * changed to do, and that change is deliberate and stated there. A swap *within*
 * one sheet is not: rules keep the order they were read in, so a tie that flips
 * without leaving its file means something reordered rules that argue. Only the
 * second is a failure, and the pairs behind it are always printed; the first is
 * reported by boundary and by count, with `--verbose` for the pairs, because a
 * count that moves between runs is the thing worth looking at.
 *
 * The whole report prints either way. A run that finds differences is exactly
 * when the reordering evidence is most worth reading: it is the difference
 * between a few deliberate edits sitting on an otherwise intact cascade and the
 * cascade coming apart. Only the last line and the exit code depend on what was
 * found. */
const inside = [...claim2].filter(([crossing]) => !crossing.includes("  ->  "));
const across = [...claim2].filter(([crossing]) => crossing.includes("  ->  "));
const failed = claim1.length + inside.reduce((n, [, lines]) => n + lines.length, 0);
const say = failed ? (line) => console.error(line) : (line) => console.log(line);

for (const line of claim1) say(line);
if (claim1.length) say("");
say(`${after.length} rules, ${newWin.size} winning declarations, ${apart} reordered pairs proven never to meet on one element.`);
for (const [crossing, lines] of inside) {
  say(`  ${lines.length} reordered inside ${crossing} — rules that argue changed places without leaving their file`);
  for (const line of lines.sort()) say(line);
}
for (const [crossing, lines] of across.sort((x, y) => y[1].length - x[1].length)) {
  say(`  ${lines.length} ties now read the other way across ${crossing}`);
  if (process.argv.includes("--verbose")) for (const line of lines.sort()) say(line);
}

if (failed) {
  const swaps = failed - claim1.length;
  console.error(
    `\ncss-order: ${claim1.length} declaration ${claim1.length === 1 ? "difference" : "differences"} and ` +
      `${swaps} tie ${swaps === 1 ? "swap" : "swaps"} inside a sheet, against ${ref}. ` +
      `Every one of them has to be something somebody meant to do.`
  );
  process.exit(1);
}

console.log(`css-order: every winning value identical to ${ref}, and no tie moved inside a sheet.`);
