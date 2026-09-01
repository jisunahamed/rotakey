/** Browser state React has no opinion about, read as React state.
 *
 *  The one rule these follow: subscribe in an effect, read once on mount, and
 *  unsubscribe on unmount. Anything that reads a live browser value during
 *  render instead disagrees with itself the moment the value changes.
 */

import { useEffect, useState } from "react";

/** Reads a media query in JS so behaviour (focus order, inert) can follow the
 *  same breakpoint the stylesheets use, rather than a second guess at it. */
export function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const list = window.matchMedia(query);
    const onChange = () => setMatches(list.matches);
    onChange();
    list.addEventListener("change", onChange);
    return () => list.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}
