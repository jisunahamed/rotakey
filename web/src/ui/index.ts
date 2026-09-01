/** The console's primitive kit.
 *
 *  Everything a page draws comes from here, so that the same idea is the same
 *  object everywhere it appears. Before this existed, `App.tsx` declared 242 class
 *  names and 76% of them were used exactly once: sixteen kinds of row, twelve
 *  kinds of button, five segmented controls, eight ways to show a row was
 *  selected. Nothing looked the same twice because nothing *was* the same twice,
 *  which is the whole of "there is no address for where anything is".
 *
 *  Two rules hold the kit together.
 *
 *  A primitive is added by *deleting* — it earns its place by replacing a named
 *  set of existing classes, not by being a good idea. And a primitive takes the
 *  choices that carry meaning as props and refuses the rest: `Empty` takes the
 *  heading level because a heading at the wrong depth breaks the page for a screen
 *  reader, and takes no colour because there is nothing an empty list could mean
 *  by being green.
 *
 *  Nothing is migrated onto the kit in the commit that adds it. Both systems are
 *  on screen at once for a few commits, which is why every class here is prefixed
 *  `ui-`: the two vocabularies cannot collide in the cascade while they overlap,
 *  and afterwards the prefix still says which system a rule belongs to. */

export { Surface } from "./Surface";
export { Label } from "./Label";
export { SectionHeader } from "./SectionHeader";
export { Dot } from "./Dot";
export { Tag } from "./Tag";
export { Stat } from "./Stat";
export { Notice, type NoticeTone } from "./Notice";
export { Empty } from "./Empty";
export { DataTable, Row, Cell } from "./DataTable";
export { Field, FieldPair, FieldStack, FieldRow, type ControlProps } from "./Field";
export { Toolbar, SearchInput } from "./Toolbar";
export { Tabs, TabPanel, Segmented, tabID, panelID } from "./Tabs";
export { Disclosure } from "./Disclosure";
export { Menu, MenuItem, MenuSection } from "./Menu";
export { WorkbenchFrame, Workbench, Inspector } from "./Workbench";
/** The one exception to the `ui-` prefix, and it predates the rule: the rotor has
 *  had its own component and its own stylesheet since before the kit existed, and
 *  it is the console's signature rather than a generic shape. It lives here now
 *  because three pages draw it and none of them should be importing it from
 *  another page. */
export { Rotor } from "./Rotor";
export { Markdown } from "./Markdown";
export { states, stateEntry, type ConsoleState, type KeyState, type DerivedState, type StateTone, type StateEntry } from "./state";

/** The parts that were already right.
 *
 *  These are re-exported and not rewritten. Each one encodes something that took
 *  a long time to get correct and that a fresh implementation would get wrong
 *  again: `Sheet`'s Tab trap, its Escape handling, where it puts focus on open and
 *  where it puts it back on close, and its refusal to discard a dirty form without
 *  asking; `ConfirmDialog`'s promise-shaped contract and the context that tells
 *  every other overlay to stand down while a question is on screen;
 *  `useDrawerOverlay`'s `inert` and scroll lock; `SecretReveal`'s one-time
 *  contract.
 *
 *  They are exported from here so that a page imports its whole vocabulary from
 *  one place. Where a primitive above overlaps one of these — `Notice` and
 *  `InlineNotice`, `Empty` and `EmptyState` — both remain until the pages have
 *  moved, and then the older one goes. */
export { Button } from "../Button";
export { Sheet, Toggle, RateFields, InlineNotice, EmptyState, StatusDot, statusLabel, useDrawerOverlay } from "../components";
export { useConfirm, useConfirmOpen } from "../ConfirmDialog";
export { useListKeys, useFilterHotkey } from "../keyboard";
