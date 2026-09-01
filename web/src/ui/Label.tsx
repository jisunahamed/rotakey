import type { ReactNode } from "react";

/** The console's micro-label: uppercase, wide-tracked, set in the label face.
 *  It names the kind of thing underneath it — "PROVIDER", "TOKENS IN", "STATUS".
 *
 *  It existed as `.eyebrow` and as twenty hand-written copies of the same five
 *  declarations, two of which zeroed out the tracking that makes the face legible
 *  at 11px, so the label above one panel was a different object from the label
 *  above the next.
 *
 *  The accent is opt-in and rare. Every one of those copies was iris-coloured,
 *  which is why the console reads busier than it is: when a dozen static captions
 *  are the same colour as the links, the colour has stopped meaning "you can act
 *  here" and started meaning nothing. Reach for `tone="accent"` only when the
 *  label belongs to something the operator is being asked to act on. */
export function Label({
  children,
  tone = "muted",
  as: Element = "span",
  className = "",
  id
}: {
  children: ReactNode;
  tone?: "muted" | "accent";
  /** `dt` inside a description list, `legend` inside a fieldset, `p` when the
   *  label is a caption of its own rather than part of a line. */
  as?: "span" | "p" | "dt" | "legend";
  className?: string;
  id?: string;
}) {
  return (
    <Element className={`ui-label ui-label--${tone} ${className}`.trim()} id={id}>
      {children}
    </Element>
  );
}
