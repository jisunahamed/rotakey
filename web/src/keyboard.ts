import { useCallback, useEffect, type KeyboardEvent as ReactKeyboardEvent, type RefObject } from "react";
import { useCoveredPage } from "./overlays";

/** The console's keyboard contract, in one place.
 *
 *  Everything here is an addition to what the browser already does, never a
 *  replacement for it: every row is still a real button, so Tab still walks the
 *  list and Enter still opens the row under focus. The arrow keys are the faster
 *  way through a list of forty routes, and the single-key shortcuts are the way to
 *  a page without reaching for the rail. */

/** Which modifier this keyboard prints. It changes only what is *shown* — every
 *  handler below accepts Ctrl and ⌘ alike, so an operator on a Mac keyboard
 *  plugged into a Windows machine never has to know which one the console
 *  believes. `navigator.platform` is deprecated and still the only thing that
 *  answers this in every browser; being wrong costs one wrong glyph on a hint. */
export const isMac = /mac|iphone|ipad/i.test(navigator.platform || navigator.userAgent);

/** True when the keystroke belongs to something the operator is typing into. A
 *  single-key shortcut has to stand down here, or `/` never reaches a search box
 *  and `?` cannot be typed into a prompt. */
export function isTypingTarget(target: EventTarget | null): boolean {
  const node = target as HTMLElement | null;
  if (!node || typeof node.tagName !== "string") return false;
  return node.tagName === "INPUT" || node.tagName === "TEXTAREA" || node.tagName === "SELECT" || node.isContentEditable;
}

/** A keystroke the operator meant as a shortcut: no chord, and not aimed at a
 *  field. Ctrl and Meta are excluded because those belong to the browser and to
 *  the palette, which asks for them explicitly. */
function isPlainKey(event: KeyboardEvent): boolean {
  return !event.ctrlKey && !event.metaKey && !event.altKey && !isTypingTarget(event.target);
}

/** Arrow-key movement through a list of rows. Attach the returned handler to the
 *  element that *contains the rows* — not the pane around it, or Home and End
 *  would be taken away from the filter box above them — and mark each row with
 *  `data-row`. The rows keep their place in the tab order: arrows are a second way
 *  through the list, not a replacement for the first one.
 *
 *  Focus moves; it does not select. On a narrow screen the inspector is a drawer,
 *  so selecting on arrow would slam a panel over the list the operator is still
 *  reading. Enter opens the row, which is what a button already does. */
export function useListKeys() {
  return useCallback((event: ReactKeyboardEvent<HTMLElement>) => {
    if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey) return;
    if (isTypingTarget(event.target)) return;
    const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    const edge = event.key === "Home" ? "first" : event.key === "End" ? "last" : "";
    if (step === 0 && edge === "") return;

    const rows = Array.from(event.currentTarget.querySelectorAll<HTMLElement>("[data-row]"));
    if (rows.length === 0) return;
    const active = document.activeElement;
    let here = rows.findIndex((row) => row === active || row.contains(active));
    // A row can carry a second control beside the one marked `data-row` — an
    // overflow menu, a check button. Landing on it still counts as being on that
    // row, so the arrow keys move from there rather than jumping to the top.
    if (here < 0 && active) here = rows.findIndex((row) => row.parentElement?.contains(active));

    const next =
      edge === "first" ? 0
      : edge === "last" ? rows.length - 1
      : here < 0 ? (step > 0 ? 0 : rows.length - 1)
      // Deliberately clamped rather than wrapped: an operator holding ArrowDown to
      // reach the end of a list should stop at the end, not reappear at the top.
      : Math.min(rows.length - 1, Math.max(0, here + step));

    event.preventDefault();
    rows[next].focus();
    rows[next].scrollIntoView({ block: "nearest" });
  }, []);
}

/** `/` puts the cursor in the page's filter box, wherever focus happens to be.
 *  The text already there is selected, so the next thing typed replaces the last
 *  search rather than extending it. */
export function useFilterHotkey(field: RefObject<HTMLInputElement | null>) {
  const covered = useCoveredPage();
  useEffect(() => {
    if (covered) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "/" || !isPlainKey(event)) return;
      const input = field.current;
      if (!input) return;
      event.preventDefault();
      input.focus();
      input.select();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [covered, field]);
}

/** The two shortcuts the shell owns. The palette answers Ctrl+K and ⌘K, which is
 *  the chord every application with a palette uses; the shortcut list answers `?`,
 *  which is where anyone looks for it.
 *
 *  `paletteOpen` is what makes the chord a toggle rather than a one-way door: the
 *  second press closes the palette even though the palette is itself covering the
 *  page. Everything else that covers the page — a form sheet, a confirmation —
 *  keeps the chord for itself, because every result in the palette is a
 *  navigation, and navigating out from under a half-filled form on a keystroke is
 *  how work gets lost. */
export function useShellHotkeys({
  paletteOpen,
  onPalette,
  onShortcuts
}: {
  paletteOpen: boolean;
  onPalette: () => void;
  onShortcuts: () => void;
}) {
  const covered = useCoveredPage();
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && !event.altKey && event.key.toLowerCase() === "k") {
        if (covered && !paletteOpen) return;
        event.preventDefault();
        onPalette();
        return;
      }
      if (covered) return;
      if (event.key === "?" && isPlainKey(event)) {
        event.preventDefault();
        onShortcuts();
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [covered, paletteOpen, onPalette, onShortcuts]);
}

/** What the shortcut list shows. It is written out rather than collected from the
 *  handlers above so that each line can say what the shortcut is *for*; a table of
 *  key names tells an operator nothing they could not have guessed. */
export const shortcutGroups: ReadonlyArray<{
  title: string;
  shortcuts: ReadonlyArray<{ keys: string[]; description: string }>;
}> = [
  {
    title: "Anywhere",
    shortcuts: [
      { keys: ["Ctrl", "K"], description: "Search every page, provider, model route and API key by name" },
      { keys: ["?"], description: "Open this list" },
      { keys: ["Esc"], description: "Close whatever panel is in front — a menu, a form, a question" }
    ]
  },
  {
    title: "In a list",
    shortcuts: [
      { keys: ["/"], description: "Jump to the page's filter box and replace what is in it" },
      { keys: ["↑", "↓"], description: "Move between rows" },
      { keys: ["Home", "End"], description: "Jump to the first or last row" },
      { keys: ["Enter"], description: "Open the row you are on" },
      { keys: ["Tab"], description: "Move on to the panel beside the list" }
    ]
  },
  {
    title: "In the search box",
    shortcuts: [
      { keys: ["↑", "↓"], description: "Move between results" },
      { keys: ["Enter"], description: "Go to the highlighted result" },
      { keys: ["Esc"], description: "Close the search without going anywhere" }
    ]
  }
];
