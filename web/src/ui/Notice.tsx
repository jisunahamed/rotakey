import type { ReactNode } from "react";

export type NoticeTone = "info" | "success" | "warning" | "danger";

/** A message about the thing on screen, in place rather than in a toast: why a
 *  list is empty of the rows the filter promised, what a save is about to
 *  overwrite, which setting made an option unavailable.
 *
 *  Seven variants of this existed. This one adds the two things they kept
 *  improvising: a heading, for a notice long enough that the first line should be
 *  scannable, and an action, so "Redis is unreachable" can carry the button that
 *  retries instead of describing where to find it.
 *
 *  A failure interrupts a screen reader and everything else waits its turn. A
 *  warning is the case worth being careful about: it is something to look at, not
 *  something that has stopped working, so it is announced politely with the rest.
 *  Getting that wrong in the other direction is worse than it sounds — a page that
 *  raises three assertive regions on load talks over itself and the operator hears
 *  none of them.
 *
 *  Every tone is legible without its colour. The hue is on the left bar and in the
 *  wash; the words carry the meaning, because one operator in twelve cannot tell
 *  the warning wash from the success one. */
export function Notice({
  tone = "info",
  title,
  children,
  action
}: {
  tone?: NoticeTone;
  title?: string;
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className={`ui-notice ui-notice--${tone}`} role={tone === "danger" ? "alert" : "status"}>
      <div className="ui-notice__text">
        {title && <strong className="ui-notice__title">{title}</strong>}
        <div className="ui-notice__body">{children}</div>
      </div>
      {action && <div className="ui-notice__action">{action}</div>}
    </div>
  );
}
