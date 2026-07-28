import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  Cable,
  Check,
  ChevronRight,
  CircleGauge,
  Clipboard,
  Database,
  FileClock,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  Plus,
  RefreshCw,
  Route,
  Settings as SettingsIcon,
  ShieldCheck,
  Sun,
  Trash2,
  X
} from "lucide-react";
import { api, APIError, setCSRF } from "./api";
import {
  Button,
  EmptyState,
  InlineNotice,
  RateFields,
  Sheet,
  StatusDot,
  Toggle
} from "./components";
import {
  emptyPolicy,
  type Credential,
  type ModelRoute,
  type Overview,
  type Provider,
  type RatePolicy,
  type RequestLog,
  type Settings
} from "./types";

type Page = "overview" | "providers" | "models" | "logs" | "access" | "settings";
type AuthPhase = "loading" | "setup" | "login" | "app";

const navItems: Array<{ id: Page; label: string; icon: typeof Activity }> = [
  { id: "overview", label: "Overview", icon: CircleGauge },
  { id: "providers", label: "Providers", icon: Database },
  { id: "models", label: "Model routes", icon: Route },
  { id: "logs", label: "Request logs", icon: FileClock },
  { id: "access", label: "Access key", icon: KeyRound },
  { id: "settings", label: "System", icon: SettingsIcon }
];

const pageFromLocation = (): Page => {
  const segment = location.pathname.replace(/^\/admin\/?/, "").split("/")[0] as Page;
  return navItems.some((item) => item.id === segment) ? segment : "overview";
};

