import { Component, type ErrorInfo, type ReactNode } from "react";

/** The console renders live payloads from a gateway that may be mid-migration or
 *  mid-outage. Without a boundary, one unexpected null took the whole page to
 *  white with no way back except a manual reload — and no clue what happened.
 *  This keeps the failure on screen, names it, and offers the reload. */
export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The stack is the only record an operator can send on, so it is logged
    // rather than swallowed.
    console.error("Console render failed", error, info.componentStack);
  }

  render() {
    const { error } = this.state;
    if (!error) return this.props.children;
    return (
      <div className="auth-shell">
        <div className="crash-panel" role="alert">
          <p className="eyebrow">Console error</p>
          <h1>This screen stopped rendering</h1>
          <p>
            The gateway is unaffected — routing keeps running. Reload to get the console back. If it
            happens again, the browser console holds the details worth reporting.
          </p>
          <pre>{error.message}</pre>
          <div className="button-row">
            <button className="button button--primary" onClick={() => location.reload()}>
              Reload console
            </button>
            <button className="button button--quiet" onClick={() => this.setState({ error: null })}>
              Try this screen again
            </button>
          </div>
        </div>
      </div>
    );
  }
}
