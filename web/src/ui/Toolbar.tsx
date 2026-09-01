import { Search } from "lucide-react";
import type { ReactNode, Ref } from "react";

/** The strip above a list: find something, then narrow what is left.
 *
 *  Five of these existed and no two were the same height, so moving between the
 *  models list and the request log shifted every row under them by a few pixels
 *  — the kind of difference nobody can name and everybody feels. */
export function Toolbar({
  children,
  label
}: {
  children: ReactNode;
  /** What this strip filters, for a screen reader. */
  label?: string;
}) {
  return (
    <div className="ui-toolbar" role={label ? "group" : undefined} aria-label={label}>
      {children}
    </div>
  );
}

/** The search box, with its picture attached.
 *
 *  The magnifier is part of the control rather than a sibling of it, because in
 *  three of the five strips it was a sibling and in two of those it was in a grid
 *  track of its own that a narrow screen collapsed — leaving an unlabelled text
 *  box with no indication of what typing in it would do.
 *
 *  Takes a ref so `useFilterHotkey` can put the cursor here on `/`. */
export function SearchInput({
  value,
  onChange,
  placeholder,
  label,
  ref
}: {
  value: string;
  onChange: (value: string) => void;
  /** An example of what can be typed here, not a restatement of the label. */
  placeholder?: string;
  /** What is being searched: "Filter models", "Filter requests". Visually hidden
   *  — the magnifier says "search" to anyone who can see it, and says nothing at
   *  all to anyone who cannot. */
  label: string;
  ref?: Ref<HTMLInputElement>;
}) {
  return (
    <span className="ui-search">
      <Search className="ui-search__icon" size={14} aria-hidden="true" />
      <input
        ref={ref}
        className="ui-search__input"
        type="search"
        value={value}
        onChange={(event) => onChange(event.target.value)}
        placeholder={placeholder}
        aria-label={label}
      />
    </span>
  );
}
