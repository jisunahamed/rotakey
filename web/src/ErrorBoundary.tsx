import { Component, Fragment, createRef, type ErrorInfo, type ReactNode } from "react";
import { Button } from "./Button";

/** Each mounted boundary needs its own heading id so `aria-labelledby` cannot
 * point at a heading belonging to a different panel. */
let panelCount = 0;

/** The console renders live payloads from a gateway that may be mid-migration or
 *  mid-outage. Without a boundary, one unexpected null took the whole page to
 *  white with no way back except a manual reload — and no clue what happened.
 *  This keeps the failure on screen, names it, and offers the way out. */
export class ErrorBoundary extends Component<
  { children: ReactNode },
  { error: Error | null; resetKey: number }
> {
  state: { error: Error | null; resetKey: number } = { error: null, resetKey: 0 };

  /** Focus is sitting on whatever control was unmounted by the crash, which
   * leaves a keyboard operator on `<body>` with the panel unread. The panel takes
   * focus itself rather than one of its buttons, so nothing destructive is one
   * Enter away, and it installs no Tab trap: this is the whole page now, so there
   * is nothing behind it to walk into. */
  private panel = createRef<HTMLElement>();

  private headingID = `crash-panel-heading-${++panelCount}`;

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The stack is the only record an operator can send on, so it is logged
    // rather than swallowed.
    console.error("Console render failed", error, info.componentStack);
    this.panel.current?.focus();
  }

  /** Clearing `error` alone re-rendered the same subtree, which threw again on the
   * spot and re-caught — so the button looked dead. Bumping the key in the same
   * update remounts the children instead, which is what "again" has to mean. */
  private retry = () => {
    this.setState((previous) => ({ error: null, resetKey: previous.resetKey + 1 }));
  };

  render() {
    const { error, resetKey } = this.state;
    // A keyed Fragment remounts the subtree without adding a wrapper element that
    // every layout below it would then have to account for.
    if (!error) return <Fragment key={resetKey}>{this.props.children}</Fragment>;
    return (
      <div className="auth-shell">
        <section
          className="crash-panel"
          role="alert"
          aria-labelledby={this.headingID}
          tabIndex={-1}
          ref={this.panel}
        >
          <p className="eyebrow">Console error</p>
          <h1 id={this.headingID}>This screen stopped rendering</h1>
          <p>
            Routing keeps running — the gateway is unaffected. Retry this screen first, and reload
            the console if the failure repeats. The details below are what to include in a report.
          </p>
          <div className="button-row">
            <Button onClick={this.retry}>Try this screen again</Button>
            <Button variant="quiet" onClick={() => location.reload()}>
              Reload console
            </Button>
          </div>
          {/* The error text stays available but sits under the actions: the operator
              needs the way out before they need the stack. */}
          <details>
            <summary>Error detail</summary>
            <pre>{error.message}</pre>
          </details>
        </section>
      </div>
    );
  }
}
