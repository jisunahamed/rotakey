import { states, type ConsoleState } from "./state";

/** An 8px circle carrying one state. The console's smallest readout, and the one
 *  it repeats the most: every key, every route and every provider row starts with
 *  one.
 *
 *  It is drawn with a wash halo rather than a bare dot, because 8px of colour in
 *  peripheral vision is not enough to separate four hues — the ring is what makes
 *  the difference legible from across a desk.
 *
 *  `label` is what a screen reader hears. It defaults to the state's own phrase,
 *  which is right when the dot is the only thing saying it. Pass a better one
 *  whenever the caller knows more than the state does: "No credit left" is the
 *  truth for a key whose balance is spent and a lie for a provider that returned a
 *  500, and both used to reach this component as the same word.
 *
 *  Pass `label=""` when the row already says it in words beside the dot — then the
 *  dot is decoration and repeating it only makes the row longer to listen to. */
export function Dot({ state, label }: { state: ConsoleState; label?: string }) {
  const phrase = label ?? states[state].phrase;
  return (
    <span
      className={`ui-dot ui-dot--${states[state].tone} ui-dot--${state}`}
      role={phrase ? "img" : undefined}
      aria-label={phrase || undefined}
      aria-hidden={phrase ? undefined : true}
    />
  );
}
