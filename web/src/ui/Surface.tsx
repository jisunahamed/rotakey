import type { HTMLAttributes, ReactNode } from "react";

/** A box that holds something. Seventeen classes across two stylesheets drew this
 *  same box — white fill, hairline, panel radius, lit top edge — and no two of
 *  them agreed on all four, so panels sitting side by side on one page had
 *  different corners and different depths.
 *
 *  Three tones, and the choice between them is a statement about depth, not taste:
 *
 *  - `raised` is a slab resting on the canvas. It is lit along its top edge, which
 *    is the whole of the console's depth model.
 *  - `inset` is a well cut into whatever it sits in — a gauge in an instrument
 *    face. No lit edge, because a hole is not lit from above.
 *  - `float` genuinely hovers over the page: a menu, a popover, a panel that
 *    arrives and leaves. It is the only one that casts a real shadow.
 *
 *  Padding is off by default because most surfaces are a header plus a body, each
 *  of which pads itself; ask for it only when the surface holds one thing.
 *
 *  `as` is a short list rather than any tag: a surface is a box, and the reason to
 *  change its element is always that the box is a landmark, a list item or a form.
 *  A `<button>` is deliberately absent — something clickable is a row or a control,
 *  and both of those are their own primitive. */
export function Surface({
  children,
  tone = "raised",
  pad = "none",
  radius = "panel",
  as: Element = "div",
  className = "",
  ...rest
}: {
  children: ReactNode;
  tone?: "raised" | "inset" | "float";
  pad?: "none" | "sm" | "md" | "lg";
  /** `fitting` is for something small enough that a 7px corner would swallow its
   *  contents — a code block inside a row, a chip-sized well. */
  radius?: "panel" | "fitting" | "none";
  as?: "div" | "section" | "article" | "aside" | "li" | "form";
} & HTMLAttributes<HTMLElement>) {
  return (
    <Element
      className={`ui-surface ui-surface--${tone} ui-surface--radius-${radius} ui-surface--pad-${pad} ${className}`.trim()}
      {...rest}
    >
      {children}
    </Element>
  );
}
