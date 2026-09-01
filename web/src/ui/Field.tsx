import { useId, type ReactNode } from "react";

/** What a control needs so that its error is announced with it. Spread onto the
 *  input, textarea or select.
 *
 *  `aria-invalid` on its own says a field is wrong without saying why, and the
 *  message underneath it is then just text near a red box — a screen reader
 *  moving field to field never reaches it. The two have to travel together, and
 *  every one of the console's fields wired that up by hand, with an id it minted
 *  itself, in three lines that were the same three lines each time. */
export type ControlProps = {
  "aria-invalid"?: true;
  "aria-describedby"?: string;
};

/** A labelled control in a form.
 *
 *  The label wraps the control, so the association is structural: there is no id
 *  to get wrong and no way to leave a control unlabelled. Pass a function as the
 *  child when the field can be invalid — it receives exactly the attributes that
 *  tie the message below to the control above. */
export function Field({
  label,
  hint,
  error,
  children,
  span
}: {
  label: string;
  /** A short qualifier that belongs to the label rather than under it: a unit, a
   *  range, "optional". It is read as part of the label, which is why it says
   *  what the value *is* and never what the field is for — that is the label's
   *  job, and a hint that repeats it is deleted. */
  hint?: string;
  /** What is wrong with what is in the field right now, in a sentence that says
   *  how to fix it. Absent when the field is fine. */
  error?: string;
  children: ReactNode | ((control: ControlProps) => ReactNode);
  /** Makes the field take both columns of a `FieldPair`. */
  span?: boolean;
}) {
  const errorID = useId();
  const control: ControlProps = error
    ? { "aria-invalid": true, "aria-describedby": errorID }
    : {};
  return (
    <label className={`ui-field${span ? " ui-field--span" : ""}`}>
      <span className="ui-field__label">
        {label}
        {hint && <small className="ui-field__hint">{hint}</small>}
      </span>
      {typeof children === "function" ? children(control) : children}
      {error && <small className="ui-field__error" id={errorID}>{error}</small>}
    </label>
  );
}

/** Two or three fields on one line, for values that are read together — a rate
 *  limit and its window, an input price and an output price. It collapses to one
 *  column on a narrow screen rather than shrinking both past legibility. */
export function FieldPair({ children, columns = 2 }: { children: ReactNode; columns?: 2 | 3 }) {
  return <div className={`ui-field-pair ui-field-pair--${columns}`}>{children}</div>;
}

/** Fields stacked down a form section. */
export function FieldStack({ children }: { children: ReactNode }) {
  return <div className="ui-field-stack">{children}</div>;
}

/** A setting: what it does on the left, the control on the right.
 *
 *  This is the other shape a labelled control takes, and it is not a variant of
 *  `Field` — it is a row in a list of settings, where the sentence explaining the
 *  consequence is as important as the control and needs the width to say it. The
 *  control keeps a floor of its own so that a select holding "Model-wise
 *  (pooled)" is not squeezed into the slot that was sized for a four-digit
 *  number. */
export function FieldRow({
  label,
  description,
  children,
  error,
  wide = false
}: {
  label: string;
  /** What changes when this is changed, and what happens if it is left alone.
   *  Two sentences at most. */
  description?: string;
  children: ReactNode | ((control: ControlProps) => ReactNode);
  error?: string;
  /** For a control that needs more than the standard slot — a segmented control,
   *  a pair of inputs. */
  wide?: boolean;
}) {
  const errorID = useId();
  const control: ControlProps = error
    ? { "aria-invalid": true, "aria-describedby": errorID }
    : {};
  return (
    <label className={`ui-field-row${wide ? " ui-field-row--wide" : ""}`}>
      <span className="ui-field-row__text">
        <strong>{label}</strong>
        {description && <small>{description}</small>}
        {error && <small className="ui-field__error" id={errorID}>{error}</small>}
      </span>
      <span className="ui-field-row__control">
        {typeof children === "function" ? children(control) : children}
      </span>
    </label>
  );
}
