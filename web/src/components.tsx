import { useEffect, useId, useRef, type ReactNode } from "react";
import { X } from "lucide-react";
import type { RatePolicy } from "./types";

export function Button({
  children,
  variant = "primary",
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "quiet" | "danger";
}) {
  return (
    <button className={`button button--${variant} ${className}`} {...props}>
      {children}
    </button>
  );
}

/** The dot is decorative next to the label it sits beside, so it announces the
 * state in words rather than exposing the raw enum as a bare label. */
const statusPhrase: Record<string, string> = {
  healthy: "Healthy",
  ready: "Ready",
  live: "Live",
  cooldown: "In cooldown",
  quarantined: "Quarantined",
  throttled: "Throttled",
  partial: "Partly ready",
  exhausted: "Out of balance",
  disabled: "Off",
  idle: "Idle",
  failed: "Failed",
  unavailable: "Unavailable",
  unverified: "Not checked yet"
};

export function statusLabel(state: string) {
  return statusPhrase[state] ?? state.replaceAll("_", " ");
}

export function StatusDot({ state }: { state: string }) {
  const phrase = statusLabel(state);
  return (
    <span className={`status-dot status-dot--${state}`} role="img" aria-label={phrase} />
  );
}

export function EmptyState({
  title,
  description,
  action,
  level = 3
}: {
  title: string;
  description: string;
  action?: ReactNode;
  /** The empty state stands in for whatever should have been here, so its heading
   * has to sit at that spot's depth. A page-level empty follows the h1 directly
   * and must be an h2; one inside a panel that already has an h2 stays an h3.
   * Skipping a level breaks heading navigation for screen reader users. */
  level?: 2 | 3;
}) {
  const Heading = level === 2 ? "h2" : "h3";
  return (
    <div className="empty-state">
      <div className="empty-state__line" aria-hidden="true" />
      <Heading>{title}</Heading>
      <p>{description}</p>
      {action}
    </div>
  );
}

const focusableSelector =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

export function Sheet({
  title,
  eyebrow,
  onClose,
  children,
  actions,
  wide = false,
  dirty = false,
  discardMessage = "Close this panel? Anything typed here will be lost."
}: {
  title: string;
  eyebrow?: string;
  onClose: () => void;
  children: ReactNode;
  actions?: ReactNode;
  wide?: boolean;
  /** Set once the operator has typed something, so a stray backdrop click or an
   * Escape keypress cannot silently discard a half-filled form. */
  dirty?: boolean;
  discardMessage?: string;
}) {
  const headingID = useId();
  const panel = useRef<HTMLElement | null>(null);
  const closeRef = useRef(onClose);
  const dirtyRef = useRef(dirty);
  const messageRef = useRef(discardMessage);
  closeRef.current = onClose;
  dirtyRef.current = dirty;
  messageRef.current = discardMessage;

  // Escape closes, Tab stays inside the panel, and focus lands in the panel on
  // open. Without the trap, tabbing walks the page behind an aria-modal dialog,
  // which leaves keyboard operators editing controls they cannot see.
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    const node = panel.current;
    const first = node?.querySelector<HTMLElement>(focusableSelector);
    (first ?? node)?.focus();

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        if (dirtyRef.current && !window.confirm(messageRef.current)) return;
        closeRef.current();
        return;
      }
      if (event.key !== "Tab" || !panel.current) return;
      const focusable = Array.from(
        panel.current.querySelectorAll<HTMLElement>(focusableSelector)
      ).filter((element) => element.offsetParent !== null || element === document.activeElement);
      if (focusable.length === 0) return;
      const edge = event.shiftKey ? focusable[0] : focusable[focusable.length - 1];
      if (document.activeElement === edge) {
        event.preventDefault();
        (event.shiftKey ? focusable[focusable.length - 1] : focusable[0]).focus();
      }
    }

    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      previous?.focus?.();
    };
  }, []);

  function requestClose() {
    if (dirty && !window.confirm(discardMessage)) return;
    onClose();
  }

  return (
    <div className="sheet-layer" role="presentation">
      <button className="sheet-scrim" onClick={requestClose} aria-label="Close panel" tabIndex={-1} />
      <section
        className={`sheet ${wide ? "sheet--wide" : ""}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingID}
        ref={panel}
        tabIndex={-1}
      >
        <header className="sheet__header">
          <div>
            {eyebrow && <p className="eyebrow">{eyebrow}</p>}
            <h2 id={headingID}>{title}</h2>
          </div>
          <div className="button-row">
            {actions}
            <button className="icon-button" onClick={requestClose} aria-label="Close">
              <X size={18} aria-hidden="true" />
            </button>
          </div>
        </header>
        <div className="sheet__body">{children}</div>
      </section>
    </div>
  );
}

const limitFields: Array<[keyof RatePolicy, string, string]> = [
  ["rps", "RPS", "Requests / second"],
  ["rpm", "RPM", "Requests / minute"],
  ["rpd", "RPD", "Requests / day"],
  ["tps", "TPS", "Tokens / second"],
  ["tpm", "TPM", "Tokens / minute"],
  ["tpd", "TPD", "Tokens / day"],
  ["tpr", "TPR", "Tokens / request"]
];

export function RateFields({
  value,
  onChange,
  compact = false
}: {
  value: RatePolicy;
  onChange: (next: RatePolicy) => void;
  compact?: boolean;
}) {
  const group = useId();
  return (
    <div className={`rate-grid ${compact ? "rate-grid--compact" : ""}`}>
      {limitFields.map(([key, label, description]) => (
        <label className="field" key={key}>
          <span>
            {label}
            {!compact && <small id={`${group}-${key}`}>{description}</small>}
          </span>
          <input
            type="number"
            min={1}
            step={1}
            inputMode="numeric"
            placeholder="No limit"
            aria-label={`${label}, ${description}`}
            aria-describedby={compact ? undefined : `${group}-${key}`}
            value={value[key] ?? ""}
            onChange={(event) => {
              const raw = event.target.value;
              onChange({ ...value, [key]: raw === "" ? null : Number(raw) });
            }}
          />
        </label>
      ))}
    </div>
  );
}

export function Toggle({
  checked,
  onChange,
  label,
  description
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
  description?: string;
}) {
  return (
    <label className="toggle-row">
      <span>
        <strong>{label}</strong>
        {description && <small>{description}</small>}
      </span>
      <input
        className="toggle-input"
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
    </label>
  );
}

export function InlineNotice({
  tone = "info",
  children
}: {
  tone?: "info" | "danger" | "success";
  children: ReactNode;
}) {
  // A failure has to interrupt a screen reader; an informational note must not.
  return (
    <div
      className={`notice notice--${tone}`}
      role={tone === "danger" ? "alert" : "status"}
    >
      {children}
    </div>
  );
}
