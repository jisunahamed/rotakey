/** The console's one button. It lives in its own file because the confirmation
 * dialog needs it and the sheet needs the confirmation dialog: importing all three
 * through the component barrel would close a cycle.
 */
export function Button({
  children,
  variant = "primary",
  className = "",
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "quiet" | "danger";
  /** React 19 passes a ref straight through as a prop, so no forwardRef wrapper is
   * needed — but the type has to say so. The confirmation dialog focuses its Cancel
   * button on open, which is the only caller that needs this. */
  ref?: React.Ref<HTMLButtonElement>;
}) {
  return (
    <button className={`button button--${variant} ${className}`} {...props}>
      {children}
    </button>
  );
}