function App() {
  const [phase, setPhase] = useState<AuthPhase>("loading");
  const [username, setUsername] = useState("");
  const [page, setPage] = useState<Page>(pageFromLocation);
  const [menuOpen, setMenuOpen] = useState(false);
  const [theme, setTheme] = useState<"light" | "dark" | "system">(() => {
    const stored = localStorage.getItem("relay-theme");
    return stored === "light" || stored === "dark" ? stored : "system";
  });
  const [gatewayKey, setGatewayKey] = useState("");
  const [toast, setToast] = useState<{ tone: "success" | "danger"; message: string } | null>(null);

  const notify = useCallback((message: string, tone: "success" | "danger" = "success") => {
    setToast({ message, tone });
    window.setTimeout(() => setToast(null), 4200);
  }, []);

  const checkSession = useCallback(async () => {
    try {
      const setup = await api<{ setup_required: boolean }>("/api/setup/status");
      if (setup.setup_required) {
        setPhase("setup");
        return;
      }
      const session = await api<{ username: string; csrf_token: string }>("/api/auth/session");
      setCSRF(session.csrf_token);
      setUsername(session.username);
      setPhase("app");
    } catch {
      setPhase("login");
    }
  }, []);

  useEffect(() => {
    void checkSession();
    const expired = () => {
      setCSRF("");
      setPhase("login");
      notify("Your session expired. Sign in again.", "danger");
    };
    window.addEventListener("relay:session-expired", expired);
    return () => window.removeEventListener("relay:session-expired", expired);
  }, [checkSession, notify]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem("relay-theme", theme);
  }, [theme]);

  useEffect(() => {
    const onPopState = () => setPage(pageFromLocation());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = (next: Page) => {
    setPage(next);
    setMenuOpen(false);
    history.pushState({}, "", `/admin/${next}`);
  };

  const logout = async () => {
    try {
      await api<void>("/api/auth/logout", { method: "POST" });
    } finally {
      setCSRF("");
      setPhase("login");
    }
  };

  if (phase === "loading") {
    return <LoadingScreen />;
  }
  if (phase === "setup") {
    return (
      <SetupScreen
        onComplete={(name, key, csrf) => {
          setUsername(name);
          setGatewayKey(key);
          setCSRF(csrf);
          setPhase("app");
        }}
      />
    );
  }
  if (phase === "login") {
    return (
      <LoginScreen
        onLogin={(name, csrf) => {
          setUsername(name);
          setCSRF(csrf);
          setPhase("app");
        }}
      />
    );
  }

  return (
    <div className="app-shell">
      <button
        className={`mobile-scrim ${menuOpen ? "is-visible" : ""}`}
        onClick={() => setMenuOpen(false)}
        aria-label="Close navigation"
      />
      <aside className={`sidebar ${menuOpen ? "is-open" : ""}`}>
        <div className="wordmark">
          <span className="wordmark__mark" aria-hidden="true">
            <Cable size={18} />
          </span>
          <span>
            <strong>ROTAKEY</strong>
            <small>routing control plane</small>
          </span>
        </div>
        <nav aria-label="Primary navigation">
          {navItems.map(({ id, label, icon: Icon }) => (
            <button
              key={id}
              className={`nav-item ${page === id ? "is-active" : ""}`}
              onClick={() => navigate(id)}
              aria-current={page === id ? "page" : undefined}
            >
              <Icon size={17} />
              <span>{label}</span>
            </button>
          ))}
        </nav>
        <div className="sidebar__footer">
          <div className="operator">
            <span className="operator__avatar">{username.slice(0, 1).toUpperCase()}</span>
            <span>
              <strong>{username}</strong>
              <small>Owner</small>
            </span>
          </div>
          <button className="icon-button" onClick={() => void logout()} aria-label="Sign out">
            <LogOut size={17} />
          </button>
        </div>
      </aside>

      <div className="workspace">
        <header className="mobile-header">
          <button className="icon-button" onClick={() => setMenuOpen(true)} aria-label="Open navigation">
            <Menu size={19} />
          </button>
          <strong>Rotakey</strong>
          <ThemeButton theme={theme} setTheme={setTheme} />
        </header>
        <main className="main-pane">
          {page === "overview" && <OverviewPage navigate={navigate} notify={notify} />}
          {page === "providers" && <ProvidersPage notify={notify} />}
          {page === "models" && <ModelsPage notify={notify} />}
          {page === "logs" && <LogsPage notify={notify} />}
          {page === "access" && (
            <AccessPage
              gatewayKey={gatewayKey}
              onNewKey={setGatewayKey}
              notify={notify}
            />
          )}
          {page === "settings" && <SettingsPage notify={notify} />}
        </main>
        <div className="desktop-theme">
          <ThemeButton theme={theme} setTheme={setTheme} />
        </div>
      </div>

      {gatewayKey && (
        <SecretReveal
          title="Save your gateway key"
          keyValue={gatewayKey}
          message="This is the only time the active key is displayed. Store it in your application secret manager."
          onClose={() => setGatewayKey("")}
          notify={notify}
        />
      )}
      {toast && (
        <div className={`toast toast--${toast.tone}`} role="status">
          {toast.tone === "success" ? <Check size={16} /> : <X size={16} />}
          {toast.message}
        </div>
      )}
    </div>
  );
}

function LoadingScreen() {
  return (
    <div className="auth-shell">
      <div className="boot-sequence" aria-label="Loading Rotakey">
        <Cable size={24} />
        <div className="boot-line">
          <span />
        </div>
        <p>Checking control plane</p>
      </div>
    </div>
  );
}

function SetupScreen({
  onComplete
}: {
  onComplete: (username: string, key: string, csrf: string) => void;
}) {
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [bootstrap, setBootstrap] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (event: React.FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await api<{ gateway_key: string; csrf_token: string }>("/api/setup", {
        method: "POST",
        headers: { "X-Bootstrap-Token": bootstrap },
        json: { username, password }
      });
      onComplete(username, result.gateway_key, result.csrf_token);
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Setup failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-shell">
      <section className="auth-panel auth-panel--setup">
        <div className="auth-panel__intro">
          <p className="eyebrow">First run · owner setup</p>
          <h1>One gateway.<br />Every configured model.</h1>
          <p>
            Create the only admin account, then save the generated gateway key. Providers remain
            private behind a single model-wise API.
          </p>
          <div className="setup-rail" aria-hidden="true">
            <span className="is-active">Owner</span>
            <span>Gateway key</span>
            <span>Provider</span>
          </div>
        </div>
        <form className="auth-form" onSubmit={submit}>
          <div>
            <p className="eyebrow">Secure bootstrap</p>
            <h2>Create owner account</h2>
          </div>
          {error && <InlineNotice tone="danger">{error}</InlineNotice>}
          <label className="field">
            <span>Bootstrap token <small>From your VPS .env file</small></span>
            <input
              type="password"
              autoComplete="off"
              required
              minLength={24}
              value={bootstrap}
              onChange={(event) => setBootstrap(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Username</span>
            <input
              autoComplete="username"
              required
              minLength={3}
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label className="field">
            <span>Password <small>At least 12 characters</small></span>
            <input
              type="password"
              autoComplete="new-password"
              required
              minLength={12}
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          <Button type="submit" disabled={busy}>
            {busy ? "Securing control plane…" : "Create owner and gateway key"}
          </Button>
        </form>
      </section>
    </div>
  );
}

function LoginScreen({
  onLogin
}: {
  onLogin: (username: string, csrf: string) => void;
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
      setError(caught instanceof Error ? caught.message : "Sign in failed.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-shell">
      <section className="login-panel">
        <div className="wordmark wordmark--auth">
          <span className="wordmark__mark"><Cable size={18} /></span>
          <span><strong>ROTAKEY</strong><small>routing control plane</small></span>
        </div>
        <form className="auth-form" onSubmit={submit}>
          <div>
            <p className="eyebrow">Owner access</p>
            <h1>Inspect the next route.</h1>
            <p>Sign in to configure providers, credentials, models and limits.</p>
          </div>
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

function ThemeButton({
  theme,
  setTheme
}: {
  theme: "light" | "dark" | "system";
  setTheme: (theme: "light" | "dark" | "system") => void;
}) {
  const next = theme === "system" ? "dark" : theme === "dark" ? "light" : "system";
  const Icon = theme === "dark" ? Moon : theme === "light" ? Sun : Activity;
  return (
    <button className="icon-button" onClick={() => setTheme(next)} aria-label={`Theme: ${theme}`}>
      <Icon size={17} />
    </button>
  );
}

function PageHeader({
  eyebrow,
  title,
  description,
  actions
}: {
  eyebrow: string;
  title: string;
  description: string;
  actions?: React.ReactNode;
}) {
  return (
    <header className="page-header">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h1>{title}</h1>
        <p>{description}</p>
      </div>
      {actions && <div className="page-header__actions">{actions}</div>}
    </header>
  );
}

function OverviewPage({
  navigate,
  notify
}: {
  navigate: (page: Page) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const [overview, setOverview] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const load = useCallback(async () => {
    setError("");
    try {
      setOverview(await api<Overview>("/api/admin/overview"));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Overview could not be loaded.");
    }
  }, []);
  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 15000);
    return () => window.clearInterval(timer);
  }, [load]);

  if (!overview && !error) return <PageSkeleton />;
  return (
    <>
      <PageHeader
        eyebrow="Live routing state"
        title="Can the next request run?"
        description="Capacity is shown model-first. Provider details stay behind the route."
        actions={
          <Button variant="quiet" onClick={() => void load()}><RefreshCw size={15} /> Refresh</Button>
        }
      />
      {error && <InlineNotice tone="danger">{error}</InlineNotice>}
      {overview && (
        <>
          <section className="base-url-strip">
            <div>
              <span className="signal-light" />
              <span>
                <small>Unified base URL</small>
                <code>{overview.base_url || `${location.origin}/v1`}</code>
              </span>
            </div>
            <Button
              variant="quiet"
              onClick={() => {
                void navigator.clipboard.writeText(overview.base_url || `${location.origin}/v1`)
                  .then(() => notify("Base URL copied."))
                  .catch(() => notify("Clipboard access was blocked.", "danger"));
              }}
            >
              <Clipboard size={15} /> Copy
            </Button>
          </section>
          <section className="usage-line" aria-label="Last 24 hours usage">
            <div><span>Requests</span><strong>{formatNumber(overview.usage.requests_24h)}</strong></div>
            <div><span>Tokens</span><strong>{formatNumber(overview.usage.tokens_24h)}</strong></div>
            <div>
              <span>Errors</span>
              <strong className={overview.usage.errors_24h ? "text-danger" : ""}>
                {formatNumber(overview.usage.errors_24h)}
              </strong>
            </div>
            <div><span>Wait ceiling</span><strong>{overview.settings.max_wait_ms / 1000}s</strong></div>
          </section>
          <section className="section-block">
            <div className="section-heading">
              <div><p className="eyebrow">Capacity rail</p><h2>Public model routes</h2></div>
              <span>{overview.routes.length} configured</span>
            </div>
            {overview.routes.length === 0 ? (
              <EmptyState
                title="No model routes yet"
                description="Add a provider, map a public model alias, then attach at least one credential."
                action={<Button onClick={() => navigate("providers")}><Plus size={15} /> Add first provider</Button>}
              />
            ) : (
              <div className="capacity-list">
                {overview.routes.map((route) => <CapacityRail key={route.id} route={route} />)}
              </div>
            )}
          </section>
        </>
      )}
    </>
  );
}

function CapacityRail({ route }: { route: Overview["routes"][number] }) {
  const state = !route.enabled
    ? "disabled"
    : route.healthy_credentials === 0
      ? "exhausted"
      : route.healthy_credentials < route.total_credentials
        ? "partial"
        : "healthy";
  return (
    <article className="capacity-row">
      <div className="capacity-row__identity">
        <StatusDot state={state} />
        <span><code>{route.alias}</code><small>{route.provider}</small></span>
      </div>
      <div className="capacity-track" aria-label={`${route.healthy_credentials} of ${route.total_credentials} credentials healthy`}>
        {route.segments.length === 0 ? (
          <div className="credential-segment credential-segment--empty">
            <strong>No credentials</strong>
            <small>Add a key to serve this route</small>
          </div>
        ) : route.segments.map((segment) => (
          <div
            key={segment.id}
            className={`credential-segment credential-segment--${segment.status}${segment.cursor ? " is-cursor" : ""}`}
            title={`${segment.label}: ${segment.status}`}
          >
            <div className="credential-segment__label">
              <strong>{segment.label}</strong>
              {segment.cursor && <span>next</span>}
            </div>
            <small>
              {segment.request_headroom
                ? `${segment.request_headroom.remaining}/${segment.request_headroom.limit} ${segment.request_headroom.dimension}`
                : "∞ requests"}
              {" · "}
              {segment.token_headroom
                ? `${segment.token_headroom.remaining}/${segment.token_headroom.limit} ${segment.token_headroom.dimension}`
                : "∞ tokens"}
            </small>
          </div>
        ))}
      </div>
      <div className="capacity-row__numbers">
        <span><strong>{route.healthy_credentials}/{route.total_credentials}</strong> keys ready</span>
        <span><strong>{route.requests_24h}</strong> requests</span>
        <span className={route.errors_24h ? "text-danger" : ""}><strong>{route.errors_24h}</strong> errors</span>
      </div>
    </article>
  );
}

function ProvidersPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [selectedID, setSelectedID] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [panel, setPanel] = useState<null | { type: "wizard" } | { type: "provider"; provider: Provider } | { type: "model"; provider: Provider; model?: ModelRoute } | { type: "credential"; provider: Provider; credential?: Credential }>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      setProviders(result.providers);
      setSelectedID((current) => current || result.providers[0]?.id || "");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Providers could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const selected = providers.find((provider) => provider.id === selectedID);
  const complete = (message: string) => {
    setPanel(null);
    notify(message);
    void load();
  };

  return (
    <>
      <PageHeader
        eyebrow="Upstream setup"
        title="Providers"
        description="Setup stays provider-wise. Every application still calls one model-wise gateway."
        actions={<Button onClick={() => setPanel({ type: "wizard" })}><Plus size={15} /> Add provider</Button>}
      />
      {error && <InlineNotice tone="danger">{error}</InlineNotice>}
      {loading ? <PageSkeleton /> : providers.length === 0 ? (
        <EmptyState
          title="Connect the first upstream"
          description="You will define its base URL, model aliases, credentials and limits before enabling traffic."
          action={<Button onClick={() => setPanel({ type: "wizard" })}><Plus size={15} /> Start provider setup</Button>}
        />
      ) : (
        <div className="resource-layout">
          <section className="resource-list" aria-label="Providers">
            {providers.map((provider) => {
              const healthy = provider.credentials.filter((credential) => credential.enabled && credential.status === "healthy").length;
              return (
                <button
                  key={provider.id}
                  className={`resource-item ${selectedID === provider.id ? "is-selected" : ""}`}
                  onClick={() => setSelectedID(provider.id)}
                >
                  <StatusDot state={!provider.enabled ? "disabled" : healthy ? "healthy" : "exhausted"} />
                  <span><strong>{provider.name}</strong><small>{provider.base_url}</small></span>
                  <span className="resource-item__count">{provider.models.length} models</span>
                  <ChevronRight size={15} />
                </button>
              );
            })}
          </section>
          {selected && (
            <section className="resource-inspector">
              <div className="inspector-header">
                <div>
                  <p className="eyebrow">{selected.slug}</p>
                  <h2>{selected.name}</h2>
                  <code>{selected.base_url}</code>
                </div>
                <div className="button-row">
                  <Button variant="quiet" onClick={() => void testProvider(selected, notify)}><Activity size={15} /> Test</Button>
                  <Button variant="quiet" onClick={() => setPanel({ type: "provider", provider: selected })}>Edit</Button>
                </div>
              </div>
              <div className="inspector-stats">
                <span><strong>{selected.models.length}</strong> public routes</span>
                <span><strong>{selected.credentials.length}</strong> credentials</span>
                <span><strong>{selected.timeout_seconds}s</strong> timeout</span>
              </div>
              <ResourceSection
                title="Model routes"
                description="Aliases visible through the unified /v1 API."
                action={<Button variant="quiet" onClick={() => setPanel({ type: "model", provider: selected })}><Plus size={14} /> Route</Button>}
              >
                {selected.models.length === 0 ? (
                  <p className="inline-empty">No route can receive traffic yet.</p>
                ) : (
                  <div className="dense-table">
                    {selected.models.map((model) => (
                      <button key={model.id} className="dense-row" onClick={() => setPanel({ type: "model", provider: selected, model })}>
                        <StatusDot state={model.enabled ? "healthy" : "disabled"} />
                        <span><code>{model.public_alias}</code><small>→ {model.upstream_model}</small></span>
                        <span>{model.supports_responses ? "Responses native" : "Responses translated"}</span>
                        <ChevronRight size={14} />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceSection>
              <ResourceSection
                title="Credential pool"
                description="Round-robin skips credentials that cannot reserve every active limit."
                action={<Button variant="quiet" onClick={() => setPanel({ type: "credential", provider: selected })}><Plus size={14} /> Credentials</Button>}
              >
                {selected.credentials.length === 0 ? (
                  <p className="inline-empty">No key is available for this provider.</p>
                ) : (
                  <div className="dense-table">
                    {selected.credentials.map((credential) => (
                      <button key={credential.id} className="dense-row" onClick={() => setPanel({ type: "credential", provider: selected, credential })}>
                        <StatusDot state={credential.status} />
                        <span><strong>{credential.label}</strong><small>•••• {credential.secret_suffix}</small></span>
                        <LimitSummary policy={credential.limits} />
                        <ChevronRight size={14} />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceSection>
              <div className="danger-zone">
                <div><strong>Delete provider</strong><small>Routes, credentials and their policies are removed.</small></div>
                <Button
                  variant="danger"
                  onClick={() => {
                    if (confirm(`Delete ${selected.name} and every route/credential under it?`)) {
                      void api<void>(`/api/admin/providers/${selected.id}`, { method: "DELETE" })
                        .then(() => complete("Provider deleted."))
                        .catch((caught) => notify(errorMessage(caught), "danger"));
                    }
                  }}
                ><Trash2 size={14} /> Delete</Button>
              </div>
            </section>
          )}
        </div>
      )}
      {panel?.type === "wizard" && <ProviderWizard onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "provider" && <ProviderForm provider={panel.provider} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "model" && <ModelForm provider={panel.provider} model={panel.model} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "credential" && <CredentialForm provider={panel.provider} credential={panel.credential} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
    </>
  );
}

function ResourceSection({ title, description, action, children }: { title: string; description: string; action: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="inspector-section">
      <header><div><h3>{title}</h3><p>{description}</p></div>{action}</header>
      {children}
    </section>
  );
}

function LimitSummary({ policy }: { policy: RatePolicy }) {
  const active = Object.entries(policy).filter(([, value]) => value !== null);
  if (!active.length) return <span className="muted">No limits</span>;
  return <span className="mono-summary">{active.slice(0, 2).map(([key, value]) => `${key.toUpperCase()} ${value}`).join(" · ")}{active.length > 2 ? ` +${active.length - 2}` : ""}</span>;
}

async function testProvider(provider: Provider, notify: (message: string, tone?: "success" | "danger") => void) {
  try {
    const result = await api<{ ok: boolean; status_code: number; latency_ms: number }>(`/api/admin/providers/${provider.id}/test`, { method: "POST" });
    notify(result.ok ? `Provider responded in ${result.latency_ms} ms.` : `Provider returned HTTP ${result.status_code}.`, result.ok ? "success" : "danger");
  } catch (caught) {
    notify(errorMessage(caught), "danger");
  }
}

function ProviderWizard({ onClose, onComplete, notify }: { onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [provider, setProvider] = useState({
    name: "", slug: "", base_url: "", auth_header: "Authorization", auth_scheme: "Bearer",
    timeout_seconds: 120, enabled: true, allow_private_network: false, extra_headers: {}
  });
  const [model, setModel] = useState({
    public_alias: "", upstream_model: "", supports_chat: true, supports_responses: false,
    default_max_output_tokens: 1024, tokenizer: "heuristic", capture_bodies: false, enabled: true
  });
  const [credentialLines, setCredentialLines] = useState("");
  const [limits, setLimits] = useState<RatePolicy>(emptyPolicy);
  const steps = ["Provider", "Model route", "Credentials", "Review"];

  const finish = async () => {
    setBusy(true);
    setError("");
    try {
      const created = await api<{ id: string }>("/api/admin/providers", { method: "POST", json: provider });
      if (model.public_alias.trim() && model.upstream_model.trim()) {
        await api(`/api/admin/providers/${created.id}/models`, { method: "POST", json: model });
      }
      const credentials = parseCredentialLines(credentialLines, limits);
      if (credentials.length) {
        await api(`/api/admin/providers/${created.id}/credentials`, { method: "POST", json: { credentials } });
      }
      if (credentials.length) {
        try {
          await api(`/api/admin/providers/${created.id}/test`, { method: "POST" });
        } catch (caught) {
          notify(`Provider saved, but test failed: ${errorMessage(caught)}`, "danger");
        }
      }
      onComplete("Provider setup completed.");
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet title="Add provider" eyebrow={`Step ${step + 1} of ${steps.length}`} onClose={onClose} wide>
      <div className="stepper">
        {steps.map((label, index) => <span key={label} className={index <= step ? "is-active" : ""}>{label}</span>)}
      </div>
      {error && <InlineNotice tone="danger">{error}</InlineNotice>}
      {step === 0 && <ProviderFields value={provider} onChange={setProvider} />}
      {step === 1 && <ModelFields value={model} onChange={setModel} />}
      {step === 2 && (
        <>
          <label className="field">
            <span>Credentials <small>One per line: label|secret</small></span>
            <textarea rows={7} placeholder={"primary|sk-...\nsecondary|sk-..."} value={credentialLines} onChange={(event) => setCredentialLines(event.target.value)} />
          </label>
          <fieldset><legend>Shared limits for imported credentials</legend><RateFields value={limits} onChange={setLimits} /></fieldset>
        </>
      )}
      {step === 3 && (
        <div className="review-list">
          <div><span>Provider</span><strong>{provider.name || "Missing"}</strong><code>{provider.base_url || "Missing base URL"}</code></div>
          <div><span>Public model</span><strong>{model.public_alias || "No initial route"}</strong><code>{model.upstream_model || "Add later"}</code></div>
          <div><span>Credentials</span><strong>{parseCredentialLines(credentialLines, limits).length}</strong><small>Secrets will be encrypted before storage.</small></div>
        </div>
      )}
      <div className="sheet-actions">
        {step > 0 && <Button variant="quiet" onClick={() => setStep(step - 1)} disabled={busy}>Back</Button>}
        <span />
        {step < steps.length - 1 ? (
          <Button onClick={() => setStep(step + 1)}>Continue</Button>
        ) : (
          <Button onClick={() => void finish()} disabled={busy}>{busy ? "Creating provider…" : "Create and test"}</Button>
        )}
      </div>
    </Sheet>
  );
}

type ProviderDraft = {
  name: string; slug: string; base_url: string; auth_header: string; auth_scheme: string;
  timeout_seconds: number; enabled: boolean; allow_private_network: boolean; extra_headers: Record<string, string>;
};

function ProviderFields({ value, onChange }: { value: ProviderDraft; onChange: (value: ProviderDraft) => void }) {
  return (
    <div className="form-stack">
      <div className="field-pair">
        <label className="field"><span>Name</span><input required placeholder="Groq production" value={value.name} onChange={(e) => onChange({ ...value, name: e.target.value, slug: value.slug || slugify(e.target.value) })} /></label>
        <label className="field"><span>Slug</span><input required placeholder="groq-prod" value={value.slug} onChange={(e) => onChange({ ...value, slug: slugify(e.target.value) })} /></label>
      </div>
      <label className="field"><span>OpenAI-compatible base URL <small>Include /v1 when the provider requires it</small></span><input type="url" required placeholder="https://api.provider.com/v1" value={value.base_url} onChange={(e) => onChange({ ...value, base_url: e.target.value })} /></label>
      <div className="field-pair">
        <label className="field"><span>Authentication header</span><input value={value.auth_header} onChange={(e) => onChange({ ...value, auth_header: e.target.value })} /></label>
        <label className="field"><span>Authentication scheme</span><input placeholder="Bearer" value={value.auth_scheme} onChange={(e) => onChange({ ...value, auth_scheme: e.target.value })} /></label>
      </div>
      <label className="field"><span>Timeout seconds</span><input type="number" min={1} max={900} value={value.timeout_seconds} onChange={(e) => onChange({ ...value, timeout_seconds: Number(e.target.value) })} /></label>
      <Toggle checked={value.enabled} onChange={(enabled) => onChange({ ...value, enabled })} label="Enable provider" description="Disabled providers are never considered for routing." />
      <Toggle checked={value.allow_private_network} onChange={(allow_private_network) => onChange({ ...value, allow_private_network })} label="Allow private-network target" description="Also permits HTTP. Enable only for a provider you operate on this VPS or LAN." />
    </div>
  );
}

function ProviderForm({ provider, onClose, onComplete, notify }: { provider: Provider; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [draft, setDraft] = useState<ProviderDraft>({
    name: provider.name, slug: provider.slug, base_url: provider.base_url,
    auth_header: provider.auth_header, auth_scheme: provider.auth_scheme,
    timeout_seconds: provider.timeout_seconds, enabled: provider.enabled,
    allow_private_network: provider.allow_private_network, extra_headers: provider.extra_headers
  });
  const [busy, setBusy] = useState(false);
  return (
    <Sheet title={`Edit ${provider.name}`} eyebrow="Provider settings" onClose={onClose}>
      <ProviderFields value={draft} onChange={setDraft} />
      <div className="sheet-actions"><span /><Button disabled={busy} onClick={() => {
        setBusy(true);
        void api(`/api/admin/providers/${provider.id}`, { method: "PUT", json: draft })
          .then(() => onComplete("Provider updated."))
          .catch((caught) => notify(errorMessage(caught), "danger"))
          .finally(() => setBusy(false));
      }}>{busy ? "Saving…" : "Save provider"}</Button></div>
    </Sheet>
  );
}

type ModelDraft = Omit<ModelRoute, "id" | "provider_id" | "created_at" | "updated_at">;

function ModelFields({ value, onChange }: { value: ModelDraft; onChange: (value: ModelDraft) => void }) {
  return (
    <div className="form-stack">
      <label className="field"><span>Public model alias <small>Applications put this in the model field</small></span><input required placeholder="groq/llama-3.3-70b" value={value.public_alias} onChange={(e) => onChange({ ...value, public_alias: e.target.value })} /></label>
      <label className="field"><span>Upstream model ID</span><input required placeholder="llama-3.3-70b-versatile" value={value.upstream_model} onChange={(e) => onChange({ ...value, upstream_model: e.target.value })} /></label>
      <div className="field-pair">
        <label className="field"><span>Default max output tokens</span><input type="number" min={1} value={value.default_max_output_tokens} onChange={(e) => onChange({ ...value, default_max_output_tokens: Number(e.target.value) })} /></label>
        <label className="field"><span>Tokenizer profile</span><select value={value.tokenizer} onChange={(e) => onChange({ ...value, tokenizer: e.target.value })}><option value="heuristic">Conservative heuristic</option><option value="cl100k_base">cl100k_base</option><option value="o200k_base">o200k_base</option></select></label>
      </div>
      <Toggle checked={value.supports_chat} onChange={(supports_chat) => onChange({ ...value, supports_chat })} label="Upstream supports Chat Completions" />
      <Toggle checked={value.supports_responses} onChange={(supports_responses) => onChange({ ...value, supports_responses })} label="Upstream supports Responses natively" description="When off, the gateway translates the supported Responses subset to Chat Completions." />
      <Toggle checked={value.capture_bodies} onChange={(capture_bodies) => onChange({ ...value, capture_bodies })} label="Capture encrypted request and response bodies" description="Off by default. Captured bodies follow the configured retention window." />
      <Toggle checked={value.enabled} onChange={(enabled) => onChange({ ...value, enabled })} label="Enable public route" />
    </div>
  );
}

function ModelForm({ provider, model, onClose, onComplete, notify }: { provider: Provider; model?: ModelRoute; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [draft, setDraft] = useState<ModelDraft>(model ? {
    public_alias: model.public_alias, upstream_model: model.upstream_model,
    supports_chat: model.supports_chat, supports_responses: model.supports_responses,
    default_max_output_tokens: model.default_max_output_tokens, tokenizer: model.tokenizer,
    capture_bodies: model.capture_bodies, enabled: model.enabled
  } : {
    public_alias: `${provider.slug}/`, upstream_model: "", supports_chat: true,
    supports_responses: false, default_max_output_tokens: 1024,
    tokenizer: "heuristic", capture_bodies: false, enabled: true
  });
  const [busy, setBusy] = useState(false);
  const save = async () => {
    setBusy(true);
    try {
      if (model) await api(`/api/admin/models/${model.id}`, { method: "PUT", json: draft });
      else await api(`/api/admin/providers/${provider.id}/models`, { method: "POST", json: draft });
      onComplete(model ? "Model route updated." : "Model route created.");
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet title={model ? "Edit model route" : "Add model route"} eyebrow={provider.name} onClose={onClose}>
      <ModelFields value={draft} onChange={setDraft} />
      {model && <div className="danger-zone"><div><strong>Delete route</strong><small>Requests using this alias will stop immediately.</small></div><Button variant="danger" onClick={() => {
        if (confirm(`Delete route ${model.public_alias}?`)) void api(`/api/admin/models/${model.id}`, { method: "DELETE" }).then(() => onComplete("Model route deleted.")).catch((caught) => notify(errorMessage(caught), "danger"));
      }}><Trash2 size={14} /> Delete</Button></div>}
      <div className="sheet-actions"><span /><Button disabled={busy} onClick={() => void save()}>{busy ? "Saving…" : model ? "Save route" : "Create route"}</Button></div>
    </Sheet>
  );
}

function CredentialForm({ provider, credential, onClose, onComplete, notify }: { provider: Provider; credential?: Credential; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [label, setLabel] = useState(credential?.label ?? "");
  const [secret, setSecret] = useState("");
  const [bulk, setBulk] = useState("");
  const [enabled, setEnabled] = useState(credential?.enabled ?? true);
  const [limits, setLimits] = useState<RatePolicy>(credential?.limits ?? emptyPolicy());
  const [selectedModel, setSelectedModel] = useState(provider.models[0]?.id ?? "");
  const [modelLimits, setModelLimits] = useState<RatePolicy>(() => credential?.model_limits[provider.models[0]?.id] ?? emptyPolicy());
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setModelLimits(credential?.model_limits[selectedModel] ?? emptyPolicy());
  }, [selectedModel, credential]);

  const save = async () => {
    setBusy(true);
    try {
      if (credential) {
        await api(`/api/admin/credentials/${credential.id}`, { method: "PUT", json: { label, secret, enabled, limits } });
        onComplete("Credential and shared limits updated.");
      } else {
        const credentials = bulk.trim()
          ? parseCredentialLines(bulk, limits)
          : [{ label, secret, enabled, limits }];
        await api(`/api/admin/providers/${provider.id}/credentials`, { method: "POST", json: { credentials } });
        onComplete(`${credentials.length} credential${credentials.length === 1 ? "" : "s"} added.`);
      }
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet title={credential ? `Edit ${credential.label}` : "Add credentials"} eyebrow={provider.name} onClose={onClose} wide>
      {!credential && (
        <label className="field">
          <span>Bulk import <small>Optional. One label|secret pair per line.</small></span>
          <textarea rows={5} value={bulk} onChange={(e) => setBulk(e.target.value)} placeholder={"primary|sk-...\nsecondary|sk-..."} />
        </label>
      )}
      {(!bulk || credential) && (
        <div className="field-pair">
          <label className="field"><span>Label</span><input required value={label} onChange={(e) => setLabel(e.target.value)} /></label>
          <label className="field"><span>{credential ? "Replacement secret" : "Secret"} <small>{credential ? "Leave blank to keep current" : ""}</small></span><input type="password" required={!credential} autoComplete="off" value={secret} onChange={(e) => setSecret(e.target.value)} /></label>
        </div>
      )}
      <Toggle checked={enabled} onChange={setEnabled} label="Enable credential" description="Re-enabling also clears quarantine and circuit-breaker state." />
      <fieldset><legend>Shared credential limits</legend><p className="fieldset-note">Every model using this key consumes these limits. Blank means no limit.</p><RateFields value={limits} onChange={setLimits} /></fieldset>
      {credential && provider.models.length > 0 && (
        <fieldset>
          <legend>Additional model limit</legend>
          <p className="fieldset-note">Both shared and model-specific limits must have capacity.</p>
          <label className="field"><span>Model route</span><select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)}>{provider.models.map((model) => <option key={model.id} value={model.id}>{model.public_alias}</option>)}</select></label>
          <RateFields value={modelLimits} onChange={setModelLimits} compact />
          <div className="button-row">
            <Button variant="quiet" onClick={() => void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "PUT", json: modelLimits }).then(() => onComplete("Model-specific limits saved.")).catch((caught) => notify(errorMessage(caught), "danger"))}>Save model limit</Button>
            {credential.model_limits[selectedModel] && <Button variant="quiet" onClick={() => void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "DELETE" }).then(() => onComplete("Model-specific limits removed.")).catch((caught) => notify(errorMessage(caught), "danger"))}>Use shared only</Button>}
          </div>
        </fieldset>
      )}
      {credential && <div className="danger-zone"><div><strong>Delete credential</strong><small>The encrypted secret cannot be recovered after deletion.</small></div><Button variant="danger" onClick={() => {
        if (confirm(`Delete credential ${credential.label}?`)) void api(`/api/admin/credentials/${credential.id}`, { method: "DELETE" }).then(() => onComplete("Credential deleted.")).catch((caught) => notify(errorMessage(caught), "danger"));
      }}><Trash2 size={14} /> Delete</Button></div>}
      <div className="sheet-actions"><span /><Button disabled={busy} onClick={() => void save()}>{busy ? "Saving…" : credential ? "Save credential" : "Add credentials"}</Button></div>
    </Sheet>
  );
}

function ModelsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  useEffect(() => {
    void api<{ providers: Provider[] }>("/api/admin/providers").then((result) => setProviders(result.providers)).catch((caught) => notify(errorMessage(caught), "danger")).finally(() => setLoading(false));
  }, [notify]);
  const models = providers.flatMap((provider) => provider.models.map((model) => ({ ...model, provider, credentials: provider.credentials })));
  return (
    <>
      <PageHeader eyebrow="Public contract" title="Model routes" description="These aliases—not provider names—are the only routing choice your applications make." />
      {loading ? <PageSkeleton /> : models.length === 0 ? <EmptyState title="No public models" description="Create a route inside a provider to expose it through /v1/models." /> : (
        <div className="model-table">
          <div className="table-head"><span>Public alias</span><span>Provider / upstream</span><span>Endpoints</span><span>Credential pool</span><span>Capture</span></div>
          {models.map((model) => (
            <div className="table-row" key={model.id}>
              <span><StatusDot state={model.enabled ? "healthy" : "disabled"} /><code>{model.public_alias}</code></span>
              <span><strong>{model.provider.name}</strong><small>{model.upstream_model}</small></span>
              <span><small>Chat · {model.supports_responses ? "Responses native" : "Responses translated"}</small></span>
              <span>{model.credentials.filter((item) => item.enabled && item.status === "healthy").length}/{model.credentials.length} ready</span>
              <span>{model.capture_bodies ? "30d encrypted" : "Metadata only"}</span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}

function LogsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const [query, setQuery] = useState("");
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<RequestLog | null>(null);
  const [bodies, setBodies] = useState<{ request: string | null; response: string | null } | null>(null);
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await api<{ logs: RequestLog[] }>(`/api/admin/logs?limit=100&q=${encodeURIComponent(query)}&status=${encodeURIComponent(status)}`);
      setLogs(result.logs);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setLoading(false);
    }
  }, [query, status, notify]);
  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    setBodies(null);
    if (selected?.body_captured) {
      void api<{ request: string | null; response: string | null }>(`/api/admin/logs/${selected.id}/bodies`).then(setBodies).catch((caught) => notify(errorMessage(caught), "danger"));
    }
  }, [selected, notify]);

  return (
    <>
      <PageHeader eyebrow="Request evidence" title="Logs" description="Metadata is always retained. Bodies appear only for routes where encrypted capture is enabled." actions={<Button variant="quiet" onClick={() => void load()}><RefreshCw size={15} /> Refresh</Button>} />
      <div className="filter-bar">
        <label><span className="sr-only">Search logs</span><input placeholder="Request ID or model alias" value={query} onChange={(e) => setQuery(e.target.value)} /></label>
        <label><span className="sr-only">Status</span><select value={status} onChange={(e) => setStatus(e.target.value)}><option value="">All statuses</option><option value="200">200</option><option value="400">400</option><option value="429">429</option><option value="500">500</option><option value="502">502</option><option value="503">503</option></select></label>
      </div>
      {loading ? <PageSkeleton /> : logs.length === 0 ? <EmptyState title="No matching requests" description="Send a request through /v1 or clear the current filters." /> : (
        <div className="log-table">
          <div className="table-head"><span>Time / request</span><span>Model</span><span>Route</span><span>Status</span><span>Latency</span><span>Tokens</span></div>
          {logs.map((log) => (
            <button className="table-row" key={log.id} onClick={() => setSelected(log)}>
              <span><strong>{new Date(log.created_at).toLocaleTimeString()}</strong><small>{log.request_id}</small></span>
              <span><code>{log.model_alias}</code></span>
              <span><strong>{log.provider_name}</strong><small>{log.credential_label || "No credential"}</small></span>
              <span className={log.status_code >= 400 ? "text-danger" : ""}>{log.status_code}</span>
              <span>{log.latency_ms} ms</span>
              <span>{formatNumber(log.input_tokens + log.output_tokens)}</span>
            </button>
          ))}
        </div>
      )}
      {selected && (
        <Sheet title={selected.request_id} eyebrow={`${selected.endpoint} · HTTP ${selected.status_code}`} onClose={() => setSelected(null)} wide>
          <div className="log-detail-grid">
            <div><span>Model</span><code>{selected.model_alias}</code></div>
            <div><span>Provider</span><strong>{selected.provider_name}</strong></div>
            <div><span>Latency</span><strong>{selected.latency_ms} ms</strong></div>
            <div><span>Tokens</span><strong>{selected.input_tokens} in · {selected.output_tokens} out</strong></div>
          </div>
          <fieldset><legend>Attempts</legend><div className="attempt-list">{selected.attempts.map((attempt, index) => <div key={`${attempt.credential_id}-${index}`}><span>{index + 1}</span><strong>{attempt.credential_label}</strong><code>{attempt.status_code || attempt.error}</code><small>{attempt.duration_ms} ms</small></div>)}</div></fieldset>
          {selected.body_captured ? (
            <div className="body-grid">
              <section><h3>Request {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies?.request ?? "Loading encrypted body…"}</pre></section>
              <section><h3>Response {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies?.response ?? "No captured response body."}</pre></section>
            </div>
          ) : <InlineNotice>Body capture was off for this model route. Only metadata is available.</InlineNotice>}
        </Sheet>
      )}
    </>
  );
}

function AccessPage({ gatewayKey, onNewKey, notify }: { gatewayKey: string; onNewKey: (key: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  useEffect(() => { void api<Settings>("/api/admin/settings").then(setSettings).catch((caught) => notify(errorMessage(caught), "danger")); }, [notify, gatewayKey]);
  return (
    <>
      <PageHeader eyebrow="Unified authentication" title="Access key" description="One active Bearer key can call every enabled model alias. Upstream provider secrets never leave this control plane." />
      <section className="access-key-panel">
        <div className="key-prefix"><ShieldCheck size={20} /><span><small>Active key prefix</small><code>{settings?.gateway_key_prefix ? `${settings.gateway_key_prefix}••••••••••••` : "Loading…"}</code></span></div>
        <div><strong>Immediate rotation</strong><p>Rotating revokes the current key in the same database transaction. Update every application immediately.</p></div>
        <Button variant="danger" onClick={() => {
          if (!confirm("Rotate the gateway key now? The current key will stop working immediately.")) return;
          void api<{ gateway_key: string }>("/api/admin/access/rotate", { method: "POST" })
            .then((result) => { onNewKey(result.gateway_key); notify("Gateway key rotated."); })
            .catch((caught) => notify(errorMessage(caught), "danger"));
        }}><RefreshCw size={15} /> Rotate key</Button>
      </section>
      <section className="section-block code-example">
        <div className="section-heading"><div><p className="eyebrow">Client contract</p><h2>Use any configured model</h2></div></div>
        <pre>{`curl "$BASE_URL/chat/completions" \\
  -H "Authorization: Bearer $ROTAKEY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"provider/model-alias","messages":[{"role":"user","content":"Hello"}]}'`}</pre>
      </section>
    </>
  );
}

function SettingsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => { void api<Settings>("/api/admin/settings").then(setSettings).catch((caught) => notify(errorMessage(caught), "danger")); }, [notify]);
  if (!settings) return <PageSkeleton />;
  return (
    <>
      <PageHeader eyebrow="Control plane policy" title="System" description="Bound waiting and retention so the gateway stays predictable on a small VPS." />
      <section className="settings-list">
        <label className="settings-row"><span><strong>Capacity wait ceiling</strong><small>Requests wait only when capacity can return within this deadline.</small></span><div><input type="number" min={0} max={30000} step={100} value={settings.max_wait_ms} onChange={(e) => setSettings({ ...settings, max_wait_ms: Number(e.target.value) })} /><code>ms</code></div></label>
        <label className="settings-row"><span><strong>Metadata retention</strong><small>Request IDs, routing attempts, status, latency and usage.</small></span><div><input type="number" min={1} max={3650} value={settings.metadata_retention_days} onChange={(e) => setSettings({ ...settings, metadata_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
        <label className="settings-row"><span><strong>Captured body retention</strong><small>Only applies to model routes where encrypted body capture is enabled.</small></span><div><input type="number" min={1} max={365} value={settings.body_retention_days} onChange={(e) => setSettings({ ...settings, body_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
      </section>
      <div className="page-actions"><Button disabled={busy} onClick={() => {
        setBusy(true);
        void api("/api/admin/settings", { method: "PUT", json: settings })
          .then(() => notify("System settings saved."))
          .catch((caught) => notify(errorMessage(caught), "danger"))
          .finally(() => setBusy(false));
      }}>{busy ? "Saving…" : "Save settings"}</Button></div>
      <section className="security-baseline">
        <ShieldCheck size={19} />
        <div><strong>Security baseline active</strong><p>Encrypted provider secrets · HttpOnly sessions · CSRF checks · private-network SSRF blocking · strict browser policy</p></div>
      </section>
    </>
  );
}

function SecretReveal({ title, keyValue, message, onClose, notify }: { title: string; keyValue: string; message: string; onClose: () => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <Sheet title={title} eyebrow="One-time secret" onClose={() => { if (confirmed || confirm("Close without confirming that the key is saved?")) onClose(); }}>
      <InlineNotice tone="danger">{message}</InlineNotice>
      <div className="secret-value"><code>{keyValue}</code><Button variant="quiet" onClick={() => void navigator.clipboard.writeText(keyValue).then(() => notify("Gateway key copied.")).catch(() => notify("Clipboard access was blocked.", "danger"))}><Clipboard size={14} /> Copy</Button></div>
      <label className="confirmation-check"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /><span>I stored this key securely.</span></label>
      <div className="sheet-actions"><span /><Button disabled={!confirmed} onClick={onClose}>Finish</Button></div>
    </Sheet>
  );
}

function PageSkeleton() {
  return <div className="page-skeleton" aria-label="Loading"><span /><span /><span /><span /></div>;
}

function parseCredentialLines(value: string, limits: RatePolicy) {
  return value.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).map((line) => {
    const separator = line.indexOf("|");
    if (separator < 1) return { label: "", secret: "", enabled: true, limits };
    return { label: line.slice(0, separator).trim(), secret: line.slice(separator + 1).trim(), enabled: true, limits };
  }).filter((entry) => entry.label && entry.secret);
}

function slugify(value: string) {
  return value.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 63);
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("en", { notation: value > 9999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function errorMessage(caught: unknown) {
  if (caught instanceof APIError || caught instanceof Error) return caught.message;
  return "The operation could not be completed.";
}

export default App;
