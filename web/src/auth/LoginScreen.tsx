/** The whole screen, not a panel on a page. There is one account and one thing to
 *  do here, so the card carries its own reason for being on screen: a session that
 *  expired under the operator is announced on the card rather than in a toast the
 *  card would have replaced. */

import { useState } from "react";
import { Cable } from "lucide-react";
import { api } from "../api";
import { Button, InlineNotice } from "../ui";

export function LoginScreen({
  onLogin,
  notice
}: {
  onLogin: (username: string, csrf: string) => void;
  /** Why the operator is here, when they did not choose to be. Empty on a normal
   * visit; set when the session expired under them. */
  notice?: string;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api<{ csrf_token: string }>("/api/auth/login", {
        method: "POST",
        json: { username, password }
      });
      onLogin(username, result.csrf_token);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "The gateway did not answer. Try signing in again.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-shell">
      <section className="login-panel">
        <div className="wordmark wordmark--auth">
          <span className="wordmark__mark"><Cable size={18} aria-hidden="true" /></span>
          <span><strong>ROTAKEY</strong><small>routing control plane</small></span>
        </div>
        <form className="auth-form" onSubmit={submit}>
          <div>
            <p className="eyebrow">Owner access</p>
            <h1>Sign in to Rotakey.</h1>
            <p>Configure providers, API keys, model routes and limits.</p>
          </div>
          {/* The reason first, then whatever this attempt went wrong with. */}
          {notice && !error && <InlineNotice tone="danger">{notice}</InlineNotice>}
          {error && <InlineNotice tone="danger">{error}</InlineNotice>}
          <label className="field">
            <span>Username</span>
            <input autoFocus autoComplete="username" required value={username} onChange={(e) => setUsername(e.target.value)} />
          </label>
          <label className="field">
            <span>Password</span>
            <input type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </label>
          <Button type="submit" disabled={busy}>{busy ? "Signing in…" : "Sign in"}</Button>
        </form>
      </section>
    </div>
  );
}
