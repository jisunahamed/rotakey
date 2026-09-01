import type { KeyboardEvent as ReactKeyboardEvent, ReactNode } from "react";

/** Two controls that look alike and are not the same thing.
 *
 *  `Tabs` switches which panel is on screen. `Segmented` picks one value out of a
 *  few — a time range, a status filter — and changes nothing about the layout.
 *  The console had five of these and treated them as one shape, so the request
 *  log's status filter announced itself as a set of tabs controlling panels that
 *  did not exist, and the playground's real tabs declared `role="tablist"` with
 *  no `aria-controls`, no roving tabindex and no arrow keys — the three things
 *  that role is a promise about.
 *
 *  Both move with the arrow keys and both wrap at the ends. A list clamps,
 *  because an operator holding ArrowDown through forty routes means to stop at
 *  the end; four choices in a row are a loop, and stopping at the last one just
 *  reads as the key having failed. */

type Choice = {
  id: string;
  label: string;
  /** A count or a state beside the word: "12", "3 need attention". Never a second
   *  label. */
  badge?: ReactNode;
};

/** Where a tab and its panel keep their ids. Both sides compute the same string
 *  from the same base, so a panel can sit anywhere in the document — beside the
 *  tabs, in another column, inside a drawer — and still be pointed at. */
export function tabID(base: string, id: string) {
  return `${base}-tab-${id}`;
}

export function panelID(base: string, id: string) {
  return `${base}-panel-${id}`;
}

/** Moves along a row of choices. Selection follows focus, which is right when
 *  switching costs nothing: an operator arrowing across four panels is looking
 *  for one of them, and making them press Enter at each stop to see what is in it
 *  turns four keystrokes into eight. */
function useChoiceKeys(items: readonly Choice[], value: string, onChange: (id: string) => void) {
  return (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
    const edge = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : -1;
    if (step === 0 && edge < 0) return;
    const here = items.findIndex((item) => item.id === value);
    const next = edge >= 0 ? edge : (here + step + items.length) % items.length;
    event.preventDefault();
    onChange(items[next].id);
    event.currentTarget.querySelector<HTMLElement>(`[data-choice="${items[next].id}"]`)?.focus();
  };
}

export function Tabs({
  items,
  value,
  onChange,
  label,
  base
}: {
  items: readonly Choice[];
  value: string;
  onChange: (id: string) => void;
  /** What this set of tabs divides up: "Provider details", "Playground panels". */
  label: string;
  /** A `useId()` from the page that owns both the tabs and their panels. */
  base: string;
}) {
  const onKeyDown = useChoiceKeys(items, value, onChange);
  return (
    <div className="ui-tabs" role="tablist" aria-label={label} onKeyDown={onKeyDown}>
      {items.map((item) => {
        const selected = item.id === value;
        return (
          <button
            key={item.id}
            type="button"
            role="tab"
            id={tabID(base, item.id)}
            data-choice={item.id}
            className={`ui-tabs__tab${selected ? " is-active" : ""}`}
            aria-selected={selected}
            aria-controls={panelID(base, item.id)}
            // Only the open tab is in the tab order. Tab from the strip lands in
            // the panel, which is where the operator was going; walking all four
            // tabs first is the thing roving tabindex exists to stop.
            tabIndex={selected ? 0 : -1}
            onClick={() => onChange(item.id)}
          >
            {item.label}
            {item.badge !== undefined && <span className="ui-tabs__badge">{item.badge}</span>}
          </button>
        );
      })}
    </div>
  );
}

/** The panel a tab opens. It is `hidden` rather than unmounted when it is not the
 *  open one, so a half-typed value in a panel survives a look at the one next to
 *  it. */
export function TabPanel({
  base,
  id,
  active,
  children,
  className = ""
}: {
  base: string;
  id: string;
  active: boolean;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      id={panelID(base, id)}
      role="tabpanel"
      aria-labelledby={tabID(base, id)}
      hidden={!active}
      // Focusable so that Tab out of the strip lands in the panel even when the
      // first thing in it is text rather than a control.
      tabIndex={active ? 0 : -1}
      className={`ui-tabpanel ${className}`.trim()}
    >
      {children}
    </div>
  );
}

export function Segmented({
  items,
  value,
  onChange,
  label,
  figures = false
}: {
  items: readonly Choice[];
  value: string;
  onChange: (id: string) => void;
  /** What is being chosen: "Time range", "Status". */
  label: string;
  /** The choices are numbers — 1h, 24h, 7d — so they take the data face and line
   *  up with each other rather than each taking the width of its own glyphs. */
  figures?: boolean;
}) {
  const onKeyDown = useChoiceKeys(items, value, onChange);
  return (
    <div
      className={`ui-segmented${figures ? " ui-segmented--figures" : ""}`}
      role="radiogroup"
      aria-label={label}
      onKeyDown={onKeyDown}
    >
      {items.map((item) => {
        const checked = item.id === value;
        return (
          <button
            key={item.id}
            type="button"
            role="radio"
            data-choice={item.id}
            className={`ui-segmented__choice${checked ? " is-active" : ""}`}
            aria-checked={checked}
            tabIndex={checked ? 0 : -1}
            onClick={() => onChange(item.id)}
          >
            {item.label}
            {item.badge !== undefined && <span className="ui-segmented__badge">{item.badge}</span>}
          </button>
        );
      })}
    </div>
  );
}
