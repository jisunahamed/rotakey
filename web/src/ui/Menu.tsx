import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type ReactNode
} from "react";
import { createPortal } from "react-dom";
import { MoreHorizontal } from "lucide-react";
import { useConfirmOpen } from "../ConfirmDialog";

/** A short list of things that can be done, behind one control.
 *
 *  Nothing like this existed. The owner's brief was that every option must
 *  survive but must sit somewhere it can be found by clicking, and the console's
 *  answer up to now was to put every option on the surface at once — which is why
 *  the rail's foot carried five loose items and a provider row carried four
 *  buttons, one of which took a live route off the air.
 *
 *  Everything the account menu learned the hard way is here, because a menu that
 *  gets any of it wrong is worse than no menu: Escape closes it and puts focus
 *  back on the trigger, a press outside closes it before the thing underneath is
 *  pressed, the arrow keys walk it, and while a confirmation is open on top of it
 *  the whole lot stands down — or Escape would answer the question and shut the
 *  menu in one keypress, and the click that opened the dialog would read as a
 *  click outside.
 *
 *  The panel is drawn at the top of the document rather than beside its trigger.
 *  It has to be: every place a menu is actually wanted is inside something that
 *  clips — a row inside a list that scrolls, a section inside an inspector that
 *  scrolls — and a panel left where it was declared is cut off by the pane it
 *  belongs to. So it is positioned from the trigger's own rectangle, follows it
 *  on scroll, and flips above the trigger when there is no room below. */

const MenuClose = createContext<() => void>(() => {});

/** The gap between the trigger and the panel, and the margin the panel keeps from
 *  the edge of the window. Numbers rather than tokens because they are read by
 *  arithmetic here, not by the stylesheet. */
const GAP = 6;
const EDGE = 8;

export function Menu({
  label,
  trigger,
  children,
  align = "end"
}: {
  /** What this menu acts on: "More actions for gpt-4o". It names the trigger,
   *  which is otherwise three dots, and it names the menu, which is otherwise a
   *  list of verbs with no subject. */
  label: string;
  /** What the trigger shows. Three dots by default, which is what a row uses. */
  trigger?: ReactNode;
  children: ReactNode;
  /** Which edge of the trigger the panel lines up with. */
  align?: "start" | "end";
}) {
  const [open, setOpen] = useState(false);
  const container = useRef<HTMLDivElement | null>(null);
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const panel = useRef<HTMLDivElement | null>(null);
  const confirmOpen = useConfirmOpen();
  const confirmOpenRef = useRef(confirmOpen);
  confirmOpenRef.current = confirmOpen;

  const close = () => {
    setOpen(false);
    triggerRef.current?.focus();
  };
  const closeRef = useRef(close);
  closeRef.current = close;

  useEffect(() => {
    if (!open) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (confirmOpenRef.current || event.key !== "Escape") return;
      event.preventDefault();
      closeRef.current();
    };
    // pointerdown rather than click: the menu should be gone by the time the
    // button underneath it is pressed, not after. The panel is checked separately
    // from the trigger because it is no longer inside it — it is drawn at the top
    // of the document, so `container` does not contain it.
    const onPointerDown = (event: PointerEvent) => {
      if (confirmOpenRef.current) return;
      const target = event.target as Node;
      if (container.current?.contains(target) || panel.current?.contains(target)) return;
      setOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    document.addEventListener("pointerdown", onPointerDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.removeEventListener("pointerdown", onPointerDown);
    };
  }, [open]);

  /** Where the panel goes, worked out from the trigger's rectangle in the window.
   *
   *  Written onto the node rather than returned as state, and that is not a style
   *  choice. React flushes the effect that moves focus into the panel before it
   *  commits a re-render queued from a layout effect, so a placement held in state
   *  arrives *after* the first item is focused — and focusing an element that is
   *  still sitting at its resting place scrolls the window to it. The panel opened
   *  correctly and the page jumped five thousand pixels.
   *
   *  All four offsets are written, `auto` on the side that is not used. Setting
   *  only the side that matters leaves the stylesheet's `top` in force, and an
   *  element pinned by `top` and `bottom` at once is not placed by them — it is
   *  stretched between them, which turned a four-item menu into a panel two and a
   *  half thousand pixels tall. */
  const place = useCallback(() => {
    const node = panel.current;
    const anchor = triggerRef.current?.getBoundingClientRect();
    if (!node || !anchor) return;
    // The window measured the way a fixed element is: `innerWidth` counts the
    // scrollbar and the containing block does not, so `right` worked out from it
    // put every row menu a scrollbar's width left of the trigger it belongs to.
    const width = document.documentElement.clientWidth;
    const height = document.documentElement.clientHeight;
    const below = height - anchor.bottom - GAP - EDGE;
    const above = anchor.top - GAP - EDGE;
    // The natural height, measured before any cap is applied — a panel already
    // capped at the room below would always appear to fit there and would never
    // flip.
    node.style.maxHeight = "";
    // Flip only to gain room. A menu opens downwards unless it cannot, because
    // down is where the eye goes after pressing something.
    const flipped = node.offsetHeight > below && above > below;
    node.style.position = "fixed";
    node.style.top = flipped ? "auto" : `${anchor.bottom + GAP}px`;
    node.style.bottom = flipped ? `${height - anchor.top + GAP}px` : "auto";
    node.style.left = align === "end" ? "auto" : `${Math.max(EDGE, anchor.left)}px`;
    node.style.right = align === "end" ? `${Math.max(EDGE, width - anchor.right)}px` : "auto";
    // The room on the side it actually opened towards, so a menu longer than the
    // window scrolls to its last item — which is the destructive one — instead of
    // hiding it off the edge.
    node.style.maxHeight = `${Math.max(flipped ? above : below, 0)}px`;
  }, [align]);

  useLayoutEffect(() => {
    if (!open) return;
    place();
    // Capture, so a scroll in any pane between the trigger and the window moves
    // the panel with it rather than leaving it hanging over the page.
    window.addEventListener("scroll", place, true);
    window.addEventListener("resize", place);
    return () => {
      window.removeEventListener("scroll", place, true);
      window.removeEventListener("resize", place);
    };
  }, [open, place]);

  // Opening from the keyboard has to land somewhere, and the first item is the
  // only place that is not a guess.
  useEffect(() => {
    if (!open) return;
    panel.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus();
  }, [open]);

  const onPanelKeyDown = (event: ReactKeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    const edge = event.key === "Home" ? "first" : event.key === "End" ? "last" : "";
    if (step === 0 && edge === "") return;
    const items = Array.from(event.currentTarget.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])'));
    if (items.length === 0) return;
    const here = items.findIndex((item) => item === document.activeElement);
    // A menu wraps where a list clamps: it is a handful of items in a panel the
    // operator can see the whole of, not a scroll through forty rows.
    const next =
      edge === "first" ? 0
      : edge === "last" ? items.length - 1
      : here < 0 ? (step > 0 ? 0 : items.length - 1)
      : (here + step + items.length) % items.length;
    event.preventDefault();
    items[next].focus();
  };

  return (
    <div className="ui-menu" ref={container}>
      <button
        type="button"
        ref={triggerRef}
        className={`ui-menu__trigger${trigger ? " ui-menu__trigger--wide" : ""}${open ? " is-open" : ""}`}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label={label}
        onClick={() => setOpen((current) => !current)}
      >
        {trigger ?? <MoreHorizontal size={16} aria-hidden="true" />}
      </button>
      {open && createPortal(
        <div
          ref={panel}
          className="ui-menu__panel"
          role="menu"
          aria-label={label}
          onKeyDown={onPanelKeyDown}
        >
          <MenuClose.Provider value={close}>{children}</MenuClose.Provider>
        </div>,
        document.body
      )}
    </div>
  );
}

