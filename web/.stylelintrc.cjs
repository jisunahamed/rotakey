/** What stylelint is here for, and what it is deliberately not here for.
 *
 *  No shared config. `stylelint-config-standard` is three dozen stylistic rules
 *  — quote style, notation preferences, keyword casing — and turning it on would
 *  bury the four findings that matter under a thousand it decided for us. Every
 *  rule below is here because this codebase has already been bitten by the thing
 *  it catches, or because it catches a fault that cannot be seen by reading. */

module.exports = {
  rules: {
    /* The reason stylelint is installed at all. `styles.css` and `console.css`
       declared 27 of the same selectors, resolved only by which file `main.tsx`
       imported second — so the console's real appearance was an emergent property
       of an import order nobody had written down. This rule is per-stylesheet, so
       it is run over the concatenated bundle; see scripts/lint-css.cjs. */
    "no-duplicate-selectors": true,

    /* The `font` shorthand silently resets `font-variant-numeric`, which turns
       off the tabular figures every number in this console is set in. It has
       already cost two debugging sessions — once on the playground's two <pre>
       blocks, which it also quietly set in the display face.

       `font: inherit` is exempt, and that is not a loophole: a CSS-wide keyword
       applies to every longhand the shorthand owns, including the ones a real
       value silently resets, so `font: inherit` on a form control inherits the
       figures rather than dropping them. It is also the only way to stop a
       browser styling <button> and <select> in its own font. */
    "declaration-property-value-disallowed-list": {
      font: [/^(?!(inherit|initial|unset|revert|revert-layer)$)/]
    },

    /* A block declaring the same property twice is either a fallback stack or a
       mistake, and only the fallback has different values in a row. */
    "declaration-block-no-duplicate-properties": [true, { ignore: ["consecutive-duplicates-with-different-values"] }],
    "declaration-block-no-duplicate-custom-properties": true,

    /* `padding: var(--space-4); padding-top: 0` is fine; `padding-top: 0;
       padding: var(--space-4)` throws the first line away. The two read almost
       identically in a diff. */
    "declaration-block-no-shorthand-property-overrides": true,

    /* `calc(100dvh -var(--shell-chrome))` is not a syntax error — it is a valid
       calc that computes something else, or an invalid one that takes the whole
       declaration with it. Either way nothing says so at build time. */
    "function-calc-no-unspaced-operator": true,

    /* An empty block is the residue of a rule whose last declaration was deleted,
       and it reads as though something is still styled there. */
    "block-no-empty": true,

    /* `// comment` is not CSS. It does not comment anything out; it silently
       breaks the declaration after it. */
    "no-invalid-double-slash-comments": true
  }
};
