import type { ReactNode } from "react";

/** What stands where the rows would have been. Eight of these existed and half of
 *  them said only that there was nothing there, which the blank space had already
 *  made clear.
 *
 *  The contract is that every empty state answers three questions in order: what
 *  is missing, why it is missing, and what to do about it. A filtered list that
 *  found nothing is a different sentence from a list that has never had a row in
 *  it, and both are different from a list whose request failed — so `title` states
 *  which of the three this is, `description` gives the reason, and `action` is the
 *  way out. An empty state with no action is a dead end; if there is genuinely
 *  nothing to do, the description has to say so in words.
 *
 *  `level` is required for the same reason it is on `SectionHeader`: this heading
 *  stands in for whatever should have been at this spot, so it has to sit at that
 *  spot's depth in the document.
 *
 *  `size="pane"` is for an empty inside a column or an inspector, where the full
 *  height would push the panel taller than the list it replaced. */
export function Empty({
  title,
  description,
  action,
  level,
  size = "page"
}: {
  title: string;
  description: string;
  action?: ReactNode;
  level: 2 | 3 | 4;
  size?: "page" | "pane";
}) {
  const Heading = `h${level}` as const;
  return (
    <div className={`ui-empty ui-empty--${size}`}>
      <Heading className="ui-empty__title">{title}</Heading>
      <p className="ui-empty__description">{description}</p>
      {action && <div className="ui-empty__action">{action}</div>}
    </div>
  );
}
