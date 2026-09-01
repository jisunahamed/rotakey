import { useEffect, useState } from "react";

/** One contract for everything that covers the page.
 *
 *  The console draws four kinds of overlay — the navigation drawer, the inspector
 *  drawers inside four pages, the edit sheet and the confirmation dialog — and each
 *  one grew its own half of the behaviour. Only the navigation drawer stopped the
 *  page behind it scrolling, and it did that by writing `document.body.style`
 *  directly, so a second overlay opening on top of it would have handed the page
 *  its scrollbar back on close. The inspector drawers cover the list they belong to
 *  with nothing between, so on a phone the page reads as two overlapping pages.
 *
 *  This module owns both facts: how many overlays are up, and which drawer is the
 *  one currently covering a page. */

/* ---------------------------------------------------------------- scroll lock */

/** How many overlays are currently up. The lock is released on the way past zero,
 *  never on the first close: a confirmation opened from inside a sheet closes
 *  before the sheet does, and the sheet is still covering the page. */
let lockCount = 0;
/** Whatever the document was set to before the first overlay took the scrollbar.
 *  Restored rather than cleared, because nothing guarantees it was empty. */
let overflowBeforeLock = "";

/** Stops the page behind an overlay scrolling for as long as `active` holds.
 *  Every overlay calls this, and they nest. */
export function useScrollLock(active: boolean) {
  useEffect(() => {
    if (!active) return;
    if (lockCount === 0) {
      overflowBeforeLock = document.body.style.overflow;
      document.body.style.overflow = "hidden";
    }
    lockCount += 1;
    return () => {
      lockCount -= 1;
      if (lockCount === 0) document.body.style.overflow = overflowBeforeLock;
    };
  }, [active]);
}

/* ------------------------------------------------------------ inspector drawer */

/** Closing the drawer that is currently covering a page, or null when none is.
 *
 *  Each inspector lives inside its own page, but the two things that have to react
 *  to one being open live in the shell: the scrim drawn over the page behind it,
 *  and the navigation rail, which slides out from the same edge and must not open
 *  on top of it. Publishing one registration is how the shell learns about a state
 *  four levels below it — the same shape the routing mode and the browser tab's
 *  detail already use.
 *
 *  One at a time is not a simplification: exactly one page is mounted, and a page
 *  has one inspector. */
let closeActiveDrawer: (() => void) | null = null;
const drawerSubscribers = new Set<(open: boolean) => void>();

function announceDrawer() {
  const open = closeActiveDrawer !== null;
  drawerSubscribers.forEach((notifySubscriber) => notifySubscriber(open));
}

/** Called by a drawer for as long as it is acting as an overlay rather than as a
 *  column of the layout. Returns its own release. */
export function registerDrawer(close: () => void) {
  closeActiveDrawer = close;
  announceDrawer();
  return () => {
    // A drawer that has already been replaced must not clear the newer one's
    // registration on its way out.
    if (closeActiveDrawer !== close) return;
    closeActiveDrawer = null;
    announceDrawer();
  };
}

/** Dismisses whichever inspector is covering the page. The shell's scrim calls
 *  this, and so does opening the navigation: two panels sliding over each other
 *  from the same edge is not a state the operator asked for. */
export function closeActiveDrawerIfAny() {
  closeActiveDrawer?.();
}

/** Whether an inspector is covering the page right now, for the shell's scrim. */
export function useDrawerOpen() {
  const [open, setOpen] = useState(closeActiveDrawer !== null);
  useEffect(() => {
    drawerSubscribers.add(setOpen);
    setOpen(closeActiveDrawer !== null);
    return () => {
      drawerSubscribers.delete(setOpen);
    };
  }, []);
  return open;
}