/** A band of related items with a caption over it. The caption is what turns a
 *  list of eleven verbs into three short lists an operator can scan. */
export function MenuSection({ caption, children }: { caption?: string; children: ReactNode }) {
  return (
    <div className="ui-menu__section">
      {caption && <p className="ui-menu__caption">{caption}</p>}
      {children}
    </div>
  );
}

/** One thing that can be done, or one place to go.
 *
 *  Choosing closes the menu, always — including on a link, where the browser is
 *  about to leave anyway and a menu left hanging over the new page is the thing
 *  that looks broken. An action that opens a confirmation is the one case where
 *  that would be wrong, so it closes after the answer rather than before the
 *  question: `onSelect` is awaited. */
export function MenuItem({
  children,
  onSelect,
  href,
  icon,
  tone = "plain",
  disabled = false
}: {
  children: ReactNode;
  onSelect?: () => void | Promise<void>;
  href?: string;
  icon?: ReactNode;
  /** `danger` for anything that destroys work. It is the one hue in a menu,
   *  because a list where three items are coloured says nothing about any of
   *  them. */
  tone?: "plain" | "danger";
  disabled?: boolean;
}) {
  const close = useContext(MenuClose);
  const className = `ui-menu__item${tone === "danger" ? " ui-menu__item--danger" : ""}`;
  if (href) {
    return (
      <a className={className} role="menuitem" href={href} target="_blank" rel="noreferrer" onClick={() => close()}>
        {icon}
        <span>{children}</span>
      </a>
    );
  }
  return (
    <button
      type="button"
      className={className}
      role="menuitem"
      disabled={disabled}
      onClick={() => {
        void Promise.resolve(onSelect?.()).finally(close);
      }}
    >
      {icon}
      <span>{children}</span>
    </button>
  );
}
