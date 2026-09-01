import type { CSSProperties, KeyboardEventHandler, ReactNode } from "react";
import { ChevronRight } from "lucide-react";

/** A list of rows that open something.
 *
 *  Sixteen of these existed. Not sixteen tables — sixteen re-implementations of
 *  the same table, each one deciding again how tall a row is, how a selected row
 *  is marked, whether a cell truncates, and which of the eight selection washes
 *  it used. Between them they declared 66 `grid-template-columns`.
 *
 *  The track list is the one thing that genuinely differs between two lists, so
 *  it is the one thing a caller states. It is declared on the table and read by
 *  the header and every row through a custom property, which is what stops a
 *  column heading drifting away from the column under it — the failure the old
 *  markup made almost inevitable, because each row redeclared the tracks itself.
 *
 *  These are buttons, not a `<table>`. Every row here opens a panel rather than
 *  presenting a cell of data to be compared with the cell beside it, and a real
 *  table would take Tab, Enter and the browser's own click handling away from
 *  rows that need all three. `useListKeys` adds the arrow keys on top. */
export function DataTable({
  columns,
  label,
  head,
  children,
  onKeyDown,
  linked = true,
  actions = false
}: {
  /** A CSS grid track list, written once for the whole table. Use `minmax(0, …)`
   *  for anything that holds text: the panes do not scroll sideways, so a track
   *  with a content-based minimum pushes the far side of the row out of sight
   *  instead of ellipsing. */
  columns: string;
  /** What this list is, for a screen reader arriving at a group of buttons. */
  label: string;
  /** The column captions. `<Cell>`s, in the same order as the tracks. */
  head?: ReactNode;
  children: ReactNode;
  /** From `useListKeys` — the arrow keys, Home and End. It belongs on the rows
   *  and not on the pane around them, or Home and End stop working in the filter
   *  box above. */
  onKeyDown?: KeyboardEventHandler<HTMLDivElement>;
  /** Whether every row opens something, which is what the trailing chevron says.
   *  It is a property of the table rather than of a row because a column of
   *  chevrons with a gap in it reads as a row that is broken, not as a row that
   *  is inert. */
  linked?: boolean;
  /** Whether the rows carry an overflow menu beside them. The header has to
   *  reserve the same strip, or every caption sits a menu's width to the left of
   *  the column beneath it. Stated on the table for the same reason as `linked`:
   *  it is the shape of the list, and a row that decided it for itself is how the
   *  columns came apart in the first place. */
  actions?: boolean;
}) {
  return (
    <div
      className={`ui-table${linked ? " ui-table--linked" : ""}${actions ? " ui-table--actions" : ""}`}
      style={{ "--ui-table-columns": columns } as CSSProperties}
    >
      <div className="ui-table__rows" role="group" aria-label={label} onKeyDown={onKeyDown}>
        {/* Inside the scroller, not above it. The rows reserve a scrollbar gutter,
            so a header that is a sibling of them is wider than they are by exactly
            that gutter and every caption sits left of its column — the failure
            this component exists to prevent, reintroduced by the one element that
            is supposed to prove it has not happened. */}
        {head && <div className="ui-table__head">{head}</div>}
        {children}
      </div>
    </div>
  );
}

/** One row. Selected and hovered share a wash on purpose — the row under the
 *  pointer and the row that is open are the same colour — and the bar down the
 *  left edge is the only thing that tells them apart, so that moving the mouse
 *  across a list never looks like it is changing what is open. */
export function Row({
  children,
  selected = false,
  onClick,
  href,
  title,
  disabled,
  actions
}: {
  children: ReactNode;
  selected?: boolean;
  onClick?: () => void;
  /** Makes the row a real link. A row that has a URL should be one: middle-click,
   *  Cmd-click and "copy link address" are how an operator gets a second view of
   *  the same list open, and a button cannot offer any of them. */
  href?: string;
  title?: string;
  disabled?: boolean;
  /** A `<Menu>`, usually — everything that can be done to this row without opening
   *  it. It is rendered beside the row rather than inside it, because the row is a
   *  button and a button inside a button is not a thing a browser can render. */
  actions?: ReactNode;
}) {
  const className = `ui-row${selected ? " is-selected" : ""}`;
  // `aria-current` rather than `aria-selected`: nothing here is a listbox option,
  // and this is the plain meaning — the one of these the panel beside them is
  // currently showing.
  const shared = { className, title, "data-row": "", "aria-current": selected || undefined } as const;
  const body = (
    <>
      {children}
      <ChevronRight className="ui-row__chevron" size={14} aria-hidden="true" />
    </>
  );
  const row = href
    ? <a {...shared} href={href} onClick={onClick && ((event) => {
        // A plain click stays inside the console; anything with a modifier is the
        // operator asking the browser for a second tab or a second window, and the
        // console has no business intercepting it.
        if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
        event.preventDefault();
        onClick();
      })}>{body}</a>
    : <button {...shared} type="button" onClick={onClick} disabled={disabled}>{body}</button>;

  if (!actions) return row;
  // `useListKeys` already treats focus on a control beside a `data-row` element as
  // being on that row, so the arrow keys keep working from inside the menu's
  // trigger rather than jumping back to the top of the list.
  return (
    <div className="ui-row-shell">
      {row}
      <div className="ui-row__actions">{actions}</div>
    </div>
  );
}

/** One cell. Everything in here truncates, because the panes clip rather than
 *  scroll sideways: a cell that refuses to shorten is not wider, it is cut off
 *  with no way to reach the rest of it. */
export function Cell({
  children,
  sub,
  icon,
  align = "start",
  figure = false,
  title
}: {
  children?: ReactNode;
  /** A second, quieter line under the first: the upstream model id under the
   *  alias, the provider under the key label. It is the commonest shape in the
   *  console's lists and it was hand-built in all sixteen of them. */
  sub?: ReactNode;
  /** A `<Dot>`, usually. It keeps its own column so the two lines above stay
   *  aligned with each other rather than each starting after the dot. */
  icon?: ReactNode;
  align?: "start" | "end";
  /** A number: the data face and tabular figures, so a column of them lines up
   *  and a value changing does not shift the row. */
  figure?: boolean;
  title?: string;
}) {
  const className = [
    "ui-cell",
    align === "end" && "ui-cell--end",
    figure && "ui-cell--figure",
    icon && "ui-cell--icon"
  ].filter(Boolean).join(" ");
  return (
    <span className={className} title={title}>
      {icon}
      <span className="ui-cell__line">{children}</span>
      {sub !== undefined && sub !== null && sub !== "" && <small className="ui-cell__sub">{sub}</small>}
    </span>
  );
}
