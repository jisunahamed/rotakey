import type { Credential, Overview } from "../types";

/** The console's one vocabulary for "what state is this thing in".
 *
 *  There used to be three, and they disagreed. A phrase table listed fourteen
 *  states; the status dot implemented seven; the rotor implemented nine, two of
 *  which nothing could ever produce. A state present in one and missing from the
 *  others rendered as a grey dot with the raw backend enum printed beside it —
 *  which is how an operator ends up reading the word `tpr_exceeded` on a screen
 *  that is supposed to explain itself.
 *
 *  So there is one map, and it is checked against the backend's own unions below.
 *  Adding a state on the Go side now fails `tsc` here instead of shipping as grey.
 */

/** The status the gateway reports for one API key. Taken from the two places the
 *  backend spells it rather than retyped, so the two cannot drift. */
type SegmentStatus = Overview["routes"][number]["segments"][number]["status"];
export type KeyState = Credential["status"] | SegmentStatus;

/** States the console works out for itself. The gateway never sends these: they
 *  are what a pool of keys, a probe in flight, or a route on a switched-off
 *  provider looks like once the console has read everything it has. */
export type DerivedState =
  | "partial"
  | "throttled"
  | "failed"
  | "unavailable"
  | "unverified"
  | "checking";

export type ConsoleState = KeyState | DerivedState;

/** Which of the five hues the state is drawn in. A tone name and not a colour:
 *  the colours live in `tokens.css`, where both themes can answer for them, and a
 *  component that carried `#c8362f` in a prop would be right in one theme only.
 *
 *  `busy` is the accent rather than a state hue, because a check that has not
 *  finished is not a verdict — it is the console working. */
export type StateTone = "live" | "hold" | "fault" | "idle" | "busy";

export type StateEntry = {
  tone: StateTone;
  /** What goes on screen and into the screen reader: two or three plain words,
   *  never the enum. */
  phrase: string;
  /** One sentence saying what actually happened and whether it clears on its own.
   *  Legends and tooltips read this; it is also what proves, on one page, that
   *  every state the console can draw is a state it can explain. */
  meaning: string;
};

export const states = {
  healthy: {
    tone: "live",
    phrase: "Ready",
    meaning: "Rotakey will send the next matching request to this key."
  },
  cooldown: {
    tone: "hold",
    phrase: "Paused briefly",
    meaning: "An error or a rate limit made Rotakey rest this key for a moment. It returns to use on its own."
  },
  throttled: {
    tone: "hold",
    phrase: "At its rate limit",
    meaning: "This key has used everything its limits allow for now. It returns to use when the limit resets."
  },
  partial: {
    tone: "hold",
    phrase: "Partly ready",
    meaning: "Some keys here can serve a request and some cannot, so traffic still flows but with less to fall back on."
  },
  quarantined: {
    tone: "fault",
    phrase: "Taken out of use",
    meaning: "This key failed too many times in a row, so Rotakey stopped sending requests to it. It stays out until you fix it or turn it back on."
  },
  exhausted: {
    tone: "fault",
    phrase: "No credit left",
    meaning: "The balance you are tracking on this key is spent. Top it up or stop tracking a balance on it."
  },
  failed: {
    tone: "fault",
    phrase: "Check failed",
    meaning: "The last time Rotakey tried this, the provider refused it. The reason is on the row."
  },
  unavailable: {
    tone: "fault",
    phrase: "Cannot be used",
    meaning: "Nothing here can serve a request right now."
  },
  disabled: {
    tone: "idle",
    phrase: "Turned off",
    meaning: "Someone switched this off. Rotakey skips it and it counts as nothing rather than as a failure."
  },
  unverified: {
    tone: "idle",
    phrase: "Not checked yet",
    meaning: "Rotakey has never tried this, so it has nothing to report either way."
  },
  unknown: {
    tone: "idle",
    phrase: "Not known yet",
    meaning: "Rotakey has not read this key's state in this view. It is not a claim that anything is wrong."
  },
  checking: {
    tone: "busy",
    phrase: "Checking",
    meaning: "A request is in flight right now. The answer replaces this in a moment."
  }
  // `satisfies` and not a type annotation: it holds the map to exactly these keys
  // — a backend state with no entry is an error here, and so is an entry for a
  // state nothing can produce — while keeping each value's literal type, so
  // `states.healthy.tone` is "live" and not `StateTone`.
} satisfies Record<ConsoleState, StateEntry>;

export function stateEntry(state: ConsoleState): StateEntry {
  return states[state];
}
