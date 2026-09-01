import type { CSSProperties, ReactNode, Ref } from "react";
import { X } from "lucide-react";

/** A list on the left and whatever is open on the right.
 *
 *  Four pages build this and all four build it differently. The differences are
 *  not choices: they are the order the pages were written in, and they are why
 *  the request log's inspector scrolls with the page while the models page's
 *  inspector scrolls inside itself.
 *
 *  Three things about it are hard and are therefore here rather than in a page.
 *  The frame is a fixed height so the two columns scroll independently, which is
 *  the whole reason to draw a list beside a panel instead of above it. Both
 *  columns take a zero minimum, because neither this frame nor the page scrolls
 *  sideways, so a column with a content-based floor does not widen the layout —
 *  it pushes the other column off the edge. And below 900px the right-hand column
 *  stops being a column and becomes a drawer over the list. */

/** The page-height frame the workbench sits in: a header of whatever size above,
 *  the workbench taking the rest, and nothing scrolling but the columns inside
 *  it. Separate from `Workbench` because the height belongs to the page — the
 *  workbench cannot know what is stacked above it. */
export function WorkbenchFrame({ children }: { children: ReactNode }) {
  return <div className="ui-workbench-frame">{children}</div>;
}

export function Workbench({
  list,
  inspector,
  inspectorOpen = false,
  columns = "minmax(0, 1.5fr) minmax(0, 0.72fr)"
}: {
  list: ReactNode;
  inspector: ReactNode;
  /** Only consulted below 900px, where the inspector is a drawer. Above that it
   *  is a column and it is always there — which is deliberate: a column that
   *  appears when a row is clicked re-lays out the chart, the ranking and every
   *  row beside it, and the panel's own empty state, the one sentence that says
   *  what clicking a row does, becomes unreachable. */
  inspectorOpen?: boolean;
  /** The split. Stated because the request log's rows are wider than the models
   *  page's and its inspector needs less room, not because a page should be free
   *  to pick a number. */
  columns?: string;
}) {
  return (
    <div className="ui-workbench" style={{ "--ui-workbench-columns": columns } as CSSProperties}>
      {/* `inert` while the drawer is over it, so Tab cannot walk into the list
          underneath the panel describing one of its rows. The attribute is set by
          the page through `useDrawerOverlay`; this only holds the geometry. */}
      <div className="ui-workbench__list">{list}</div>
      <div className={`ui-workbench__inspector${inspectorOpen ? " is-open" : ""}`}>{inspector}</div>
    </div>
  );
}

/** What is open, and everything that can be done to it.
 *
 *  There is no eyebrow. Every inspector in the console carried one, in the accent
 *  colour, directly above a heading that said the same thing in the operator's
 *  own words — and the accent is supposed to mean "you can act here", which an
 *  eyebrow never is.
 *
 *  `ref` and `onClose` are what make it a drawer below 900px: the ref comes from
 *  `useDrawerOverlay`, which owns the focus trap, the Escape key, the scrim and
 *  the page's frozen scroll, and the close button appears only at the widths
 *  where the panel is covering something. */
export function Inspector({
  title,
  level,
  subtitle,
  meta,
  actions,
  onClose,
  children,
  ref
}: {
  title: string;
  level: 2 | 3;
  /** The identifier under the name: the upstream model id, the request id. Set as
   *  data, and allowed to truncate — it is a fact to check, not a sentence. */
  subtitle?: ReactNode;
  /** A `<Tag>`, a `<Dot>` and its phrase: what state this thing is in. */
  meta?: ReactNode;
  actions?: ReactNode;
  /** Given only by a page whose inspector becomes a drawer. */
  onClose?: () => void;
  children: ReactNode;
  ref?: Ref<HTMLElement>;
}) {
  const Heading = `h${level}` as const;
  return (
    <section className="ui-inspector" ref={ref} tabIndex={-1} aria-label={title}>
      <header className="ui-inspector__head">
        <div className="ui-inspector__text">
          <Heading className="ui-inspector__title">{title}</Heading>
          {subtitle && <p className="ui-inspector__subtitle">{subtitle}</p>}
          {meta && <div className="ui-inspector__meta">{meta}</div>}
        </div>
        {actions && <div className="ui-inspector__actions">{actions}</div>}
        {onClose && (
          <button type="button" className="ui-inspector__close" onClick={onClose} aria-label={`Close ${title}`}>
            <X size={16} aria-hidden="true" />
          </button>
        )}
      </header>
      <div className="ui-inspector__body">{children}</div>
    </section>
  );
}
