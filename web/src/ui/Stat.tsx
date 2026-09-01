import type { ReactNode } from "react";
import { Label } from "./Label";
import type { StateTone } from "./state";

/** One figure with the word for what it counts. Eight versions of this existed —
 *  the ledger cells, the capacity readouts, the sweep tally, the spend summary —
 *  and they disagreed about which of the two lines was larger, which face the
 *  number took, and whether the state hue tinted the cell or sat in a bar.
 *
 *  It sits in a bar along the bottom. Seven or eight of these stand side by side
 *  in a row, and tinting the cells turns that row into a stripe of colour with no
 *  figure legible in it.
 *
 *  Nothing here is allowed to be clipped. A `Stat` is often the only place a figure
 *  appears, with no title attribute and no row to expand, so an ellipsis would put
 *  the number somewhere the operator cannot reach. Both lines wrap instead, and the
 *  grid the stats sit in equalises around whichever one needed two lines.
 *
 *  `value` is a node rather than a string so a figure can carry its unit in a
 *  smaller face — but it must stay a figure. Anything that needs a sentence is not
 *  a stat. */
export function Stat({
  label,
  value,
  note,
  tone
}: {
  label: string;
  value: ReactNode;
  /** One short line under the figure: what it is measured over, or why it is
   *  absent. This is where "Not tracked" belongs — never in place of the figure,
   *  because a stat with a word where its number should be reads as a number. */
  note?: string;
  tone?: StateTone;
}) {
  return (
    <div className={`ui-stat ${tone ? `ui-stat--${tone}` : ""}`.trim()}>
      <Label as="span">{label}</Label>
      <strong className="ui-stat__value">{value}</strong>
      {note && <small className="ui-stat__note">{note}</small>}
    </div>
  );
}
