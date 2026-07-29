import type { ReactNode } from "react";
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

export function StatusDot({ state }: { state: string }) {
  return <span className={`status-dot status-dot--${state}`} aria-label={state} />;
}

export function EmptyState({
  title,
  description,
  action
}: {
  title: string;
  description: string;
  action?: ReactNode;
}) {
  return (
    <div className="empty-state">
      <div className="empty-state__line" aria-hidden="true" />
      <h3>{title}</h3>
      <p>{description}</p>
      {action}
    </div>
  );
}

export function Sheet({
  title,
  eyebrow,
  onClose,
  children,
  actions,
  wide = false
}: {
  title: string;
  eyebrow?: string;
  onClose: () => void;
  children: ReactNode;
  actions?: ReactNode;
  wide?: boolean;
}) {
  return (
    <div className="sheet-layer" role="presentation">
      <button className="sheet-scrim" onClick={onClose} aria-label="Close panel" />
      <section className={`sheet ${wide ? "sheet--wide" : ""}`} role="dialog" aria-modal="true">
        <header className="sheet__header">
          <div>
            {eyebrow && <p className="eyebrow">{eyebrow}</p>}
            <h2>{title}</h2>
          </div>
          <div className="button-row">
            {actions}
            <button className="icon-button" onClick={onClose} aria-label="Close">
              <X size={18} />
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
  return (
    <div className={`rate-grid ${compact ? "rate-grid--compact" : ""}`}>
      {limitFields.map(([key, label, description]) => (
        <label className="field" key={key}>
          <span>
            {label}
            {!compact && <small>{description}</small>}
          </span>
          <input
            type="number"
            min={1}
            step={1}
            placeholder="No limit"
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
  return <div className={`notice notice--${tone}`}>{children}</div>;
}
