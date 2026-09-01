import { createContext, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "./Button";
import { focusableSelector, trapTab } from "./focus";
import { useScrollLock } from "./overlays";

/** The console's consequential decisions used to go through window.confirm(): the
 * browser's own dialog, which cannot be styled, cannot be read by a screen reader as
 * part of this page, renders the question as a single unformatted string with the
 * origin printed above it, and — in a Chrome tab that has already shown one — offers
 * the operator a checkbox to suppress every future one. Deleting a provider is not a
 * decision to hand to a control the page does not own.
 *
 * This is the same contract in one modal the console draws itself: `ask()` returns a
 * promise that resolves true when the operator confirms and false when they cancel,
 * so a call site reads exactly as `if (!(await ask(…))) return;`.
 */
export type ConfirmRequest = {
  title: string;
  /** The consequence, in the operator's terms. Sentences, not a wall. */
  body: ReactNode;
  /** Names the action being confirmed, and matches the button that opened the
   * dialog — "Delete provider", not "OK". */
  confirmLabel: string;
  cancelLabel?: string;
  /** `danger` for anything that destroys data or stops traffic. It colours the
   * confirm button and, more importantly, opens with focus on Cancel. */
  tone?: "danger" | "primary";
  /** Shown as a bordered block below the body — a list of what is about to be
   * deleted, or an import summary. Kept separate so the prose stays readable. */
  detail?: ReactNode;
};

type Pending = ConfirmRequest & { resolve: (answer: boolean) => void };

const ConfirmContext = createContext<(request: ConfirmRequest) => Promise<boolean>>(
  () => Promise.resolve(false)
);

/** Whether a confirmation is on screen right now. The sheet and the inspector drawer
 * both listen for Escape and Tab on the document, and this dialog opens *on top of*
 * them — so without a way to ask, their listeners would keep running underneath:
 * Escape would answer the question and then immediately ask it again, and Tab would
 * pull focus out of the dialog and back into the panel behind it. */
const ConfirmOpenContext = createContext(false);

/** Wraps the app once. One dialog exists at a time: a confirmation is a modal
 * decision, so a second request while one is open cannot be a second dialog. */
export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);

  const ask = useCallback(
    (request: ConfirmRequest) =>
      new Promise<boolean>((resolve) => {
        setPending((current) => {
          // Nothing should be able to stack a second question on top of the first,
          // but if it happens the earlier one is answered "no" rather than left
          // holding a promise that never settles.
          current?.resolve(false);
          return { ...request, resolve };
        });
      }),
    []
  );

  const settle = useCallback((answer: boolean) => {
    setPending((current) => {
      current?.resolve(answer);
      return null;
    });
  }, []);

  return (
    <ConfirmContext.Provider value={ask}>
      <ConfirmOpenContext.Provider value={pending !== null}>
        {children}
        {pending && <ConfirmDialog request={pending} onSettle={settle} />}
      </ConfirmOpenContext.Provider>
    </ConfirmContext.Provider>
  );
}

export function useConfirm() {
  return useContext(ConfirmContext);
}

/** True while a confirmation is open. Any component with its own document-level
 * Escape or Tab handler has to stand down while this is true. */
export function useConfirmOpen() {
  return useContext(ConfirmOpenContext);
}

function ConfirmDialog({
  request,
  onSettle
}: {
  request: ConfirmRequest;
  onSettle: (answer: boolean) => void;
}) {
  const headingID = useId();
  const bodyID = useId();
  // A question is the most modal thing the console does. The lock is counted, so
  // this releasing does not hand the scrollbar back to a sheet that is still open
  // underneath — which is exactly the case, since a sheet is usually what asked.
  useScrollLock(true);
  const panel = useRef<HTMLDivElement | null>(null);
  const cancel = useRef<HTMLButtonElement | null>(null);
  const settleRef = useRef(onSettle);
  settleRef.current = onSettle;

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    // A destructive dialog opens on Cancel. The operator arrived here by pressing a
    // button, so their hands are already on Enter; focusing the button that deletes
    // the provider would make one more keypress finish the job.
    (cancel.current ?? panel.current?.querySelector<HTMLElement>(focusableSelector) ?? panel.current)?.focus();

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        settleRef.current(false);
        return;
      }
      if (event.key === "Tab" && panel.current) trapTab(panel.current, event);
    }

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      // Focus goes back to the control that asked, so cancelling leaves the operator
      // exactly where they were.
      previous?.focus?.();
    };
  }, []);

  const danger = request.tone !== "primary";

  return (
    <div className="confirm-layer" role="presentation">
      {/* The scrim answers "no": clicking away from a question is not consent. It is
          a button so that it has a name, and out of the tab order because the dialog
          already has a Cancel. */}
      <button
        className="confirm-scrim"
        onClick={() => onSettle(false)}
        aria-label={request.cancelLabel ?? "Cancel"}
        tabIndex={-1}
      />
      <div
        className={`confirm-dialog ${danger ? "confirm-dialog--danger" : ""}`}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={headingID}
        aria-describedby={bodyID}
        ref={panel}
        tabIndex={-1}
      >
        <div className="confirm-dialog__head">
          {danger && (
            <span className="confirm-dialog__mark" aria-hidden="true">
              <AlertTriangle size={17} />
            </span>
          )}
          <h2 id={headingID}>{request.title}</h2>
        </div>
        <div className="confirm-dialog__body" id={bodyID}>
          {typeof request.body === "string" ? <p>{request.body}</p> : request.body}
        </div>
        {request.detail && <div className="confirm-dialog__detail">{request.detail}</div>}
        <div className="confirm-dialog__actions">
          <Button variant="quiet" ref={cancel} onClick={() => onSettle(false)}>
            {request.cancelLabel ?? "Cancel"}
          </Button>
          <Button variant={danger ? "danger" : "primary"} onClick={() => onSettle(true)}>
            {request.confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
