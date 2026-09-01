import { Component, Fragment, createRef, type ErrorInfo, type ReactNode } from "react";
import { Button } from "./Button";
import { clipboardBlocked, copyText } from "./clipboard";

/** Each mounted boundary needs its own heading id so `aria-labelledby` cannot
 * point at a heading belonging to a different panel. */
let panelCount = 0;

type Scope = "console" | "page";

type State = {
  error: Error | null;
  /** Where the throw happened, in component names. React only hands this to
   * `componentDidCatch`, so it arrives one update after the error itself. */
  stack: string;
  /** What the copy button last did, so the panel can answer without a toast — the
   * notifier lives inside the tree that has just stopped rendering. */
  copied: "" | "done" | "blocked";
  resetKey: number;
};

const emptyState: State = { error: null, stack: "", copied: "", resetKey: 0 };

/** The console renders live payloads from a gateway that may be mid-migration or
 *  mid-outage. Without a boundary, one unexpected null took the whole page to
 *  white with no way back except a manual reload — and no clue what happened.
 *  This keeps the failure on screen, names it, and offers the way out.
 *
 *  Two of these are mounted. The outer one wraps the whole application and is the
 *  last resort. The inner one wraps only the open page, so a page that throws
 *  leaves the navigation, the account menu and the message dock standing — the
 *  operator can walk to another page instead of reloading and losing their place.
 *  `scope` is which of the two this is, and it changes only how much of the screen
 *  the panel claims and what the panel promises still works. */
export class ErrorBoundary extends Component<{ children: ReactNode; scope?: Scope }, State> {
  state: State = emptyState;

  /** Focus is sitting on whatever control was unmounted by the crash, which
   * leaves a keyboard operator on `<body>` with the panel unread. The panel takes
   * focus itself rather than one of its buttons, so nothing destructive is one
   * Enter away, and it installs no Tab trap: a page-level panel has the rest of
   * the console around it to tab into, and the whole-console one has nothing
   * behind it to walk into. */
  private panel = createRef<HTMLElement>();

  private headingID = `crash-panel-heading-${++panelCount}`;

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The stack is the only record an operator can send on, so it is logged
    // rather than swallowed — and, since the console is the thing that broke,
    // also put on screen where it can be copied without opening devtools.
    console.error("Console render failed", error, info.componentStack);
    this.setState({ stack: info.componentStack ?? "" });
    this.panel.current?.focus();
  }

  /** Clearing `error` alone re-rendered the same subtree, which threw again on the
   * spot and re-caught — so the button looked dead. Bumping the key in the same
   * update remounts the children instead, which is what "again" has to mean. */
  private retry = () => {
    this.setState((previous) => ({ ...emptyState, resetKey: previous.resetKey + 1 }));
  };

  private copyReport = () => {
    const { error, stack } = this.state;
    const report = [`Rotakey console error at ${location.pathname}`, error?.stack ?? error?.message ?? "", stack]
      .filter(Boolean)
      .join("\n\n");
    void copyText(report)
      .then(() => this.setState({ copied: "done" }))
      .catch(() => this.setState({ copied: "blocked" }));
  };

  render() {
    const { error, stack, copied, resetKey } = this.state;
    // A keyed Fragment remounts the subtree without adding a wrapper element that
    // every layout below it would then have to account for.
    if (!error) return <Fragment key={resetKey}>{this.props.children}</Fragment>;
    const page = this.props.scope === "page";
    const panel = (
      <section
        className={`crash-panel${page ? " crash-panel--page" : ""}`}
        role="alert"
        aria-labelledby={this.headingID}
        tabIndex={-1}
        ref={this.panel}
      >
        <p className="eyebrow">Console error</p>
        <h1 id={this.headingID}>This screen stopped rendering</h1>
        <p>
          {page
            ? "Routing keeps running and the rest of the console still works — the navigation beside this panel will take you to another page. Retry this screen first, and reload if the failure repeats."
            : "Routing keeps running — the gateway is unaffected. Retry this screen first, and reload the console if the failure repeats."}{" "}
          The details below are what to include in a report.
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
          <div className="crash-panel__report">
            <Button variant="quiet" onClick={this.copyReport}>
              Copy report
            </Button>
            {/* Said in the panel rather than as a toast: the notifier is part of
                the tree that just failed, and on the whole-console boundary it is
                not on screen at all. */}
            {copied === "done" && <span>Copied.</span>}
            {copied === "blocked" && <span>{clipboardBlocked}</span>}
          </div>
          <pre>{error.stack ?? error.message}</pre>
          {/* Which component threw. The message alone names the symptom; this
              names the screen, and it is the first thing anyone reading a report
              asks for. */}
          {stack && <pre>{stack.trim()}</pre>}
        </details>
      </section>
    );
    // The page-level panel is content inside a shell that is still standing, so it
    // must not draw the shell again around itself.
    return page ? panel : <div className="auth-shell">{panel}</div>;
  }
}
