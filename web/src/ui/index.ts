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
export { states, stateEntry, type ConsoleState, type KeyState, type DerivedState, type StateTone, type StateEntry } from "./state";
