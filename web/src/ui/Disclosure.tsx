import { useId, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";

/** A section that folds away.
 *
 *  Five of these existed and none of them was a heading, so a screen reader user
 *  moving by headings through a provider skipped straight past "API keys" and
 *  "Model routes" — the two things the panel is for. The title here is a real
 *  heading with the button inside it, which is the shape that keeps both: the
 *  section is findable, and it still opens on Enter.
 *
 *  `level` is required for the same reason it is on `SectionHeader` — it is the
 *  section's depth in the document, not a size. */
export function Disclosure({
  title,
  level,
  subtitle,
  meta,
  actions,
  open,
  onToggle,
  children
}: {
  title: string;
  level: 2 | 3 | 4;
  /** One quiet line under the title: what is in here when it is closed. */
  subtitle?: string;
  /** A fact, set as a figure — "3 keys", "2 need attention". It stays visible
   *  when the section is folded, which is the point of folding it. */
  meta?: ReactNode;
  /** Controls that act on the section, beside the title rather than inside the
   *  toggle: a button nested in a button is not a thing a browser can render. */
  actions?: ReactNode;
  open: boolean;
  onToggle: () => void;
  children: ReactNode;
}) {
  const bodyID = useId();
  const Heading = `h${level}` as const;
  return (
    <section className={`ui-disclosure${open ? " is-open" : ""}`}>
      <div className="ui-disclosure__head">
        <Heading className="ui-disclosure__heading">
          <button
            type="button"
            className="ui-disclosure__toggle"
            aria-expanded={open}
            aria-controls={bodyID}
            onClick={onToggle}
          >
            <ChevronDown className="ui-disclosure__chevron" size={16} aria-hidden="true" />
            <span className="ui-disclosure__text">
              <strong>{title}</strong>
              {subtitle && <small>{subtitle}</small>}
            </span>
            {meta !== undefined && <span className="ui-disclosure__meta">{meta}</span>}
          </button>
        </Heading>
        {actions && <div className="ui-disclosure__actions">{actions}</div>}
      </div>
      {/* Kept in the document and hidden, rather than unmounted: these hold forms,
          and a key half-typed into a folded section is work the operator did. */}
      <div id={bodyID} className="ui-disclosure__body" hidden={!open}>{children}</div>
    </section>
  );
}
