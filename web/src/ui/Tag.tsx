import type { ReactNode } from "react";
import type { StateTone } from "./state";

/** A short word or figure in a hairline box: a status code, a protocol name, the
 *  key that is primary, a capability a route was proved to have.
 *
 *  Four separate implementations drew this, and the useful difference between them
 *  was one thing — whether the contents are language or a figure. A figure takes
 *  the data face and tabular numerals so a column of them lines up and does not
 *  jump sideways when 200 becomes 429; a word takes the body face.
 *
 *  The tint is deliberately weak. The text itself carries the state colour, and a
 *  solid fill at this size fights every row around it. */
export function Tag({
  children,
  tone = "neutral",
  figure = false,
  title
}: {
  children: ReactNode;
  /** `accent` means the operator can act on what this names; the four state tones
   *  mean the thing is in that state. `neutral` is a fact with no verdict attached
   *  — a protocol name, a tokenizer, a region. */
  tone?: StateTone | "accent" | "neutral";
  /** Set for anything read as a number: a status code, a count, a latency. */
  figure?: boolean;
  title?: string;
}) {
  return (
    <span className={`ui-tag ui-tag--${tone} ${figure ? "ui-tag--figure" : ""}`.trim()} title={title}>
      {children}
    </span>
  );
}
