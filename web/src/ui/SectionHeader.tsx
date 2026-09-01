import type { ReactNode } from "react";

/** The line that starts a panel, a page or a section of a form: what this is, one
 *  fact about it, and whatever can be done to it.
 *
 *  Ten variants of this shape existed. The differences between them were not
 *  design decisions — they were the order in which the pages happened to be
 *  written.
 *
 *  `level` is required, and it is not a size. It is where the heading sits in the
 *  document, which is how a screen reader user moves around a page without
 *  reading it: a heading that skips a level breaks that. A panel directly under
 *  the page's `<h1>` takes 2; a section inside that panel takes 3. Size follows
 *  from the level rather than being chosen, so the two cannot disagree.
 *
 *  There is no eyebrow slot. Every one of the console's thirteen eyebrows named
 *  the category of a page whose heading was directly underneath it — "Public
 *  contract" over "Connect", "Model lab" over "Playground" — which is two lines
 *  saying one thing, and the invented half was the one an operator could not look
 *  up. Whatever is genuinely worth putting above a heading is a fact, and a fact
 *  goes in `meta`. */
export function SectionHeader({
  title,
  level,
  description,
  meta,
  actions,
  id
}: {
  title: string;
  level: 1 | 2 | 3 | 4;
  /** What this section is for, in one sentence. Delete it if it only restates the
   *  title in longer words. */
  description?: string;
  /** A fact about what is in here, set as a figure: "4 providers · 12 keys · 2
   *  need attention". Never a description — the heading already did that. */
  meta?: ReactNode;
  actions?: ReactNode;
  /** For `aria-labelledby` on the region this header opens. */
  id?: string;
}) {
  const Heading = `h${level}` as const;
  return (
    <div className={`ui-section-header ui-section-header--${level}`}>
      <div className="ui-section-header__text">
        <Heading className="ui-section-header__title" id={id}>{title}</Heading>
        {description && <p className="ui-section-header__description">{description}</p>}
      </div>
      {meta && <span className="ui-section-header__meta">{meta}</span>}
      {actions && <div className="ui-section-header__actions">{actions}</div>}
    </div>
  );
}
