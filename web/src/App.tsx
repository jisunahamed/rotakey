import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Activity,
  AlertTriangle,
  ArrowRight,
  BookOpen,
  Cable,
  Check,
  ChevronDown,
  ChevronRight,
  CircleGauge,
  Clipboard,
  Database,
  Download,
  FileClock,
  Github,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  Plus,
  RefreshCw,
  Route,
  Search,
  Settings as SettingsIcon,
  ShieldCheck,
  Sun,
  Trash2,
  Upload,
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
  type DiscoveredModel,
  type ImportResult,
  type ModelRoute,
  type Overview,
  type Provider,
  type RatePolicy,
  type RequestLog,
  type RoutingMode,
  type Settings,
  type SettingsUpdateResult
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

// The routing mode decides whether a new public alias carries the provider slug,
// so it is fetched once and shared by every form that proposes an alias.
let routingModeCache: RoutingMode | null = null;

function useRoutingMode(): RoutingMode {
  const [mode, setMode] = useState<RoutingMode>(routingModeCache ?? "provider");
  useEffect(() => {
    if (routingModeCache) return;
    void api<Settings>("/api/admin/settings")
      .then((settings) => {
        routingModeCache = settings.routing_mode === "model" ? "model" : "provider";
        setMode(routingModeCache);
      })
      .catch(() => undefined);
  }, []);
  return mode;
}

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
  const [version, setVersion] = useState<VersionInfo | null>(null);
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
    const loadVersion = () => void api<VersionInfo>("/api/version").then(setVersion).catch(() => undefined);
    loadVersion();
    const timer = window.setInterval(loadVersion, 60 * 60 * 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    const onPopState = () => setPage(pageFromLocation());
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const navigate = (next: Page, query?: Record<string, string>) => {
    setPage(next);
    setMenuOpen(false);
    const search = query ? `?${new URLSearchParams(query).toString()}` : "";
    history.pushState({}, "", `/admin/${next}${search}`);
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
        <div className="sidebar__bottom">
          {version?.update_available && (
            <a className="release-notice" href={version.release_url} target="_blank" rel="noreferrer">
              <span>Update available</span>
              <strong>Rotakey v{version.latest_version}</strong>
              <small>View release notes →</small>
            </a>
          )}
          <a
            className="sidebar__github"
            href="https://github.com/jisunahamed/rotakey/blob/main/docs/OPERATOR-GUIDE.md"
            target="_blank"
            rel="noreferrer"
          >
            <BookOpen size={15} />
            <span>Operator guide</span>
          </a>
          <a
            className="sidebar__github"
            href="https://github.com/jisunahamed/rotakey"
            target="_blank"
            rel="noreferrer"
          >
            <Github size={15} />
            <span>Star on GitHub</span>
          </a>
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
          <div className="sidebar__version" title={version?.commit ? `Commit ${version.commit}` : undefined}>
            Rotakey v{version?.current_version ?? "0.2.7"}
            {version?.update_available ? <span>new v{version.latest_version}</span> : <span>up to date</span>}
          </div>
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
          {page === "models" && <ModelsPage navigate={navigate} notify={notify} />}
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

type VersionInfo = {
  current_version: string;
  commit: string;
  build_time: string;
  latest_version?: string;
  update_available: boolean;
  release_url: string;
  published_at?: string;
};

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
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  type InspectTarget = { type: "provider" | "route" | "credential"; id: string };
  const [overview, setOverview] = useState<Overview | null>(null);
  const [error, setError] = useState("");
  const [range, setRange] = useState<Overview["range"]>(() => {
    const value = new URLSearchParams(location.search).get("range");
    return value === "1h" || value === "24h" || value === "7d" || value === "all" ? value : "all";
  });
  const [selected, setSelected] = useState<InspectTarget | null>(null);
  const [expandedProviders, setExpandedProviders] = useState<Set<string>>(() => new Set());
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const selectTarget = (target: InspectTarget) => {
    if (overview) {
      const providerID = overviewProviderIDForTarget(overview, target);
      if (providerID) {
        setExpandedProviders((current) => new Set(current).add(providerID));
      }
    }
    setSelected(target);
    setInspectorOpen(true);
  };
  const load = useCallback(async () => {
    setError("");
    setRefreshing(true);
    try {
      const result = await api<Overview>(`/api/admin/overview?range=${range}`);
      setOverview(result);
      setSelected((current) => current && overviewTargetExists(result, current) ? current : null);
      setExpandedProviders((current) => new Set([...current].filter((id) => result.providers.some((provider) => provider.id === id))));
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Overview could not be loaded.");
    } finally {
      setRefreshing(false);
    }
  }, [range]);
  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 10_000);
    return () => window.clearInterval(timer);
  }, [load]);

  if (!overview && !error) return <PageSkeleton />;
  const selectedProvider = overview?.providers.find((provider) => (
    selected?.type === "provider"
      ? provider.id === selected.id
      : selected?.type === "route"
        ? overview.routes.find((route) => route.id === selected.id)?.provider_id === provider.id
        : overview.routes.some((route) => route.provider_id === provider.id && route.segments.some((segment) => segment.id === selected?.id))
  ));
  const selectedRoute = overview?.routes.find((route) => (
    selected?.type === "route"
      ? route.id === selected.id
      : selected?.type === "credential" && route.segments.some((segment) => segment.id === selected.id)
  ));
  const selectedCredential = selectedRoute?.segments.find((segment) => segment.id === selected?.id);
  return (
    <>
      {error && <InlineNotice tone="danger">{error}</InlineNotice>}
      {overview && (
        <div className="ops-console">
          <header className="ops-commandbar">
            <div className="ops-commandbar__identity">
              <span className={`gateway-pulse ${overview.summary.routes_ready === overview.summary.routes_total ? "" : "is-warning"}`} />
              <span>
                <small>Unified gateway · {overview.summary.gateway_key_ready ? "authenticated" : "key missing"}</small>
                <code>{overview.base_url || `${location.origin}/v1`}</code>
              </span>
              <button
                className="console-icon copy-base-url"
                aria-label="Copy unified base URL"
                onClick={() => {
                  void copyText(overview.base_url || `${location.origin}/v1`)
                    .then(() => notify("Base URL copied."))
                    .catch(() => notify("Clipboard access was blocked.", "danger"));
                }}
              ><Clipboard size={14} /><span>Copy</span></button>
            </div>
            <div className="ops-commandbar__actions">
              <div className="range-switcher" aria-label="Overview time range">
                {(["1h", "24h", "7d", "all"] as const).map((value) => (
                  <button
                    key={value}
                    className={range === value ? "is-active" : ""}
                    aria-pressed={range === value}
                    onClick={() => {
                      setRange(value);
                      history.replaceState({}, "", `/admin/overview?range=${value}`);
                    }}
                  >{value}</button>
                ))}
              </div>
              <button className={`console-refresh ${refreshing ? "is-refreshing" : ""}`} onClick={() => void load()}>
                <RefreshCw size={14} /> {refreshing ? "Syncing" : "Refresh"}
              </button>
            </div>
          </header>

          <section className="status-ledger" aria-label={`${range} gateway status`}>
            <LedgerMetric label="Routes ready" value={`${overview.summary.routes_ready}/${overview.summary.routes_total}`} tone={overview.summary.routes_ready < overview.summary.routes_total ? "danger" : "healthy"} />
            <LedgerMetric label="API keys ready" value={`${overview.summary.keys_ready}/${overview.summary.keys_total}`} tone={overview.summary.keys_warning ? "warning" : "healthy"} />
            <LedgerMetric label="Requests" value={formatNumber(overview.summary.requests)} />
            <LedgerMetric label="Errors" value={`${formatNumber(overview.summary.errors)} · ${(overview.summary.error_rate * 100).toFixed(1)}%`} tone={overview.summary.errors ? "danger" : "default"} />
            <LedgerMetric label="P95 latency" value={`${formatNumber(overview.summary.latency_p95_ms)} ms`} />
            <LedgerMetric label="Tokens" value={formatCompact(overview.summary.tokens)} />
            <LedgerMetric label="Estimated cost" value={formatUSD(overview.summary.estimated_cost_usd)} />
          </section>

          <div className={`ops-console__workspace${selected ? " has-inspector" : ""}`}>
            <div className="ops-console__main">
              <ModelUsageRanking routes={overview.routes} range={overview.range} />
              <div className="signal-grid">
                <SignalTimeline overview={overview} />
                <AttentionQueue
                  alerts={overview.alerts}
                  onSelect={(alert) => selectTarget({ type: alert.resource_type, id: alert.resource_id })}
                />
              </div>

              <section className="console-panel capacity-debugger">
                <ConsolePanelHeader
                  eyebrow="Provider capacity"
                  title="Providers and model routes"
                  detail={`Updated ${formatRelativeTime(overview.generated_at)}`}
                />
                {overview.routes.length === 0 ? (
                  <EmptyState
                    title="No model routes yet"
                    description="Add a provider, validate an API key, then select at least one upstream model."
                    action={<Button onClick={() => navigate("providers")}><Plus size={15} /> Add first provider</Button>}
                  />
                ) : overview.providers.map((provider) => (
                  <ProviderCapacityGroup
                    key={provider.id}
                    provider={provider}
                    routes={overview.routes.filter((route) => route.provider_id === provider.id)}
                    selected={selected}
                    onSelect={selectTarget}
                    expanded={expandedProviders.has(provider.id)}
                    onToggle={() => setExpandedProviders((current) => {
                      const next = new Set(current);
                      if (next.has(provider.id)) next.delete(provider.id);
                      else next.add(provider.id);
                      return next;
                    })}
                  />
                ))}
              </section>

              <section className="console-panel failure-panel">
                <ConsolePanelHeader eyebrow="Request evidence" title="Recent failures" detail={`Selected range · ${range}`} />
                {overview.recent_failures.length === 0 ? (
                  <p className="console-empty">No failed requests in this range.</p>
                ) : (
                  <div className="failure-list">
                    {overview.recent_failures.map((failure) => (
                      <button
                        key={failure.request_id}
                        onClick={() => navigate("logs", { q: failure.request_id, status: String(failure.status_code) })}
                      >
                        <span className="failure-code">{failure.status_code}</span>
                        <span><code>{failure.model_alias}</code><small>{failure.error_code || failure.provider_name}</small></span>
                        <span><strong>{failure.credential_label || "No key"}</strong><small>{failure.latency_ms} ms</small></span>
                        <time>{formatRelativeTime(failure.created_at)}</time>
                        <ChevronRight size={14} />
                      </button>
                    ))}
                  </div>
                )}
              </section>
            </div>

            {selected && <OverviewInspector
              provider={selectedProvider}
              route={selectedRoute}
              credential={selectedCredential}
              open={inspectorOpen}
              onClose={() => {
                setInspectorOpen(false);
                setSelected(null);
              }}
              onSelectCredential={(id) => selectTarget({ type: "credential", id })}
              onTest={async () => {
                if (!selectedProvider) return;
                await testProvider(selectedProvider, notify);
                await load();
              }}
              onRecheck={async (credentialID) => {
                if (!selectedProvider) return;
                try {
                  const inspection = await api<{ valid: boolean; warning?: string; models: unknown[] }>(
                    `/api/admin/providers/${selectedProvider.id}/models/discover`,
                    { method: "POST", json: { credential_id: credentialID } }
                  );
                  notify(
                    inspection.valid ? `API key is valid · ${inspection.models.length} models visible.` : inspection.warning || "API key validation failed.",
                    inspection.valid ? "success" : "danger"
                  );
                  await load();
                } catch (caught) {
                  notify(errorMessage(caught), "danger");
                }
              }}
              navigate={navigate}
            />}
          </div>
        </div>
      )}
    </>
  );
}

function LedgerMetric({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "healthy" | "warning" | "danger" }) {
  return <div className={`ledger-metric ledger-metric--${tone}`}><span>{label}</span><strong>{value}</strong></div>;
}

function ConsolePanelHeader({ eyebrow, title, detail }: { eyebrow: string; title: string; detail?: string }) {
  return (
    <header className="console-panel__header">
      <div><span>{eyebrow}</span><h2>{title}</h2></div>
      {detail && <small>{detail}</small>}
    </header>
  );
}

function SignalTimeline({ overview }: { overview: Overview }) {
  const chartWidth = 654;
  const chartHeight = 116;
  const requestValues = overview.series.map((point) => point.requests);
  const latencyValues = overview.series.map((point) => point.latency_p95_ms);
  const requestPath = timelinePath(requestValues, chartWidth, chartHeight);
  const latencyPath = timelinePath(latencyValues, chartWidth, chartHeight);
  const requestMax = Math.max(...requestValues, 0);
  const latencyMax = Math.max(...latencyValues, 0);
  const errorPoints = overview.series.map((point, index) => ({
    x: overview.series.length <= 1 ? 0 : (index / (overview.series.length - 1)) * chartWidth,
    active: point.errors > 0
  }));
  return (
    <section className="console-panel signal-panel">
      <ConsolePanelHeader eyebrow="Signal timeline" title={`Traffic · ${overview.range}`} detail={`P50 ${overview.summary.latency_p50_ms} ms`} />
      <div className="signal-chart">
        <svg viewBox="0 0 720 170" role="img" aria-label={`Requests and P95 latency over ${overview.range}`}>
          <g className="signal-axis-labels">
            <text x="0" y="15">{formatCompact(requestMax)}</text><text x="28" y="127">0</text>
            <text x="672" y="15">{formatLatency(latencyMax)}</text><text x="688" y="127">0</text>
            <text x="46" y="158">{formatChartDate(overview.series[0]?.timestamp)}</text>
            <text x="620" y="158">{formatChartDate(overview.series.at(-1)?.timestamp)}</text>
          </g>
          <g transform="translate(46 12)"><g className="signal-gridlines">
            {[0, 29, 58, 87, 116].map((y) => <line key={y} x1="0" x2={chartWidth} y1={y} y2={y} />)}
          </g><path className="signal-path signal-path--latency" d={latencyPath} />
          <path className="signal-path signal-path--requests" d={requestPath} />
          {errorPoints.filter((point) => point.active).map((point) => <line className="signal-error-tick" key={point.x} x1={point.x} x2={point.x} y1="121" y2="130" />)}</g>
        </svg>
        <div className="signal-legend">
          <span><i className="is-request" /> requests</span>
          <span><i className="is-latency" /> p95 latency</span>
          <span><i className="is-error" /> error bucket</span>
        </div>
      </div>
    </section>
  );
}

function ModelUsageRanking({ routes, range }: { routes: Overview["routes"]; range: Overview["range"] }) {
  const ranking = [...routes].filter((route) => route.requests > 0).sort((a, b) => b.estimated_cost_usd - a.estimated_cost_usd || b.tokens - a.tokens || b.requests - a.requests).slice(0, 6);
  return <section className="console-panel model-ranking"><ConsolePanelHeader eyebrow="Usage ledger" title="Model ranking" detail={`${range} · ranked by estimated cost`} />
    {ranking.length === 0 ? <p className="console-empty">No model usage in this range.</p> : <div className="model-ranking__table"><div className="model-ranking__head"><span>Model</span><span>Requests</span><span>Tokens</span><span>Cost</span></div>{ranking.map((route, index) => <div className="model-ranking__row" key={route.id}><strong><i>{index + 1}</i><code>{route.alias}</code></strong><span>{formatNumber(route.requests)}</span><span>{formatCompact(route.tokens)}</span><span>{formatUSD(route.estimated_cost_usd)}</span></div>)}</div>}
  </section>;
}

function AttentionQueue({ alerts, onSelect }: { alerts: Overview["alerts"]; onSelect: (alert: Overview["alerts"][number]) => void }) {
  return (
    <section className="console-panel attention-panel">
      <ConsolePanelHeader eyebrow="Attention queue" title={alerts.length ? `${alerts.length} signals` : "All clear"} />
      {alerts.length === 0 ? (
        <div className="all-clear"><Check size={16} /><span><strong>No intervention needed</strong><small>Routes and API keys are ready.</small></span></div>
      ) : (
        <div className="attention-list">
          {alerts.slice(0, 6).map((alert) => (
            <button key={alert.id} className={`attention-item attention-item--${alert.severity}`} onClick={() => onSelect(alert)}>
              <AlertTriangle size={14} />
              <span><strong>{alert.title}</strong><small>{alert.detail}</small></span>
              <ChevronRight size={13} />
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

function ProviderCapacityGroup({
  provider,
  routes,
  selected,
  onSelect,
  expanded,
  onToggle
}: {
  provider: Overview["providers"][number];
  routes: Overview["routes"];
  selected: { type: "provider" | "route" | "credential"; id: string } | null;
  onSelect: (target: { type: "provider" | "route" | "credential"; id: string }) => void;
  expanded: boolean;
  onToggle: () => void;
}) {
  return (
    <section className={`provider-debug-group${expanded ? " is-expanded" : ""}`}>
      <div className={`provider-debug-head ${selected?.type === "provider" && selected.id === provider.id ? "is-selected" : ""}`}>
        <button className="provider-debug-toggle" onClick={onToggle} aria-expanded={expanded} aria-controls={`provider-models-${provider.id}`}>
          <ChevronDown size={15} />
          <StatusDot state={!provider.enabled ? "disabled" : provider.keys_ready ? "healthy" : "exhausted"} />
          <span><strong>{provider.name}</strong><small>{provider.models_ready}/{provider.models_total} models ready · {provider.keys_ready}/{provider.keys_total} keys ready</small></span>
        </button>
        <div className="limit-preview">
          {capacityDimensions.map((dimension) => <LimitCell key={dimension} dimension={dimension} limit={provider.capacity[dimension]} />)}
        </div>
        <button className="provider-inspect" onClick={() => onSelect({ type: "provider", id: provider.id })}>Inspect</button>
      </div>
      {expanded && <div className="route-debug-list" id={`provider-models-${provider.id}`}>
        <div className="route-debug-columns" aria-hidden="true">
          <span>Public model</span><span>Traffic</span><span>Keys</span><span>Next key</span><span>Model override</span><span />
        </div>
        {routes.length ? routes.map((route) => (
          <RouteDebugRow key={route.id} route={route} selected={selected} onSelect={onSelect} />
        )) : <p className="provider-model-empty">No model routes configured for this provider.</p>}
      </div>}
    </section>
  );
}

function LimitCell({ dimension, limit }: { dimension: string; limit?: Overview["providers"][number]["capacity"][string] }) {
  const ratio = limit && !limit.unlimited && limit.limit > 0 ? limit.remaining / limit.limit : 1;
  const tone = limit?.unknown ? "unknown" : ratio <= 0 ? "critical" : ratio <= 0.2 ? "warning" : "healthy";
  return (
    <span className={`limit-cell limit-cell--${tone}`} title={`${dimension.toUpperCase()} provider capacity`}>
      <small>{dimension}</small>
      <strong>{!limit ? "—" : limit.unlimited ? "∞" : limit.unknown ? "?" : `${formatCompact(limit.remaining)}/${formatCompact(limit.limit)}`}</strong>
    </span>
  );
}

function RouteDebugRow({
  route,
  selected,
  onSelect
}: {
  route: Overview["routes"][number];
  selected: { type: "provider" | "route" | "credential"; id: string } | null;
  onSelect: (target: { type: "provider" | "route" | "credential"; id: string }) => void;
}) {
  const state = !route.enabled
    ? "disabled"
    : route.healthy_credentials === 0
      ? "exhausted"
      : route.healthy_credentials < route.total_credentials
        ? "partial"
        : "healthy";
  const isSelected = selected?.type === "route" && selected.id === route.id
    || selected?.type === "credential" && route.segments.some((segment) => segment.id === selected.id);
  const modelBottleneck = formatModelBottleneck(route);
  return (
    <button className={`route-debug-row ${isSelected ? "is-selected" : ""}`} onClick={() => onSelect({ type: "route", id: route.id })}>
      <span className="route-debug-row__identity">
        <StatusDot state={state} />
        <span><code>{route.alias}</code><small>{route.upstream_model !== route.alias ? `Upstream · ${route.upstream_model}` : route.supports_responses ? "Chat + Responses" : "Chat Completions"}</small></span>
      </span>
      <span className="route-traffic"><strong>{formatNumber(route.requests)}</strong><small>{(route.error_rate * 100).toFixed(1)}% err · {formatNumber(route.latency_p95_ms)} ms</small></span>
      <span className="route-key-count"><strong>{route.healthy_credentials}/{route.total_credentials}</strong><small>ready</small></span>
      <span className="route-next-key"><strong>{route.segments.find((segment) => segment.cursor)?.label || "—"}</strong><small>{route.next_credential_id ? "next" : "unavailable"}</small></span>
      <span className="route-bottleneck"><strong>{modelBottleneck}</strong><small>{modelBottleneck === "Shared only" ? "provider limit" : "model limit"}</small></span>
      <ChevronRight size={14} />
    </button>
  );
}

function OverviewInspector({
  provider,
  route,
  credential,
  open,
  onClose,
  onSelectCredential,
  onTest,
  onRecheck,
  navigate
}: {
  provider?: Overview["providers"][number];
  route?: Overview["routes"][number];
  credential?: Overview["routes"][number]["segments"][number];
  open: boolean;
  onClose: () => void;
  onSelectCredential: (id: string) => void;
  onTest: () => Promise<void>;
  onRecheck: (id: string) => Promise<void>;
  navigate: (page: Page, query?: Record<string, string>) => void;
}) {
  const [busy, setBusy] = useState(false);
  if (!provider && !route && !credential) {
    return <aside className={`overview-inspector is-empty${open ? " is-open" : ""}`}><CircleGauge size={20} /><strong>Select a route signal</strong><p>Inspect the next key, limiting bucket and reset without leaving Overview.</p></aside>;
  }
  return (
    <aside className={`overview-inspector${open ? " is-open" : ""}`}>
      <header>
        <div><span>{credential ? "API key" : route ? "Public model" : "Provider"}</span><h2>{credential?.label || route?.alias || provider?.name}</h2></div>
        <button className="console-icon inspector-close" onClick={onClose} aria-label="Close inspector"><X size={15} /></button>
      </header>
      {route && (
        <div className="route-trace" aria-label="Selected route debugger trace">
          <TraceNode label="model" value={route.alias} />
          <ArrowRight size={15} />
          <TraceNode label="provider" value={route.provider} />
          <ArrowRight size={15} />
          <TraceNode label="next key" value={route.segments.find((segment) => segment.cursor)?.label || "none"} tone={!route.next_credential_id ? "danger" : "default"} />
          <ArrowRight size={15} />
          <TraceNode label="limit" value={formatRouteBottleneck(route)} />
        </div>
      )}
      {credential && (
        <>
          {credential.validation_error && <InlineNotice tone="danger">{credential.validation_error}</InlineNotice>}
          <div className="inspector-definition">
            <Definition label="Status" value={credential.status} />
            <Definition label="Secret" value={`•••• ${credential.secret_suffix}`} />
            <Definition label="Routing role" value={[credential.primary ? "primary" : "", credential.cursor ? "next" : ""].filter(Boolean).join(" · ") || "fallback"} />
            <Definition label="Validated" value={credential.last_validated_at ? formatRelativeTime(credential.last_validated_at) : "not recorded"} />
          </div>
          <HeadroomReadout label="Request limit" headroom={credential.request_headroom} />
          <HeadroomReadout label="Token limit" headroom={credential.token_headroom} />
        </>
      )}
      {!credential && provider && !route && (
        <>
          <div className="inspector-definition">
            <Definition label="Routes ready" value={`${provider.models_ready}/${provider.models_total}`} />
            <Definition label="Keys ready" value={`${provider.keys_ready}/${provider.keys_total}`} />
            <Definition label="Key warnings" value={String(provider.keys_warning)} />
            <Definition label="State" value={provider.enabled ? "enabled" : "disabled"} />
          </div>
          <div className="inspector-limit-grid">
            {capacityDimensions.map((dimension) => <LimitCell key={dimension} dimension={dimension} limit={provider.capacity[dimension]} />)}
          </div>
        </>
      )}
      {route && !credential && (
        <>
          <div className="inspector-definition">
            <Definition label="Provider" value={route.provider} />
            <Definition label="Upstream model" value={route.upstream_model} mono />
            <Definition label="Keys ready" value={`${route.healthy_credentials}/${route.total_credentials}`} />
            <Definition label="Selected traffic" value={`${route.requests} requests`} />
            <Definition label="Error rate" value={`${(route.error_rate * 100).toFixed(1)}%`} />
            <Definition label="P95 latency" value={`${route.latency_p95_ms} ms`} />
          </div>
          <HeadroomReadout label="Next request bucket" headroom={route.next_request_headroom} />
          <HeadroomReadout label="Next token bucket" headroom={route.next_token_headroom} />
          <div className="inspector-key-list">
            <span>Credential path</span>
            {route.segments.map((segment) => (
              <button key={segment.id} onClick={() => onSelectCredential(segment.id)}>
                <StatusDot state={segment.status} /><strong>{segment.label}</strong><small>{segment.cursor ? "next" : segment.status}</small><ChevronRight size={13} />
              </button>
            ))}
          </div>
        </>
      )}
      <div className="inspector-actions">
        {credential ? (
          <Button variant="quiet" disabled={busy} onClick={() => {
            setBusy(true);
            void onRecheck(credential.id).finally(() => setBusy(false));
          }}><RefreshCw size={14} /> {busy ? "Checking…" : "Re-check key"}</Button>
        ) : provider ? (
          <Button variant="quiet" disabled={busy} onClick={() => {
            setBusy(true);
            void onTest().finally(() => setBusy(false));
          }}><Activity size={14} /> {busy ? "Testing…" : "Test provider"}</Button>
        ) : null}
        {provider && <Button variant="quiet" onClick={() => navigate("providers", { provider: provider.id })}>Open provider</Button>}
        {route && <Button variant="quiet" onClick={() => navigate("providers", { provider: route.provider_id })}>Manage model limits</Button>}
        {route && <Button variant="quiet" onClick={() => navigate("logs", { q: route.alias })}>View route logs</Button>}
      </div>
    </aside>
  );
}

function TraceNode({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "danger" }) {
  return <span className={`trace-node trace-node--${tone}`}><small>{label}</small><strong>{value}</strong></span>;
}

function Definition({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return <div><span>{label}</span><strong className={mono ? "is-mono" : ""}>{value}</strong></div>;
}

function HeadroomReadout({ label, headroom }: { label: string; headroom?: Overview["routes"][number]["next_request_headroom"] }) {
  if (!headroom) {
    return <div className="headroom-readout is-unlimited"><span>{label}</span><strong>Unlimited</strong><small>No configured bucket limits this path.</small></div>;
  }
  const ratio = headroom.limit ? Math.max(0, Math.min(1, headroom.remaining / headroom.limit)) : 1;
  return (
    <div className={`headroom-readout ${ratio <= 0 ? "is-critical" : ratio <= 0.2 ? "is-warning" : ""}`}>
      <span>{label} · {headroom.scope}</span>
      <strong title={`${headroom.remaining.toLocaleString()} / ${headroom.limit.toLocaleString()} ${headroom.dimension.toUpperCase()}`}>{formatCompact(headroom.remaining)} / {formatCompact(headroom.limit)} {headroom.dimension}</strong>
      <div><i style={{ width: `${ratio * 100}%` }} /></div>
      <small>{headroom.reset_at ? <>Resets <LiveResetTime value={headroom.reset_at} /></> : "Per request limit"}</small>
    </div>
  );
}

function overviewTargetExists(overview: Overview, target: { type: "provider" | "route" | "credential"; id: string }) {
  if (target.type === "provider") return overview.providers.some((provider) => provider.id === target.id);
  if (target.type === "route") return overview.routes.some((route) => route.id === target.id);
  return overview.routes.some((route) => route.segments.some((segment) => segment.id === target.id));
}

function overviewProviderIDForTarget(overview: Overview, target: { type: "provider" | "route" | "credential"; id: string }) {
  if (target.type === "provider") return target.id;
  if (target.type === "route") return overview.routes.find((route) => route.id === target.id)?.provider_id;
  return overview.routes.find((route) => route.segments.some((segment) => segment.id === target.id))?.provider_id;
}

function timelinePath(values: number[], width: number, height: number) {
  if (values.length === 0) return "";
  const max = Math.max(...values, 1);
  return values.map((value, index) => {
    const x = values.length === 1 ? width / 2 : (index / (values.length - 1)) * width;
    const y = 8 + (height - 16) * (1 - value / max);
    return `${index ? "L" : "M"} ${x.toFixed(2)} ${y.toFixed(2)}`;
  }).join(" ");
}

function formatRouteBottleneck(route: Overview["routes"][number]) {
  const candidates = [route.next_request_headroom, route.next_token_headroom].filter(Boolean);
  if (!route.next_credential_id) return "no route";
  if (candidates.length === 0) return "unlimited";
  const tightest = candidates.sort((left, right) => (
    (left!.remaining / Math.max(left!.limit, 1)) - (right!.remaining / Math.max(right!.limit, 1))
  ))[0]!;
  return `${formatCompact(tightest.remaining)}/${formatCompact(tightest.limit)} ${tightest.dimension}`;
}

function formatModelBottleneck(route: Overview["routes"][number]) {
  const candidates = [route.next_request_headroom, route.next_token_headroom].filter((headroom) => headroom?.scope === "model");
  if (candidates.length === 0) return "Shared only";
  const tightest = candidates.sort((left, right) => (
    (left!.remaining / Math.max(left!.limit, 1)) - (right!.remaining / Math.max(right!.limit, 1))
  ))[0]!;
  return `${formatCompact(tightest.remaining)}/${formatCompact(tightest.limit)} ${tightest.dimension}`;
}

function LiveResetTime({ value }: { value: string }) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    setNow(Date.now());
    const timer = window.setInterval(() => setNow(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, [value]);
  return <time dateTime={value}>{formatResetTime(value, now)}</time>;
}

function formatResetTime(value: string, now = Date.now()) {
  const milliseconds = new Date(value).getTime() - now;
  if (milliseconds <= 0) return "now";
  if (milliseconds < 60_000) return `in ${Math.ceil(milliseconds / 1000)}s`;
  if (milliseconds < 3_600_000) return `in ${Math.ceil(milliseconds / 60_000)}m`;
  return `at ${new Date(value).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function formatRelativeTime(value: string) {
  const seconds = Math.max(0, Math.floor((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function ProvidersPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(location.search).get("provider") || "");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [openSection, setOpenSection] = useState<"models" | "credentials" | null>(null);
  const [providerInspectorOpen, setProviderInspectorOpen] = useState(false);
  const [panel, setPanel] = useState<
    null
    | { type: "wizard" }
    | { type: "provider"; provider: Provider }
    | { type: "model"; provider: Provider; model?: ModelRoute }
    | { type: "import"; provider: Provider }
    | { type: "credential"; provider: Provider; credential?: Credential }
  >(null);

  const load = useCallback(async () => {
    setError("");
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      const normalized = normalizeProviders(result.providers);
      setProviders(normalized);
      setSelectedID((current) => normalized.some((provider) => provider.id === current) ? current : normalized[0]?.id || "");
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : "Providers could not be loaded.");
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
    const refresh = window.setInterval(() => void load(), 10_000);
    return () => window.clearInterval(refresh);
  }, [load]);

  const selected = providers.find((provider) => provider.id === selectedID);
  useEffect(() => setOpenSection(null), [selectedID]);
  const complete = (message: string) => {
    setPanel(null);
    notify(message);
    void load();
  };

  return (
    <div className="resource-page provider-page">
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
                  onClick={() => { setSelectedID(provider.id); setProviderInspectorOpen(true); }}
                >
                  <StatusDot state={!provider.enabled ? "disabled" : healthy ? "healthy" : "exhausted"} />
                  <span><strong>{provider.name} <em className={`protocol-badge is-${provider.api_format}`}>{provider.api_format}</em></strong><small>{provider.base_url}</small></span>
                  <span className="resource-item__count">{provider.models.length} models</span>
                  <ChevronRight size={15} />
                </button>
              );
            })}
          </section>
          {selected && (
            <section className={`resource-inspector${providerInspectorOpen ? " is-open" : ""}`}>
              {selected.credentials.some((credential) => credential.validation_error) && (
                <InlineNotice tone="danger">
                  {selected.credentials.filter((credential) => credential.validation_error).length} API key warning detected. Open the marked key to replace or re-check it.
                </InlineNotice>
              )}
              <div className="inspector-header">
                <div>
                  <p className="eyebrow">{selected.api_format === "anthropic" ? "Anthropic-compatible upstream" : "OpenAI-compatible upstream"}</p>
                  <h2>{selected.name}</h2>
                  <code>{selected.base_url}</code>
                </div>
                <div className="button-row">
                  <button className="console-icon resource-inspector-close" onClick={() => setProviderInspectorOpen(false)} aria-label="Close provider inspector"><X size={15} /></button>
                  <Button variant="quiet" onClick={() => void testProvider(selected, notify)}><Activity size={15} /> Test</Button>
                  <Button variant="quiet" onClick={() => setPanel({ type: "provider", provider: selected })}>Edit</Button>
                  <Button
                    variant="danger"
                    aria-label={`Delete provider ${selected.name}`}
                    onClick={() => {
                      if (confirm(`Delete ${selected.name} and every route/credential under it?`)) {
                        void api<void>(`/api/admin/providers/${selected.id}`, { method: "DELETE" })
                          .then(() => complete("Provider deleted."))
                          .catch((caught) => notify(errorMessage(caught), "danger"));
                      }
                    }}
                  ><Trash2 size={14} /> Delete</Button>
                </div>
              </div>
              <div className="inspector-stats">
                <span><strong>{selected.models.length}</strong> public routes</span>
                <span><strong>{selected.credentials.length}</strong> credentials</span>
                <span><strong>{selected.timeout_seconds}s</strong> timeout</span>
              </div>
              <ProviderCapacityStrip provider={selected} />
              <ResourceDisclosure
                title="Model routes"
                description="Load the provider model catalog, select routes, or add one manually."
                summary={`${selected.models.length} route${selected.models.length === 1 ? "" : "s"}`}
                open={openSection === "models"}
                onToggle={() => setOpenSection((current) => current === "models" ? null : "models")}
                action={(
                  <div className="button-row">
                    <Button variant="quiet" onClick={() => setPanel({ type: "import", provider: selected })}><RefreshCw size={14} /> Load models</Button>
                    <Button variant="quiet" onClick={() => setPanel({ type: "model", provider: selected })}><Plus size={14} /> Manual</Button>
                  </div>
                )}
              >
                {selected.models.length === 0 ? (
                  <p className="inline-empty">No route can receive traffic yet.</p>
                ) : (
                  <div className="dense-table">
                    {selected.models.map((model) => (
                      <button key={model.id} className="dense-row" onClick={() => setPanel({ type: "model", provider: selected, model })}>
                        <StatusDot state={model.enabled ? "healthy" : "disabled"} />
                        <span><code>{model.public_alias}</code><small>→ {model.upstream_model}</small></span>
                        <span>
                          {model.supports_responses ? "Responses native" : "Responses translated"}
                          {model.supports_messages ? " · Messages" : ""}
                          {model.strip_parameters.length > 0 ? ` · removes ${model.strip_parameters.join(", ")}` : ""}
                        </span>
                        <ChevronRight size={14} />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceDisclosure>
              <ResourceDisclosure
                title="Credential pool"
                description="A primary key is tried first. Without one, healthy keys use balanced round-robin."
                summary={`${selected.credentials.filter((item) => item.enabled && item.status === "healthy").length}/${selected.credentials.length} keys ready`}
                open={openSection === "credentials"}
                onToggle={() => setOpenSection((current) => current === "credentials" ? null : "credentials")}
                action={<Button variant="quiet" onClick={() => setPanel({ type: "credential", provider: selected })}><Plus size={14} /> API key</Button>}
              >
                {selected.credentials.length === 0 ? (
                  <p className="inline-empty">No key is available for this provider.</p>
                ) : (
                  <div className="dense-table">
                    {selected.credentials.map((credential) => (
                      <button key={credential.id} className={`dense-row ${credential.validation_error ? "has-warning" : ""}`} onClick={() => setPanel({ type: "credential", provider: selected, credential })}>
                        <StatusDot state={credential.status} />
                        <span>
                          <strong>{credential.label}</strong>
                          <small>
                            {credential.validation_error
                              ? credential.validation_error
                              : `${credential.is_primary ? "PRIMARY · " : ""}•••• ${credential.secret_suffix}`}
                          </small>
                        </span>
                        <LimitSummary policy={credential.limits} />
                        <ChevronRight size={14} />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceDisclosure>
            </section>
          )}
        </div>
      )}
      {panel?.type === "wizard" && <ProviderWizard onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "provider" && <ProviderForm provider={panel.provider} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "model" && <ModelForm provider={panel.provider} model={panel.model} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "import" && <ModelImportForm provider={panel.provider} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panel?.type === "credential" && <CredentialForm provider={panel.provider} credential={panel.credential} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
    </div>
  );
}

function ResourceDisclosure({
  title,
  description,
  summary,
  action,
  open,
  onToggle,
  children
}: {
  title: string;
  description: string;
  summary: string;
  action: React.ReactNode;
  open: boolean;
  onToggle: () => void;
  children: React.ReactNode;
}) {
  return (
    <section className={`resource-disclosure${open ? " is-open" : ""}`}>
      <header>
        <button type="button" className="resource-disclosure__toggle" onClick={onToggle} aria-expanded={open}>
          <ChevronDown size={15} />
          <span><strong>{title}</strong><small>{description}</small></span>
          <code>{summary}</code>
        </button>
        <div className="resource-disclosure__actions">{action}</div>
      </header>
      {open && <div className="resource-disclosure__body">{children}</div>}
    </section>
  );
}

function LimitSummary({ policy }: { policy: RatePolicy }) {
  const active = Object.entries(policy).filter(([, value]) => value !== null);
  if (!active.length) return <span className="muted">No limits</span>;
  return <span className="mono-summary">{active.slice(0, 2).map(([key, value]) => `${key.toUpperCase()} ${value}`).join(" · ")}{active.length > 2 ? ` +${active.length - 2}` : ""}</span>;
}

const capacityDimensions = ["rps", "rpm", "rpd", "tps", "tpm", "tpd", "tpr"] as const;

function ProviderCapacityStrip({ provider }: { provider: Provider }) {
  const capacity = provider.capacity;
  return (
    <section className="pool-capacity" aria-label={`${provider.name} API key pool capacity`}>
      <header>
        <div>
          <p className="eyebrow">Pool arithmetic</p>
          <h3>Total provider capacity</h3>
        </div>
        <span>{capacity?.ready_keys ?? 0}/{capacity?.total_keys ?? provider.credentials.length} keys ready</span>
      </header>
      <div className="pool-capacity__limits">
        {capacityDimensions.map((dimension) => {
          const limit = capacity?.limits?.[dimension];
          return (
            <div key={dimension}>
              <span>{dimension.toUpperCase()}</span>
              <strong>
                {!limit
                  ? "—"
                  : limit.unlimited
                    ? "∞"
                    : limit.unknown
                      ? `? / ${formatCompact(limit.limit)}`
                      : `${formatCompact(limit.remaining)} / ${formatCompact(limit.limit)}`}
              </strong>
              <small>{dimension === "tpr" ? "max / request" : limit?.unlimited ? "unlimited" : "remaining / total"}</small>
            </div>
          );
        })}
      </div>
      <p>Shared limits from every ready API key are combined. Usage lowers remaining capacity; adding or removing a key recalculates the total.</p>
    </section>
  );
}

async function testProvider(provider: Pick<Provider, "id">, notify: (message: string, tone?: "success" | "danger") => void) {
  try {
    const result = await api<{ ok: boolean; valid: number; total: number }>(`/api/admin/providers/${provider.id}/test`, { method: "POST" });
    notify(
      result.ok ? `${result.valid}/${result.total} API keys are valid.` : `${result.total - result.valid} of ${result.total} API keys need attention.`,
      result.ok ? "success" : "danger"
    );
  } catch (caught) {
    notify(errorMessage(caught), "danger");
  }
}

function ProviderWizard({ onClose, onComplete, notify }: { onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [provider, setProvider] = useState<ProviderDraft>({
    name: "", base_url: "", auth_header: "Authorization", auth_scheme: "Bearer",
    api_format: "openai", anthropic_version: "2023-06-01",
    timeout_seconds: 120, enabled: true, allow_private_network: false, extra_headers: {}
  });
  const [credentialDrafts, setCredentialDrafts] = useState<CredentialDraft[]>(() => [newCredentialDraft()]);
  const [limits, setLimits] = useState<RatePolicy>(emptyPolicy);
  const [discoveredModels, setDiscoveredModels] = useState<DiscoveredModel[]>([]);
  const [selectedModels, setSelectedModels] = useState<Record<string, string>>({});
  const [modelAccessWarning, setModelAccessWarning] = useState("");
  const [unverifiedCredentialLabels, setUnverifiedCredentialLabels] = useState<string[]>([]);
  const [canContinueWithoutValidation, setCanContinueWithoutValidation] = useState(false);
  const steps = ["Provider", "API keys", "Models", "Review"];

  useEffect(() => {
    void api<Settings>("/api/admin/settings")
      .then((settings) => setProvider((current) => (
        current.name || current.base_url ? current : { ...current, timeout_seconds: settings.default_provider_timeout_seconds }
      )))
      .catch(() => undefined);
  }, []);

  const inspectKeys = async () => {
    const incomplete = credentialDrafts.some((credential) => Boolean(credential.label.trim()) !== Boolean(credential.secret.trim()));
    const credentials = credentialInputs(credentialDrafts, limits);
    if (incomplete || credentials.length === 0) {
      setError("Add at least one complete API key entry before loading models.");
      return;
    }
    setBusy(true);
    setError("");
    setModelAccessWarning("");
    setCanContinueWithoutValidation(false);
    setUnverifiedCredentialLabels([]);
    try {
      const inspections = await Promise.all(credentials.map((credential) => (
        api<CredentialInspection>("/api/admin/providers/inspect", {
          method: "POST",
          json: { provider, secret: credential.secret }
        })
      )));
      const models = mergeModelCatalogs(inspections.map((inspection) => inspection.models));
      setDiscoveredModels(models);
      setSelectedModels({});
      setModelAccessWarning(inspections.find((inspection) => inspection.valid && inspection.warning)?.warning ?? "");
      const invalid = inspections.findIndex((inspection) => !inspection.valid);
      if (invalid >= 0) {
        setUnverifiedCredentialLabels(credentials.filter((_, index) => !inspections[index].valid).map((credential) => credential.label));
        setCanContinueWithoutValidation(true);
        setError(`${credentials[invalid].label}: ${inspections[invalid].warning || "API key validation failed."}`);
        return;
      }
      setStep(2);
    } catch (caught) {
      setUnverifiedCredentialLabels(credentials.map((credential) => credential.label));
      setCanContinueWithoutValidation(true);
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  };

  const finish = async () => {
    setBusy(true);
    setError("");
    let createdID = "";
    try {
      const credentials = credentialInputs(credentialDrafts, limits, unverifiedCredentialLabels);
      const created = await api<{ id: string }>("/api/admin/providers", { method: "POST", json: provider });
      createdID = created.id;
      await api(`/api/admin/providers/${created.id}/credentials`, { method: "POST", json: { credentials } });
      const catalogIDs = new Set(discoveredModels.map((model) => model.id));
      const routes = routeInputsFromSelection(selectedModels, catalogIDs);
      if (routes.length > 0) {
        await api(`/api/admin/providers/${created.id}/models/bulk`, { method: "POST", json: { models: routes } });
      }
      const unverifiedCount = credentials.filter((credential) => credential.allow_unverified).length;
      const credentialSummary = unverifiedCount
        ? `${credentials.length} API key${credentials.length === 1 ? "" : "s"}, including ${unverifiedCount} saved without validation`
        : `${credentials.length} validated API key${credentials.length === 1 ? "" : "s"}`;
      onComplete(`Provider saved with ${credentialSummary} and ${routes.length} model route${routes.length === 1 ? "" : "s"}.`);
    } catch (caught) {
      if (createdID) {
        await api(`/api/admin/providers/${createdID}`, { method: "DELETE" }).catch(() => undefined);
      }
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
      {error && (
        <InlineNotice tone="danger">
          <div className="wizard-validation-notice">
            <span>{error}</span>
            {step === 1 && canContinueWithoutValidation && (
              <Button type="button" variant="quiet" onClick={() => {
                setError("");
                setStep(2);
              }}>Continue with manual model setup</Button>
            )}
          </div>
        </InlineNotice>
      )}
      {step === 0 && <ProviderFields value={provider} onChange={(next) => {
        setProvider(next);
        setDiscoveredModels([]);
        setSelectedModels({});
        setModelAccessWarning("");
        setUnverifiedCredentialLabels([]);
        setCanContinueWithoutValidation(false);
      }} />}
      {step === 1 && (
        <>
          <CredentialEntries value={credentialDrafts} onChange={(next) => {
            setCredentialDrafts(next);
            setDiscoveredModels([]);
            setSelectedModels({});
            setModelAccessWarning("");
            setUnverifiedCredentialLabels([]);
            setCanContinueWithoutValidation(false);
          }} />
          <fieldset><legend>Shared limits for these API keys</legend><p className="fieldset-note">Usage from every model under this provider consumes the same key limit. Blank means no limit.</p><RateFields value={limits} onChange={setLimits} /></fieldset>
        </>
      )}
      {step === 2 && (
        <>
          {modelAccessWarning && <InlineNotice>{modelAccessWarning}</InlineNotice>}
          <ModelCatalog
            provider={{ slug: providerSlugForUI(provider.name) }}
            models={discoveredModels}
            existing={[]}
            selected={selectedModels}
            onChange={setSelectedModels}
          />
        </>
      )}
      {step === 3 && (
        <div className="review-list">
          <div><span>Provider</span><strong>{provider.name || "Missing"}</strong><code>{provider.base_url || "Missing base URL"}</code></div>
          <div><span>Public models</span><strong>{Object.keys(selectedModels).length}</strong><small>{discoveredModels.length} discovered from the provider.</small></div>
          <div><span>API keys</span><strong>{credentialInputs(credentialDrafts, limits).length}</strong><small>{unverifiedCredentialLabels.length ? `${unverifiedCredentialLabels.length} saved without validation, then encrypted.` : "Validated again on save, then encrypted."}</small></div>
        </div>
      )}
      <div className="sheet-actions">
        {step > 0 && <Button variant="quiet" onClick={() => setStep(step - 1)} disabled={busy}>Back</Button>}
        <span />
        {step < steps.length - 1 ? (
          <Button disabled={busy} onClick={() => {
            setError("");
            if (step === 0) {
              if (!provider.name.trim() || !provider.base_url.trim()) {
                setError("Provider name and base URL are required.");
                return;
              }
              setStep(1);
              return;
            }
            if (step === 1) {
              void inspectKeys();
              return;
            }
            setStep(step + 1);
          }}>{busy ? "Checking API keys…" : step === 1 ? "Check keys & load models" : "Continue"}</Button>
        ) : (
          <Button onClick={() => void finish()} disabled={busy}>{busy ? "Creating provider…" : "Create provider"}</Button>
        )}
      </div>
    </Sheet>
  );
}

type ProviderDraft = {
  name: string; base_url: string; auth_header: string; auth_scheme: string;
  api_format: "openai" | "anthropic"; anthropic_version: string;
  timeout_seconds: number; enabled: boolean; allow_private_network: boolean; extra_headers: Record<string, string>;
};

const GEMINI_OPENAI_BASE_URL = "https://generativelanguage.googleapis.com/v1beta/openai/";

function geminiCompatibilitySuggestion(baseURL: string, apiFormat: ProviderDraft["api_format"]) {
  if (apiFormat !== "openai") return "";
  try {
    const parsed = new URL(baseURL.trim());
    if (parsed.hostname.toLowerCase() === "generativelanguage.googleapis.com" && parsed.pathname.replace(/\/+$/, "") === "/v1beta") {
      return GEMINI_OPENAI_BASE_URL;
    }
  } catch {
    // The regular URL validation remains responsible for incomplete input.
  }
  return "";
}

function ProviderFields({ value, onChange }: { value: ProviderDraft; onChange: (value: ProviderDraft) => void }) {
  const geminiSuggestion = geminiCompatibilitySuggestion(value.base_url, value.api_format);
  const useOfficialPreset = (kind: "openai" | "anthropic") => onChange({
    ...value,
    name: value.name.trim() || (kind === "openai" ? "OpenAI" : "Anthropic"),
    base_url: kind === "openai" ? "https://api.openai.com/v1" : "https://api.anthropic.com/v1",
    api_format: kind,
    auth_header: kind === "openai" ? "Authorization" : "X-Api-Key",
    auth_scheme: kind === "openai" ? "Bearer" : "",
    anthropic_version: value.anthropic_version || "2023-06-01"
  });
  return (
    <div className="form-stack">
      <div className="button-row">
        <span className="muted">Official provider setup</span>
        <Button type="button" variant="quiet" onClick={() => useOfficialPreset("openai")}>Use OpenAI</Button>
        <Button type="button" variant="quiet" onClick={() => useOfficialPreset("anthropic")}>Use Anthropic</Button>
      </div>
      <label className="field"><span>API protocol <small>Controls authentication, validation and upstream request format</small></span><select value={value.api_format} onChange={(event) => {
        const api_format = event.target.value as "openai" | "anthropic";
        onChange({
          ...value,
          api_format,
          auth_header: api_format === "anthropic" ? "X-Api-Key" : "Authorization",
          auth_scheme: api_format === "anthropic" ? "" : "Bearer",
          anthropic_version: value.anthropic_version || "2023-06-01"
        });
      }}><option value="openai">OpenAI-compatible</option><option value="anthropic">Anthropic-compatible</option></select></label>
      <label className="field"><span>Name <small>An internal identifier is generated automatically</small></span><input required placeholder="Groq production" value={value.name} onChange={(e) => onChange({ ...value, name: e.target.value })} /></label>
      <label className="field"><span>{value.api_format === "anthropic" ? "Anthropic-compatible" : "OpenAI-compatible"} base URL <small>Include the provider's compatibility prefix</small></span><input type="url" required placeholder={value.api_format === "anthropic" ? "https://api.anthropic.com/v1" : "https://api.provider.com/v1"} value={value.base_url} onChange={(e) => onChange({ ...value, base_url: e.target.value })} /></label>
      {geminiSuggestion && <InlineNotice>
        Gemini native API URL detected. OpenAI compatibility requires <code>{geminiSuggestion}</code>{" "}
        <button type="button" className="button button--quiet" onClick={() => onChange({ ...value, base_url: geminiSuggestion })}>Use compatible URL</button>
      </InlineNotice>}
      <div className="field-pair">
        <label className="field"><span>Authentication header</span><input value={value.auth_header} onChange={(e) => onChange({ ...value, auth_header: e.target.value })} /></label>
        <label className="field">
          <span>Authentication scheme</span>
          <input
            placeholder={value.api_format === "anthropic" ? "Leave blank" : "Bearer"}
            value={value.auth_scheme}
            disabled={value.api_format === "anthropic"}
            onChange={(e) => onChange({ ...value, auth_scheme: e.target.value })}
          />
        </label>
      </div>
      {value.api_format === "anthropic" && <label className="field"><span>Anthropic API version <small>Sent upstream with every request</small></span><input value={value.anthropic_version} onChange={(e) => onChange({ ...value, anthropic_version: e.target.value })} /></label>}
      <label className="field"><span>Timeout seconds</span><input type="number" min={1} max={900} value={value.timeout_seconds} onChange={(e) => onChange({ ...value, timeout_seconds: Number(e.target.value) })} /></label>
      <Toggle checked={value.enabled} onChange={(enabled) => onChange({ ...value, enabled })} label="Enable provider" description="Disabled providers are never considered for routing." />
      <Toggle checked={value.allow_private_network} onChange={(allow_private_network) => onChange({ ...value, allow_private_network })} label="Allow private-network target" description="Also permits HTTP. Enable only for a provider you operate on this VPS or LAN." />
    </div>
  );
}

function ProviderForm({ provider, onClose, onComplete, notify }: { provider: Provider; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [draft, setDraft] = useState<ProviderDraft>({
    name: provider.name, base_url: provider.base_url,
    auth_header: provider.auth_header, auth_scheme: provider.auth_scheme,
    api_format: provider.api_format ?? "openai", anthropic_version: provider.anthropic_version ?? "2023-06-01",
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

type ModelDraft = Omit<ModelRoute, "id" | "provider_id" | "created_at" | "updated_at" | "capability_status" | "capability_profile" | "capabilities_checked_at" | "capability_error"> & { manual?: boolean };

function ModelFields({ value, onChange }: { value: ModelDraft; onChange: (value: ModelDraft) => void }) {
  return (
    <div className="form-stack">
      <label className="field"><span>Public model alias <small>Applications put this in the model field</small></span><input required placeholder="groq/llama-3.3-70b" value={value.public_alias} onChange={(e) => onChange({ ...value, public_alias: e.target.value })} /></label>
      <label className="field"><span>Upstream model ID</span><input required placeholder="llama-3.3-70b-versatile" value={value.upstream_model} onChange={(e) => onChange({ ...value, upstream_model: e.target.value })} /></label>
      <div className="field-pair">
        <label className="field"><span>Default max output tokens</span><input type="number" min={1} value={value.default_max_output_tokens} onChange={(e) => onChange({ ...value, default_max_output_tokens: Number(e.target.value) })} /></label>
        <label className="field"><span>Tokenizer profile</span><select value={value.tokenizer} onChange={(e) => onChange({ ...value, tokenizer: e.target.value })}><option value="heuristic">Conservative heuristic</option><option value="cl100k_base">cl100k_base</option><option value="o200k_base">o200k_base</option></select></label>
      </div>
      <div className="pricing-fields">
        <label className="field"><span>Input price <small>USD per 1M tokens</small></span><input type="number" min={0} step="0.000001" value={value.input_cost_per_million_usd} onChange={(e) => onChange({ ...value, input_cost_per_million_usd: Number(e.target.value) })} /></label>
        <label className="field"><span>Output price <small>USD per 1M tokens</small></span><input type="number" min={0} step="0.000001" value={value.output_cost_per_million_usd} onChange={(e) => onChange({ ...value, output_cost_per_million_usd: Number(e.target.value) })} /></label>
        <label className="field"><span>Per request price <small>Optional USD per request</small></span><input type="number" min={0} step="0.000001" placeholder="Optional" value={value.request_cost_usd ?? ""} onChange={(e) => onChange({ ...value, request_cost_usd: e.target.value === "" ? undefined : Number(e.target.value) })} /></label>
      </div>
      <Toggle checked={value.supports_chat} onChange={(supports_chat) => onChange({ ...value, supports_chat })} label="Upstream supports Chat Completions" />
      <Toggle checked={value.supports_responses} onChange={(supports_responses) => onChange({ ...value, supports_responses })} label="Upstream supports Responses natively" description="When off, the gateway translates the supported Responses subset to Chat Completions." />
      <Toggle checked={value.supports_messages} onChange={(supports_messages) => onChange({ ...value, supports_messages })} label="Expose through Anthropic Messages" description="Allows this public alias on /v1/messages. Cross-protocol routes support the lossless text, image and client-tool subset." />
      <label className="field">
        <span>Remove unsupported request fields <small>Top-level names, comma separated. Changes apply only to this route.</small></span>
        <input
          placeholder="thinking"
          value={value.strip_parameters.join(", ")}
          onChange={(event) => onChange({
            ...value,
            strip_parameters: event.target.value.split(",").map((item) => item.trim()).filter(Boolean)
          })}
        />
      </label>
      <Toggle checked={value.capture_bodies} onChange={(capture_bodies) => onChange({ ...value, capture_bodies })} label="Capture encrypted request and response bodies" description="Off by default. Captured bodies follow the configured retention window." />
      <Toggle checked={value.enabled} onChange={(enabled) => onChange({ ...value, enabled })} label="Enable public route" />
    </div>
  );
}

function ModelForm({ provider, model, onClose, onComplete, notify }: { provider: Provider; model?: ModelRoute; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const routingMode = useRoutingMode();
  const [draft, setDraft] = useState<ModelDraft>(model ? {
    public_alias: model.public_alias, upstream_model: model.upstream_model,
    supports_chat: model.supports_chat, supports_responses: model.supports_responses, supports_messages: model.supports_messages,
    default_max_output_tokens: model.default_max_output_tokens, tokenizer: model.tokenizer,
    input_cost_per_million_usd: model.input_cost_per_million_usd, output_cost_per_million_usd: model.output_cost_per_million_usd, request_cost_usd: model.request_cost_usd,
    capture_bodies: model.capture_bodies, strip_parameters: model.strip_parameters ?? [], enabled: model.enabled
  } : {
    public_alias: `${provider.slug}/`, upstream_model: "", supports_chat: true,
    supports_responses: false, supports_messages: true, default_max_output_tokens: 1024,
    tokenizer: "heuristic", input_cost_per_million_usd: 0, output_cost_per_million_usd: 0, request_cost_usd: undefined,
    capture_bodies: false, strip_parameters: [], enabled: true
  });
  const [busy, setBusy] = useState(false);
  // The mode arrives after the first render, so the untouched provider-prefixed
  // default is cleared once model-wise routing is confirmed.
  useEffect(() => {
    if (model || routingMode !== "model") return;
    setDraft((current) => current.public_alias === `${provider.slug}/` ? { ...current, public_alias: "" } : current);
  }, [model, provider.slug, routingMode]);
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
    <Sheet title={model ? "Edit model route" : "Add model route"} eyebrow={provider.name} onClose={onClose} actions={model ? <Button variant="danger" onClick={() => {
      if (confirm(`Delete route ${model.public_alias}?`)) void api(`/api/admin/models/${model.id}`, { method: "DELETE" }).then(() => onComplete("Model route deleted.")).catch((caught) => notify(errorMessage(caught), "danger"));
    }}><Trash2 size={14} /> Delete model</Button> : undefined}>
      <ModelFields value={draft} onChange={setDraft} />
      <div className="sheet-actions">
        <span />
        <Button disabled={busy} onClick={() => void save()}>{busy ? "Saving…" : model ? "Save route" : "Create route"}</Button>
      </div>
    </Sheet>
  );
}

type CredentialInspection = {
  valid: boolean;
  catalog_available: boolean;
  protocol_verified: boolean;
  protocol: "openai" | "anthropic";
  detected_protocol?: "openai" | "anthropic";
  status_code: number;
  latency_ms: number;
  models: DiscoveredModel[];
  warning?: string;
};

function CredentialForm({ provider, credential, onClose, onComplete, notify }: { provider: Provider; credential?: Credential; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [label, setLabel] = useState(credential?.label ?? "");
  const [secret, setSecret] = useState("");
  const [isPrimary, setIsPrimary] = useState(credential?.is_primary ?? false);
  const [enabled, setEnabled] = useState(credential?.enabled ?? true);
  const [limits, setLimits] = useState<RatePolicy>(credential?.limits ?? emptyPolicy());
  const [inspection, setInspection] = useState<CredentialInspection | null>(null);
  const [checkedSecret, setCheckedSecret] = useState("");
  const [selectedModels, setSelectedModels] = useState<Record<string, string>>({});
  const [selectedModel, setSelectedModel] = useState(provider.models[0]?.id ?? "");
  const [modelLimits, setModelLimits] = useState<RatePolicy>(() => credential?.model_limits[provider.models[0]?.id] ?? emptyPolicy());
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setModelLimits(credential?.model_limits[selectedModel] ?? emptyPolicy());
  }, [selectedModel, credential]);

  const checkKey = async () => {
    setBusy(true);
    setInspection(null);
    setSelectedModels({});
    try {
      let result: CredentialInspection;
      if (credential && !secret.trim()) {
        result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/models/discover`, {
          method: "POST",
          json: { credential_id: credential.id }
        });
      } else {
        if (secret.trim().length < 8) {
          notify("Enter a complete API key before checking it.", "danger");
          return;
        }
        result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/credentials/inspect`, {
          method: "POST",
          json: { secret }
        });
      }
      setInspection(result);
      setCheckedSecret(secret.trim());
      if (!result.valid) {
        notify(result.warning || "The provider rejected this API key.", "danger");
      }
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!inspection?.valid || (secret.trim() && checkedSecret !== secret.trim())) {
      await checkKey();
      return;
    }
    setBusy(true);
    try {
      let discovered: DiscoveredModel[] = inspection.models;
      if (credential) {
        const result = await api<{ models: DiscoveredModel[] }>(`/api/admin/credentials/${credential.id}`, {
          method: "PUT",
          json: { label, secret, is_primary: isPrimary, enabled, limits }
        });
        discovered = result.models ?? discovered;
      } else {
        const credentials = [{ label, secret, is_primary: isPrimary, enabled, limits }];
        const result = await api<{ models: DiscoveredModel[] }>(`/api/admin/providers/${provider.id}/credentials`, {
          method: "POST",
          json: { credentials }
        });
        discovered = result.models ?? discovered;
      }
      const discoveredIDs = new Set(discovered.map((model) => model.id));
      const routes = routeInputsFromSelection(selectedModels, discoveredIDs);
      if (routes.length > 0) {
        await api(`/api/admin/providers/${provider.id}/models/bulk`, {
          method: "POST",
          json: { models: routes }
        });
      }
      onComplete(`${credential ? "API key updated" : "API key added"} and ${routes.length} model route${routes.length === 1 ? "" : "s"} enabled.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet title={credential ? `Edit ${credential.label}` : "Add API key"} eyebrow={provider.name} onClose={onClose} wide actions={credential ? <Button variant="danger" onClick={() => {
      if (confirm(`Delete API key ${credential.label}?`)) void api(`/api/admin/credentials/${credential.id}`, { method: "DELETE" }).then(() => onComplete("API key deleted.")).catch((caught) => notify(errorMessage(caught), "danger"));
    }}><Trash2 size={14} /> Delete key</Button> : undefined}>
      {credential?.validation_error && <InlineNotice tone="danger">{credential.validation_error}</InlineNotice>}
      <div className="field-pair">
        <label className="field"><span>Label</span><input required placeholder="Production key" value={label} onChange={(e) => setLabel(e.target.value)} /></label>
        <label className="field"><span>{credential ? "Replacement API key" : "API key"} <small>{credential ? "Leave blank to check the saved key" : ""}</small></span><input type="password" required={!credential} autoComplete="off" value={secret} onChange={(e) => {
          setSecret(e.target.value);
          setInspection(null);
          setSelectedModels({});
        }} /></label>
      </div>
      <Toggle checked={isPrimary} onChange={setIsPrimary} label="Use as primary" description="Optional. This key is tried first while it has capacity; other keys remain fallbacks." />
      <Toggle checked={enabled} onChange={setEnabled} label="Enable API key" description="Re-enabling also clears quarantine and circuit-breaker state." />
      <div className="validation-action">
        <div>
          <strong>Validate key and discover models</strong>
          <small>Rotakey calls the provider’s `/models` endpoint now and checks again when saving.</small>
        </div>
        <Button type="button" variant="quiet" disabled={busy} onClick={() => void checkKey()}>
          <RefreshCw size={14} /> {busy ? "Checking…" : "Check & load models"}
        </Button>
      </div>
      {inspection && (
        <InlineNotice tone={inspection.valid ? "success" : "danger"}>
          {inspection.valid
            ? `API key and ${inspection.protocol} base URL verified · ${inspection.models.length} models loaded · ${inspection.latency_ms} ms`
            : inspection.warning || "API key validation failed."}
        </InlineNotice>
      )}
      {inspection?.valid && (
        <ModelCatalog
          provider={provider}
          models={inspection.models}
          existing={provider.models}
          selected={selectedModels}
          onChange={setSelectedModels}
        />
      )}
      <fieldset><legend>Shared API key limits</legend><p className="fieldset-note">Requests from every model under this provider consume these limits together. Blank means no limit.</p><RateFields value={limits} onChange={setLimits} /></fieldset>
      {credential && provider.models.length > 0 && (
        <fieldset>
          <legend>Optional model-specific limit</legend>
          <p className="fieldset-note">Leave this unset to use only the shared key limit. When set, both shared and model limits must have capacity.</p>
          <label className="field"><span>Model route</span><select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)}>{provider.models.map((model) => <option key={model.id} value={model.id}>{model.public_alias}</option>)}</select></label>
          <RateFields value={modelLimits} onChange={setModelLimits} compact />
          <div className="button-row">
            <Button variant="quiet" onClick={() => void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "PUT", json: modelLimits }).then(() => onComplete("Model-specific limits saved.")).catch((caught) => notify(errorMessage(caught), "danger"))}>Save model limit</Button>
            {credential.model_limits[selectedModel] && <Button variant="quiet" onClick={() => void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "DELETE" }).then(() => onComplete("Model-specific limits removed.")).catch((caught) => notify(errorMessage(caught), "danger"))}>Use shared only</Button>}
          </div>
        </fieldset>
      )}
      <div className="sheet-actions">
        <span />
        <Button disabled={busy} onClick={() => void save()}>{busy ? "Working…" : inspection?.valid ? credential ? "Save API key & routes" : "Add API key & routes" : "Check API key first"}</Button>
      </div>
    </Sheet>
  );
}

function ModelImportForm({ provider, onClose, onComplete, notify }: { provider: Provider; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [inspection, setInspection] = useState<CredentialInspection | null>(null);
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  const load = async () => {
    setBusy(true);
    setInspection(null);
    setSelected({});
    try {
      const result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/models/discover`, {
        method: "POST",
        json: {}
      });
      setInspection(result);
      if (!result.valid) notify(result.warning || "Models could not be loaded.", "danger");
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => { void load(); }, []);

  const save = async () => {
    const routes = routeInputsFromSelection(selected, new Set((inspection?.models ?? []).map((model) => model.id)));
    if (routes.length === 0) {
      notify("Select at least one new model route.", "danger");
      return;
    }
    setBusy(true);
    try {
      const result = await api<{ created: number; skipped: number }>(`/api/admin/providers/${provider.id}/models/bulk`, {
        method: "POST",
        json: { models: routes }
      });
      onComplete(`${result.created} model route${result.created === 1 ? "" : "s"} enabled${result.skipped ? ` · ${result.skipped} already existed` : ""}.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet title="Load provider models" eyebrow={provider.name} onClose={onClose} wide>
      <div className="validation-action">
        <div><strong>Provider model catalog</strong><small>Uses the primary API key first, then records whether that key is valid.</small></div>
        <Button variant="quiet" disabled={busy} onClick={() => void load()}><RefreshCw size={14} /> Reload</Button>
      </div>
      {busy && !inspection && <PageSkeleton />}
      {inspection && (
        <InlineNotice tone={inspection.valid ? "success" : "danger"}>
          {inspection.valid
            ? `${inspection.models.length} models loaded in ${inspection.latency_ms} ms.`
            : inspection.warning || "The API key could not load models."}
        </InlineNotice>
      )}
      {inspection?.valid && (
        <ModelCatalog
          provider={provider}
          models={inspection.models}
          existing={provider.models}
          selected={selected}
          onChange={setSelected}
        />
      )}
      <div className="sheet-actions"><span /><Button disabled={busy || !inspection?.valid} onClick={() => void save()}>{busy ? "Saving…" : `Enable ${Object.keys(selected).length} selected`}</Button></div>
    </Sheet>
  );
}

function ModelCatalog({
  provider,
  models,
  existing,
  selected,
  onChange
}: {
  provider: Pick<Provider, "slug">;
  models: DiscoveredModel[];
  existing: ModelRoute[];
  selected: Record<string, string>;
  onChange: (selected: Record<string, string>) => void;
}) {
  const [query, setQuery] = useState("");
  const [manualModel, setManualModel] = useState("");
  const routingMode = useRoutingMode();
  const existingIDs = useMemo(() => new Set(existing.map((model) => model.upstream_model)), [existing]);
  const catalogModels = useMemo(() => {
    const known = new Map(models.map((model) => [model.id, model]));
    Object.keys(selected).forEach((id) => { if (!known.has(id)) known.set(id, { id, owned_by: "Manual model ID" }); });
    return [...known.values()];
  }, [models, selected]);
  const visible = catalogModels.filter((model) => {
    const needle = query.trim().toLowerCase();
    return !needle || model.id.toLowerCase().includes(needle) || model.owned_by?.toLowerCase().includes(needle);
  });
  const selectable = visible.filter((model) => !existingIDs.has(model.id));
  const selectedCount = Object.keys(selected).length;
  const selectedVisibleCount = selectable.filter((model) => selected[model.id] !== undefined).length;
  const allVisibleSelected = selectable.length > 0 && selectedVisibleCount === selectable.length;
  const toggleVisible = (checked: boolean) => {
    const next = { ...selected };
    for (const model of selectable) {
      if (checked) next[model.id] = next[model.id] || defaultPublicAlias(provider.slug, model.id, routingMode);
      else delete next[model.id];
    }
    onChange(next);
  };
  return (
    <section className="model-catalog">
      <header>
        <div><strong>Select public models</strong><small>{selectedCount} selected · {existingIDs.size} already routed</small></div>
        <div className="button-row">
          {selectedCount > 0 && <Button type="button" variant="quiet" onClick={() => onChange({})}>Clear all selected</Button>}
        </div>
      </header>
      <label className="catalog-search">
        <Search size={15} />
        <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter model IDs" />
      </label>
      <label className="catalog-select-all">
        <input type="checkbox" checked={allVisibleSelected} disabled={selectable.length === 0} onChange={(event) => toggleVisible(event.target.checked)} />
        <span>
          <strong>{query.trim() ? "Select all search results" : "Select all loaded models"}</strong>
          <small>{selectedVisibleCount}/{selectable.length} available results selected{existingIDs.size ? ` · ${existingIDs.size} already routed` : ""}</small>
        </span>
      </label>
      <div className="manual-model-entry">
        <label className="field"><span>Manual model ID <small>Use this when the provider does not expose a model catalog</small></span><input value={manualModel} onChange={(event) => setManualModel(event.target.value)} placeholder="claude-model-id" /></label>
        <Button type="button" variant="quiet" disabled={!manualModel.trim() || existingIDs.has(manualModel.trim())} onClick={() => {
          const upstream = manualModel.trim();
          if (!upstream) return;
          onChange({ ...selected, [upstream]: defaultPublicAlias(provider.slug, upstream, routingMode) });
          setManualModel("");
        }}><Plus size={14} /> Add model ID</Button>
      </div>
      <div className="model-catalog__list">
        {visible.map((model) => {
          const alreadyRouted = existingIDs.has(model.id);
          const checked = alreadyRouted || selected[model.id] !== undefined;
          return (
            <div className={`catalog-model ${alreadyRouted ? "is-existing" : ""}`} key={model.id}>
              <label>
                <input
                  type="checkbox"
                  checked={checked}
                  disabled={alreadyRouted}
                  onChange={(event) => {
                    const next = { ...selected };
                    if (event.target.checked) next[model.id] = defaultPublicAlias(provider.slug, model.id, routingMode);
                    else delete next[model.id];
                    onChange(next);
                  }}
                />
                <span><code>{model.id}</code><small>{alreadyRouted ? "Already routed" : model.owned_by || "Provider model"}</small></span>
              </label>
              {!alreadyRouted && selected[model.id] !== undefined && (
                <label className="catalog-alias">
                  <span>Public alias</span>
                  <input value={selected[model.id]} onChange={(event) => onChange({ ...selected, [model.id]: event.target.value })} />
                </label>
              )}
            </div>
          );
        })}
        {visible.length === 0 && <p className="inline-empty">No provider models match this filter.</p>}
      </div>
    </section>
  );
}

type ModelProbeResult = {
  state: "checking" | "passed" | "blocked" | "failed";
  error?: string;
};

function ModelsPage({
  navigate,
  notify
}: {
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [credentialsOpen, setCredentialsOpen] = useState(false);
  const [modelInspectorOpen, setModelInspectorOpen] = useState(false);
  const [probeResults, setProbeResults] = useState<Record<string, ModelProbeResult>>({});
  const [probeProgress, setProbeProgress] = useState({ completed: 0, total: 0, passed: 0, blocked: 0, failed: 0 });
  const [bulkChecking, setBulkChecking] = useState(false);
  const [deletingFailed, setDeletingFailed] = useState(false);
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(location.search).get("model") || "");
  const routingMode = useRoutingMode();
  const load = useCallback(async () => {
    setLoading(true);
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      const normalized = normalizeProviders(result.providers);
      setProviders(normalized);
      const available = normalized.flatMap((provider) => provider.models);
      setSelectedID((current) => available.some((model) => model.id === current) ? current : available[0]?.id || "");
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setLoading(false);
    }
  }, [notify]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => setCredentialsOpen(false), [selectedID]);
  const models = providers.flatMap((provider) => provider.models.map((model) => ({ ...model, provider, credentials: provider.credentials })));
  const filtered = models.filter((model) => {
    const needle = query.trim().toLowerCase();
    const matchesProvider = !providerFilter || model.provider.id === providerFilter;
    const matchesQuery = !needle || model.public_alias.toLowerCase().includes(needle) ||
      model.upstream_model.toLowerCase().includes(needle) || model.provider.name.toLowerCase().includes(needle);
    return matchesProvider && matchesQuery;
  });
  const selected = models.find((model) => model.id === selectedID);
  const poolSizes = poolSizeByAlias(providers);
  const healthyKeyCount = (model: (typeof models)[number]) => model.credentials.filter((item) => item.enabled && item.status === "healthy").length;
  const failedProbeIDs = models.filter((model) => healthyKeyCount(model) > 0 && (probeResults[model.id]?.state === "failed" || (!probeResults[model.id] && model.capability_status === "failed"))).map((model) => model.id);

  const checkAllModels = async () => {
    if (bulkChecking || models.length === 0) return;
    const targets = models.filter((model) => healthyKeyCount(model) > 0);
    const blockedModels = models.filter((model) => healthyKeyCount(model) === 0);
    setProbeResults(Object.fromEntries(models.map((model) => [model.id, healthyKeyCount(model) > 0
      ? { state: "checking" as const }
      : { state: "blocked" as const, error: "waiting for a healthy API key" }])));
    setProbeProgress({ completed: blockedModels.length, total: models.length, passed: 0, blocked: blockedModels.length, failed: 0 });
    setBulkChecking(true);
    let cursor = 0;
    let completed = 0;
    let passed = 0;
    let blocked = blockedModels.length;
    let failed = 0;
    const worker = async () => {
      while (cursor < targets.length) {
        const model = targets[cursor++];
        try {
          await api(`/api/admin/models/${model.id}/probe`, { method: "POST" });
          passed++;
          setProbeResults((current) => ({ ...current, [model.id]: { state: "passed" } }));
        } catch (caught) {
          if (caught instanceof APIError && caught.code === "model_probe_blocked") {
            blocked++;
            setProbeResults((current) => ({ ...current, [model.id]: { state: "blocked", error: errorMessage(caught) } }));
          } else {
            failed++;
            setProbeResults((current) => ({ ...current, [model.id]: { state: "failed", error: errorMessage(caught) } }));
          }
        } finally {
          completed++;
          setProbeProgress({ completed: completed + blockedModels.length, total: models.length, passed, blocked, failed });
        }
      }
    };
    await worker();
    setBulkChecking(false);
    notify(`${passed} model${passed === 1 ? "" : "s"} live · ${blocked} waiting for keys${failed ? ` · ${failed} unavailable` : ""}.`, failed ? "danger" : "success");
    await load();
  };

  const deleteFailedModels = async () => {
    if (deletingFailed || failedProbeIDs.length === 0) return;
    const aliases = models.filter((model) => failedProbeIDs.includes(model.id)).map((model) => model.public_alias);
    const preview = aliases.slice(0, 12).join("\n");
    const remainder = aliases.length > 12 ? `\n…and ${aliases.length - 12} more` : "";
    if (!confirm(`Delete ${aliases.length} failed model route${aliases.length === 1 ? "" : "s"}? Requests using these aliases will stop immediately.\n\n${preview}${remainder}`)) return;
    setDeletingFailed(true);
    let deleted = 0;
    let deleteErrors = 0;
    for (const id of failedProbeIDs) {
      try {
        await api(`/api/admin/models/${id}`, { method: "DELETE" });
        deleted++;
        setProbeResults((current) => {
          const next = { ...current };
          delete next[id];
          return next;
        });
      } catch (caught) {
        deleteErrors++;
        setProbeResults((current) => ({ ...current, [id]: { state: "failed", error: `Delete failed: ${errorMessage(caught)}` } }));
      }
    }
    setDeletingFailed(false);
    notify(`${deleted} failed model route${deleted === 1 ? "" : "s"} deleted${deleteErrors ? ` · ${deleteErrors} could not be deleted` : ""}.`, deleteErrors ? "danger" : "success");
    await load();
  };

  return (
    <div className="resource-page model-page">
      <PageHeader eyebrow="Public contract" title="Model routes" description={routingMode === "model" ? "Model-wise routing: aliases sharing a name are served as one pool, rotating across providers and API keys. Callers see the name once." : "Inspect aliases, apply one model limit to one or every API key, and open only the credential detail you need."} />
      {loading ? <PageSkeleton /> : models.length === 0 ? <EmptyState title="No public models" description="Create a route inside a provider to expose it through /v1/models." /> : (
        <div className="ide-resource-workbench">
          <section className="ide-resource-list">
            <div className="ide-filter model-filter">
              <Search size={14} />
              <label><span className="sr-only">Filter model routes</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter aliases or upstream IDs" /></label>
              <label><span className="sr-only">Provider</span><select value={providerFilter} onChange={(event) => setProviderFilter(event.target.value)}><option value="">All providers</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></label>
            </div>
            <section className={`model-sweep${bulkChecking ? " is-running" : ""}`} aria-live="polite">
              <div className="model-sweep__readout">
                <span>Live model sweep</span>
                <strong>{bulkChecking ? `${probeProgress.completed}/${probeProgress.total} checked` : probeProgress.total ? `${probeProgress.passed} live · ${probeProgress.blocked} waiting${probeProgress.failed ? ` · ${probeProgress.failed} unavailable` : ""}` : `${models.filter((model) => healthyKeyCount(model) > 0).length} routes ready · ${models.filter((model) => healthyKeyCount(model) === 0).length} waiting for keys`}</strong>
              </div>
              <div className="model-sweep__track" role="progressbar" aria-label="Model check progress" aria-valuemin={0} aria-valuemax={probeProgress.total || models.length} aria-valuenow={probeProgress.completed}>
                <span style={{ width: `${probeProgress.total ? (probeProgress.completed / probeProgress.total) * 100 : 0}%` }} />
              </div>
              <div className="button-row">
                {failedProbeIDs.length > 0 && <Button variant="danger" disabled={bulkChecking || deletingFailed} onClick={() => void deleteFailedModels()}><Trash2 size={13} /> {deletingFailed ? "Deleting…" : `Delete ${failedProbeIDs.length} failed`}</Button>}
                <Button variant="quiet" disabled={bulkChecking || deletingFailed} onClick={() => void checkAllModels()}><Activity size={13} /> {bulkChecking ? "Checking all…" : "Check all models"}</Button>
              </div>
            </section>
            <header><span>Public alias</span><span>Provider</span><span>Keys</span></header>
            <div>
              {filtered.map((model) => {
                const ready = model.credentials.filter((item) => item.enabled && item.status === "healthy").length;
                const probe = probeResults[model.id];
                const capabilityLabel = ready === 0 ? "waiting for a healthy API key" : probe?.state === "checking" ? "checking now" : probe?.state === "passed" ? "live" : probe?.state === "blocked" ? `waiting · ${probe.error || "healthy API key required"}` : probe?.state === "failed" ? `unavailable · ${probe.error || "probe failed"}` : model.capability_status === "probe_verified" ? "probe verified" : model.capability_status === "catalog_verified" ? "catalog verified" : model.capability_status === "failed" ? `unavailable · ${model.capability_error || "probe failed"}` : "unverified";
                return (
                  <button key={model.id} className={selectedID === model.id ? "is-selected" : ""} onClick={() => { setSelectedID(model.id); setModelInspectorOpen(true); }}>
                    <span><StatusDot state={probe?.state === "passed" ? "healthy" : !model.enabled || ready === 0 || probe?.state === "blocked" ? "disabled" : probe?.state === "failed" || model.capability_status === "failed" ? "exhausted" : "healthy"} /><code>{model.public_alias}</code><small title={probe?.state === "passed" ? undefined : probe?.error || model.capability_error}>{model.upstream_model === model.public_alias ? (model.supports_responses ? "Chat + Responses" : "Chat Completions") : model.upstream_model} · {capabilityLabel}</small></span>
                    <span>{routingMode === "model" && poolSizes[model.public_alias] > 1 ? `${model.provider.name} · pool of ${poolSizes[model.public_alias]}` : model.provider.name}</span>
                    <span>{ready}/{model.credentials.length}</span>
                    <ChevronRight size={13} />
                  </button>
                );
              })}
            </div>
          </section>
          {selected && (
            <aside className={`ide-resource-inspector${modelInspectorOpen ? " is-open" : ""}`}>
              <header className="ide-inspector-titlebar">
                <div><span>Model route</span><h2>{selected.public_alias}</h2><code>{selected.upstream_model}</code></div>
                <div className="button-row"><button className="console-icon resource-inspector-close" onClick={() => setModelInspectorOpen(false)} aria-label="Close model inspector"><X size={15} /></button><Button variant="quiet" onClick={() => {
                  void api(`/api/admin/models/${selected.id}/probe`, { method: "POST" })
                    .then(() => { notify("Model capability probe passed."); void load(); })
                    .catch((caught) => { notify(errorMessage(caught), "danger"); void load(); });
                }}><Activity size={13} /> Recheck model</Button><Button variant="danger" onClick={() => {
                  if (confirm(`Delete route ${selected.public_alias}? Requests using this alias will stop immediately.`)) {
                    void api(`/api/admin/models/${selected.id}`, { method: "DELETE" })
                      .then(() => { notify("Model route deleted."); void load(); })
                      .catch((caught) => notify(errorMessage(caught), "danger"));
                  }
                }}><Trash2 size={13} /> Delete</Button></div>
              </header>
              <div className="inspector-definition">
                <Definition label="Provider" value={selected.provider.name} />
                {routingMode === "model" && poolSizes[selected.public_alias] > 1 && <Definition label="Pooled with" value={`${poolSizes[selected.public_alias] - 1} other provider route${poolSizes[selected.public_alias] === 2 ? "" : "s"}`} />}
                <Definition label="Route state" value={selected.enabled ? "enabled" : "disabled"} />
                <Definition label="Capability check" value={healthyKeyCount(selected) === 0 ? "waiting for a healthy API key" : selected.capability_status === "probe_verified" ? "probe verified" : selected.capability_status === "catalog_verified" ? "catalog verified" : selected.capability_status === "failed" ? "unavailable" : selected.capability_status || "unverified"} />
                <Definition label="Chat endpoint" value={selected.capability_profile?.chat || (selected.supports_chat ? "native" : "off")} />
                <Definition label="Responses" value={selected.capability_profile?.responses || (selected.supports_responses ? "native" : "translated")} />
                <Definition label="Messages" value={selected.capability_profile?.messages || (selected.supports_messages ? "enabled" : "off")} />
                <Definition label="Streaming" value={selected.capability_profile?.streaming || "unknown"} />
                <Definition label="Tools" value={selected.capability_profile?.tools || "unknown"} />
                <Definition label="Thinking" value={selected.capability_profile?.thinking || "unknown"} />
                <Definition label="Output ceiling" value={`${selected.default_max_output_tokens} tokens`} />
                <Definition label="Tokenizer" value={selected.tokenizer} mono />
              </div>
              {selected.strip_parameters.length > 0 && <InlineNotice>Removes unsupported fields: <code>{selected.strip_parameters.join(", ")}</code></InlineNotice>}
              <ModelLimitEditor model={selected} credentials={selected.credentials} notify={notify} onSaved={load} />
              <section className={`ide-inspector-section inspector-disclosure${credentialsOpen ? " is-open" : ""}`}>
                <button type="button" onClick={() => setCredentialsOpen((current) => !current)} aria-expanded={credentialsOpen}>
                  <ChevronDown size={14} /><span><strong>Credential path</strong><small>{selected.credentials.length} API key{selected.credentials.length === 1 ? "" : "s"}</small></span>
                </button>
                {credentialsOpen && <div className="inspector-disclosure__body">{selected.credentials.map((credential) => (
                  <div key={credential.id} className={credential.validation_error ? "has-warning" : ""}>
                    <StatusDot state={credential.status} />
                    <strong>{credential.label}</strong>
                    <small>{credential.is_primary ? "primary" : credential.status}</small>
                    <span>
                      <small>{credential.model_limits[selected.id] ? "model override" : "shared provider limit"}</small>
                      <LimitSummary policy={credential.model_limits[selected.id] || credential.limits} />
                    </span>
                  </div>
                ))}</div>}
              </section>
              <div className="inspector-actions">
                <Button variant="quiet" onClick={() => navigate("providers", { provider: selected.provider.id })}>Manage model limits</Button>
                <Button variant="quiet" onClick={() => navigate("logs", { q: selected.public_alias })}>View request logs</Button>
              </div>
            </aside>
          )}
        </div>
      )}
    </div>
  );
}

function ModelLimitEditor({
  model,
  credentials,
  notify,
  onSaved
}: {
  model: ModelRoute;
  credentials: Credential[];
  notify: (message: string, tone?: "success" | "danger") => void;
  onSaved: () => Promise<void>;
}) {
  const [target, setTarget] = useState("all");
  const [draft, setDraft] = useState<RatePolicy>(emptyPolicy());
  const [busy, setBusy] = useState(false);
  const targetCredential = credentials.find((credential) => credential.id === target);
  const hasDraftLimit = Object.values(draft).some((value) => value !== null);
  useEffect(() => {
    setTarget("all");
    setDraft(emptyPolicy());
  }, [model.id]);
  useEffect(() => {
    if (target === "all") setDraft(emptyPolicy());
    else setDraft(targetCredential?.model_limits[model.id] ?? emptyPolicy());
  }, [target, targetCredential, model.id]);
  const targets = target === "all" ? credentials : targetCredential ? [targetCredential] : [];
  const save = async () => {
    if (targets.length === 0) return;
    setBusy(true);
    try {
      await Promise.all(targets.map((credential) => api(`/api/admin/credentials/${credential.id}/model-limits/${model.id}`, { method: "PUT", json: draft })));
      notify(`Model limit saved for ${targets.length} API key${targets.length === 1 ? "" : "s"}.`);
      await onSaved();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    if (targets.length === 0) return;
    setBusy(true);
    try {
      await Promise.all(targets.map((credential) => api(`/api/admin/credentials/${credential.id}/model-limits/${model.id}`, { method: "DELETE" })));
      setDraft(emptyPolicy());
      notify(`Shared provider limits restored for ${targets.length} API key${targets.length === 1 ? "" : "s"}.`);
      await onSaved();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="model-limit-editor">
      <header><div><span>Model override</span><strong>Apply limits once</strong></div><code>{target === "all" ? `${credentials.length} keys` : targetCredential?.label}</code></header>
      <label className="field"><span>Apply to</span><select value={target} onChange={(event) => setTarget(event.target.value)}><option value="all">All API keys</option>{credentials.map((credential) => <option key={credential.id} value={credential.id}>{credential.label}</option>)}</select></label>
      {target === "all" && <p>Saving replaces this model's override on every API key. Shared provider limits still apply.</p>}
      <RateFields value={draft} onChange={setDraft} compact />
      <div className="button-row"><Button disabled={busy || targets.length === 0 || !hasDraftLimit} onClick={() => void save()}>{busy ? "Saving…" : "Save model limit"}</Button><Button variant="quiet" disabled={busy || targets.length === 0} onClick={() => void clear()}>Use shared only</Button></div>
    </section>
  );
}

function LogsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const initialParams = useMemo(() => new URLSearchParams(location.search), []);
  const [query, setQuery] = useState(() => initialParams.get("q") || "");
  const [status, setStatus] = useState(() => initialParams.get("status") || "");
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<RequestLog | null>(null);
  const [bodies, setBodies] = useState<{ request: string | null; response: string | null } | null>(null);
  const [attemptsOpen, setAttemptsOpen] = useState(false);
  const [bodiesOpen, setBodiesOpen] = useState(false);
  const [logInspectorOpen, setLogInspectorOpen] = useState(false);
  const load = useCallback(async (silent = false) => {
    if (!silent) setLoading(true);
    try {
      const result = await api<{ logs: RequestLog[] }>(`/api/admin/logs?limit=100&q=${encodeURIComponent(query)}&status=${encodeURIComponent(status)}`);
      const normalized = result.logs.map((log) => ({
        ...log,
        attempts: log.attempts ?? [],
        routing_decisions: log.routing_decisions ?? [],
      }));
      setLogs(normalized);
      setSelected((current) => normalized.find((log) => log.id === current?.id) || normalized.find((log) => log.request_id === current?.request_id) || null);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      if (!silent) setLoading(false);
    }
  }, [query, status, notify]);
  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(true), 2000);
    return () => window.clearInterval(timer);
  }, [load]);
  useEffect(() => {
    const params = new URLSearchParams();
    if (query) params.set("q", query);
    if (status) params.set("status", status);
    history.replaceState({}, "", `/admin/logs${params.size ? `?${params}` : ""}`);
  }, [query, status]);
  useEffect(() => {
    setBodies(null);
    setAttemptsOpen(Boolean(selected && selected.status_code >= 400 && selected.attempts.length));
    setBodiesOpen(false);
  }, [selected?.id]);
  useEffect(() => {
    if (selected?.body_captured && bodiesOpen) {
      void api<{ request: string | null; response: string | null }>(`/api/admin/logs/${selected.id}/bodies`).then(setBodies).catch((caught) => notify(errorMessage(caught), "danger"));
    }
  }, [selected, bodiesOpen, notify]);

  return (
    <div className="resource-page log-page">
      <PageHeader eyebrow="Live request evidence" title="Logs" description="Running requests update every two seconds. Completed metadata is retained; bodies appear only where encrypted capture is enabled." actions={<Button variant="quiet" onClick={() => void load()}><RefreshCw size={15} /> Refresh</Button>} />
      <div className="ide-resource-workbench log-workbench">
        <section className="ide-resource-list" aria-label="Request logs">
          <div className="ide-filter log-filter">
            <Search size={14} />
            <label>
              <span className="sr-only">Search logs</span>
              <input aria-label="Search logs" placeholder="Request ID or model alias" value={query} onChange={(e) => setQuery(e.target.value)} />
            </label>
            <label>
              <span className="sr-only">Status</span>
              <select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)}>
                <option value="">All statuses</option><option value="running">Running</option><option value="errors">All errors</option><option value="200">200</option><option value="400">400</option><option value="401">401</option><option value="403">403</option><option value="404">404</option><option value="429">429</option><option value="500">500</option><option value="502">502</option><option value="503">503</option><option value="504">504</option>
              </select>
            </label>
          </div>
          <header><span>Request</span><span>Model / route</span><span>Status</span><span>Latency</span><span /></header>
          <div className="log-resource-rows">
            {loading ? (
              <PageSkeleton />
            ) : logs.length === 0 ? (
              <EmptyState title="No matching requests" description="Send a request through /v1 or clear the current filters." />
            ) : logs.map((log) => (
              <button key={log.id} className={`${selected?.request_id === log.request_id ? "is-selected" : ""}${log.running ? " is-running" : ""}`} onClick={() => { setSelected(log); setLogInspectorOpen(true); }}>
                <span>
                  <StatusDot state={log.running ? "unknown" : log.status_code >= 500 ? "exhausted" : log.status_code >= 400 ? "partial" : "healthy"} />
                  <strong>{new Date(log.created_at).toLocaleTimeString()}</strong>
                  <small>{log.request_id}</small>
                </span>
                <span><code>{log.model_alias}</code><small>{log.provider_name} · {routingStageLabel(log)}{log.error_code ? ` · ${log.error_code}` : ""}</small></span>
                <strong className={`status-code status-code--${log.running ? "running" : log.status_code >= 500 ? "fault" : log.status_code >= 400 ? "warning" : "healthy"}`}>{log.running ? "RUN" : log.status_code}</strong>
                <small>{log.latency_ms} ms</small>
                <ChevronRight size={13} />
              </button>
            ))}
          </div>
        </section>
        {selected ? (
          <aside className={`ide-resource-inspector log-resource-inspector${logInspectorOpen ? " is-open" : ""}`}>
            <header className="ide-inspector-titlebar">
              <div><span>{selected.endpoint} · {selected.running ? "RUNNING" : `HTTP ${selected.status_code}`}</span><h2>{selected.request_id}</h2><code>{selected.model_alias}</code></div>
              <div className="button-row"><strong className={`status-code status-code--${selected.running ? "running" : selected.status_code >= 500 ? "fault" : selected.status_code >= 400 ? "warning" : "healthy"}`}>{selected.running ? "RUNNING" : selected.status_code}</strong><button className="console-icon resource-inspector-close" onClick={() => setLogInspectorOpen(false)} aria-label="Close log inspector"><X size={15} /></button></div>
            </header>
            <div className="log-detail-grid">
              <div><span>Provider</span><strong>{selected.provider_name}</strong></div>
              <div><span>API key</span><strong>{routingStageLabel(selected)}</strong></div>
              <div><span>Latency</span><strong>{selected.latency_ms} ms</strong></div>
              <div><span>Tokens</span><strong title={`${selected.input_tokens} input · ${selected.output_tokens} output`}>{formatCompact(selected.input_tokens)} in · {formatCompact(selected.output_tokens)} out</strong></div>
            </div>
            {selected.running ? <InlineNotice>Request is running. This inspector will switch to the final status automatically.</InlineNotice> : selected.status_code >= 400 && <LogDiagnosis log={selected} />}
            <section className={`inspector-disclosure log-disclosure${attemptsOpen ? " is-open" : ""}`}>
              <button type="button" onClick={() => setAttemptsOpen((current) => !current)} aria-expanded={attemptsOpen}><ChevronDown size={14} /><span><strong>Routing attempts</strong><small>{selected.attempts.length} recorded</small></span></button>
              {attemptsOpen && <div className="attempt-list">{selected.attempts.length ? selected.attempts.map((attempt, index) => <div key={`${attempt.credential_id}-${index}`}><span>{index + 1}</span><strong>{attempt.credential_label}</strong><code>{attempt.status_code || attempt.error}{attempt.error ? ` · ${attempt.error}` : ""}{attempt.removed_parameters?.length ? ` · removed ${attempt.removed_parameters.join(", ")}` : ""}{attempt.replaced_parameters ? ` · replaced ${Object.entries(attempt.replaced_parameters).map(([from, to]) => `${from} → ${to}`).join(", ")}` : ""}</code><small>{attempt.duration_ms} ms</small>{attempt.error_message && <p>{attempt.error_message}</p>}</div>) : <p className="console-empty">No upstream attempt was needed.</p>}</div>}
            </section>
            {!selected.running && selected.body_captured ? (
              <section className={`inspector-disclosure log-disclosure${bodiesOpen ? " is-open" : ""}`}>
                <button type="button" onClick={() => setBodiesOpen((current) => !current)} aria-expanded={bodiesOpen}><ChevronDown size={14} /><span><strong>Encrypted bodies</strong><small>{selected.body_truncated ? "captured · truncated" : "captured"}</small></span></button>
                {bodiesOpen && <div className="body-grid"><section><h3>Request {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies?.request ?? "Loading encrypted body…"}</pre></section><section><h3>Response {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies?.response ?? "No captured response body."}</pre></section></div>}
              </section>
            ) : !selected.running && <InlineNotice>Body capture was off for this model route. Only metadata is available.</InlineNotice>}
          </aside>
        ) : (
          <aside className="ide-resource-inspector ide-resource-inspector--empty">
            <FileClock size={20} /><strong>Select a request</strong><p>Inspect routing attempts, token usage, latency and optional encrypted bodies.</p>
          </aside>
        )}
      </div>
    </div>
  );
}

function LogDiagnosis({ log }: { log: RequestLog }) {
  const decisions = log.routing_decisions ?? [];
  const failedAttempts = (log.attempts ?? []).filter((attempt) => attempt.error || attempt.status_code >= 400);
  return (
    <section className="log-diagnosis" aria-label="Automatic failure diagnosis">
      <header>
        <AlertTriangle size={15} />
        <span><small>Automatic diagnosis</small><strong>{logErrorTitle(log)}</strong></span>
        <code>{log.error_code || `http_${log.status_code}`}</code>
      </header>
      <div className="log-diagnosis__rows">
        {decisions.map((decision, index) => (
          <div key={`${decision.credential_id || "route"}-${decision.dimension || decision.reason}-${index}`}>
            <span className="log-diagnosis__marker">{index + 1}</span>
            <span>
              <strong>{decision.credential_label || "Routing pool"}</strong>
              <p>{routingDecisionMessage(decision)}</p>
            </span>
            <small>{decision.reset_at ? <>reset <LiveResetTime value={decision.reset_at} /></> : decision.scope || "routing"}</small>
          </div>
        ))}
        {failedAttempts.map((attempt, index) => (
          <div key={`attempt-${attempt.credential_id}-${index}`}>
            <span className="log-diagnosis__marker">{decisions.length + index + 1}</span>
            <span>
              <strong>{attempt.credential_label || "Upstream"} · {attempt.status_code ? `HTTP ${attempt.status_code}` : attempt.error}</strong>
              <p>{attempt.error_message || humanizeErrorCode(attempt.error || log.error_code || "upstream_error")}</p>
            </span>
            <small>{attempt.retryable ? "retryable" : "final"} · {attempt.duration_ms} ms</small>
          </div>
        ))}
        {decisions.length === 0 && failedAttempts.length === 0 && (
          <div>
            <span className="log-diagnosis__marker">!</span>
            <span><strong>Gateway decision</strong><p>{log.error_message || humanizeErrorCode(log.error_code || `http_${log.status_code}`)}</p></span>
            <small>metadata</small>
          </div>
        )}
      </div>
    </section>
  );
}

function routingDecisionMessage(decision: RequestLog["routing_decisions"][number]) {
  const scope = decision.scope === "model" ? "Model" : "Shared";
  const dimension = decision.dimension?.toUpperCase();
  if (decision.reason === "cooldown") {
    return `Key is cooling down${decision.retry_after_ms ? ` for ${formatDuration(decision.retry_after_ms)}` : ""}.`;
  }
  if (decision.reason === "tpr_exceeded") {
    return `${scope} TPR allows ${formatCompact(decision.limit || 0)} tokens, but this request reserved ${formatCompact(decision.required || 0)}.`;
  }
  if (decision.reason === "limit_exhausted") {
    return `${scope} ${dimension}: ${formatCompact(decision.used || 0)} used + ${formatCompact(decision.required || 0)} required exceeds ${formatCompact(decision.limit || 0)}; ${formatCompact(decision.remaining || 0)} remained.`;
  }
  if (decision.reason === "quarantined") return "The provider rejected this API key; re-check or replace it.";
  if (decision.reason === "disabled") return "This API key is disabled and cannot receive traffic.";
  return humanizeErrorCode(decision.reason);
}

function logErrorTitle(log: RequestLog) {
  if (log.routing_decisions?.length) return "No API key passed every routing constraint";
  if (log.attempts?.some((attempt) => attempt.status_code >= 400 || attempt.error)) return "Upstream request failed";
  return "Gateway request failed";
}

function routingStageLabel(log: RequestLog) {
  if (log.credential_label) return log.credential_label;
  if (log.provider_name === "gateway") return "Gateway validation";
  if (log.attempts?.length) return "No final API key";
  return "Pre-routing rejection";
}

function humanizeErrorCode(code: string) {
  const messages: Record<string, string> = {
    rate_limit_exceeded: "Every configured API key was at capacity, in cooldown, or blocked by a request/token limit.",
    no_credentials: "No enabled healthy API key is configured for this model.",
    limiter_unavailable: "Redis was unavailable, so Rotakey failed closed instead of bypassing limits.",
    upstream_unavailable: "The upstream connection failed before a response started.",
    connection_error: "The upstream connection failed before a response started.",
    upstream_response_too_large: "The upstream response exceeded Rotakey's configured response size limit.",
    translation_failed: "The upstream response could not be translated to the requested API format.",
    stream_interrupted: "The response stream ended unexpectedly after it started.",
    unsupported_parameter: "The upstream model rejected a request parameter.",
    unrecognized_request_argument: "The upstream model did not recognize a request parameter.",
  };
  return messages[code] || code.replaceAll("_", " ");
}

function formatDuration(milliseconds: number) {
  if (milliseconds < 60_000) return `${Math.max(1, Math.ceil(milliseconds / 1_000))}s`;
  if (milliseconds < 3_600_000) return `${Math.ceil(milliseconds / 60_000)}m`;
  return `${Math.ceil(milliseconds / 3_600_000)}h`;
}

function AccessPage({ gatewayKey, onNewKey, notify }: { gatewayKey: string; onNewKey: (key: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [codexReady, setCodexReady] = useState(0);
  useEffect(() => { void api<Settings>("/api/admin/settings").then(setSettings).catch((caught) => notify(errorMessage(caught), "danger")); }, [notify, gatewayKey]);
  useEffect(() => { void api<Overview>("/api/admin/overview?range=1h").then((value) => setCodexReady(value.summary.routes_ready)).catch(() => undefined); }, []);
  const rotateKey = () => {
    if (!confirm("Rotate the gateway key now? The current key will stop working immediately.")) return;
    void api<{ gateway_key: string }>("/api/admin/access/rotate", { method: "POST" })
      .then((result) => { onNewKey(result.gateway_key); notify("Gateway key rotated."); })
      .catch((caught) => notify(errorMessage(caught), "danger"));
  };
  const rootURL = (settings?.base_url || location.origin).replace(/\/$/, "");
  const openAIURL = `${rootURL}/v1`;
  return (
    <>
      <PageHeader eyebrow="Unified authentication" title="Access key" description="One active key works with OpenAI SDKs, Anthropic SDKs and Claude Code. Upstream provider secrets never leave Rotakey." />
      <section className="access-key-panel">
        <div className="key-prefix"><ShieldCheck size={20} /><span><small>Active key prefix</small><code>{settings?.gateway_key_prefix ? `${settings.gateway_key_prefix}••••••••••••` : "Loading…"}</code></span></div>
        <div><strong>Immediate rotation</strong><p>Rotating revokes the current key in the same database transaction. Update every application immediately.</p></div>
      </section>
      <section className="section-block code-example">
        <div className="section-heading"><div><p className="eyebrow">OpenAI lane</p><h2>Chat Completions and Responses</h2></div><Button variant="quiet" onClick={() => void copyText(openAIURL).then(() => notify("OpenAI base URL copied."))}><Clipboard size={14} /> Copy base URL</Button></div>
        <pre>{`curl "${openAIURL}/chat/completions" \\
  -H "Authorization: Bearer $ROTAKEY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"provider/model-alias","messages":[{"role":"user","content":"Hello"}]}'`}</pre>
      </section>
      <section className="section-block code-example">
        <div className="section-heading">
          <div><p className="eyebrow">Codex CLI &amp; Desktop</p><h2>{codexReady} model route{codexReady === 1 ? "" : "s"} ready</h2></div>
          <div className="button-row">
            <a className="button button--quiet" href="https://github.com/jisunahamed/rotakey/blob/main/docs/CODEX.md" target="_blank" rel="noreferrer"><BookOpen size={14} /> Full guide</a>
            <Button variant="quiet" onClick={() => void copyText(`rotakey-codex install --url ${rootURL}`).then(() => notify("Codex setup command copied."))}><Clipboard size={14} /> Copy setup</Button>
          </div>
        </div>
        <pre>{`rotakey-codex install --url ${rootURL}
rotakey-codex doctor
codex --profile rotakey`}</pre>
        <p>The installer prompts for the gateway key, protects it in the OS credential store, and writes only a managed Rotakey profile. Run <code>rotakey-codex sync</code> after changing model routes.</p>
      </section>
      <section className="section-block code-example">
        <div className="section-heading"><div><p className="eyebrow">Anthropic lane</p><h2>Messages SDK and Claude Code</h2></div><div className="button-row"><a className="button button--quiet" href="https://github.com/jisunahamed/rotakey/blob/main/docs/CLAUDE-CODE.md" target="_blank" rel="noreferrer"><BookOpen size={14} /> Full guide</a><Button variant="quiet" onClick={() => void copyText(rootURL).then(() => notify("Anthropic base URL copied."))}><Clipboard size={14} /> Copy base URL</Button></div></div>
        <pre>{`export ANTHROPIC_BASE_URL="${rootURL}"
export ANTHROPIC_API_KEY="$ROTAKEY_KEY"

curl "${rootURL}/v1/messages" \\
  -H "x-api-key: $ROTAKEY_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "content-type: application/json" \\
  -d '{"model":"provider/model-alias","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'`}</pre>
      </section>
      <div className="page-actions console-actionbar">
        <span>Rotation immediately revokes the current client credential.</span>
        <Button variant="danger" onClick={rotateKey}><RefreshCw size={15} /> Rotate key</Button>
      </div>
    </>
  );
}

function SettingsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    void Promise.all([api<Settings>("/api/admin/settings"), api<{ providers: Provider[] }>("/api/admin/providers")])
      .then(([nextSettings, result]) => {
        setSettings(nextSettings);
        setProviders(normalizeProviders(result.providers));
      })
      .catch((caught) => notify(errorMessage(caught), "danger"));
  }, [notify]);
  if (!settings) return <PageSkeleton />;
  return (
    <>
      <PageHeader eyebrow="Control plane policy" title="System" description="Bound waiting and retention so the gateway stays predictable on a small VPS." />
      <section className="settings-list">
        <label className="settings-row"><span><strong>Routing mode</strong><small>{settings.routing_mode === "model" ? "Model-wise: one alias pools every provider publishing that name, rotating across providers and keys. Saving strips the provider prefix from aliases that carry one." : "Provider-wise: each alias belongs to one provider. Saving prepends the provider slug to aliases that lack one."}</small></span><div><select value={settings.routing_mode} onChange={(event) => setSettings({ ...settings, routing_mode: event.target.value as RoutingMode })}><option value="provider">Provider-wise</option><option value="model">Model-wise (pooled)</option></select></div></label>
        <label className="settings-row"><span><strong>Default Anthropic resource provider</strong><small>Files have no model field, so uploads need one native Anthropic provider. Batches remain model-routed.</small></span><div><select value={settings.default_anthropic_provider_id || ""} onChange={(event) => setSettings({ ...settings, default_anthropic_provider_id: event.target.value })}><option value="">Not configured</option>{providers.filter((provider) => provider.api_format === "anthropic" && provider.enabled).map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></div></label>
        <label className="settings-row"><span><strong>Capacity wait ceiling</strong><small>Requests wait only when capacity can return within this deadline.</small></span><div><input type="number" min={0} max={30000} step={100} value={settings.max_wait_ms} onChange={(e) => setSettings({ ...settings, max_wait_ms: Number(e.target.value) })} /><code>ms</code></div></label>
        <label className="settings-row"><span><strong>Global provider timeout</strong><small>Applies this request timeout to every provider and becomes the default for new providers.</small></span><div><input type="number" min={1} max={900} value={settings.default_provider_timeout_seconds} onChange={(e) => setSettings({ ...settings, default_provider_timeout_seconds: Number(e.target.value) })} /><code>seconds</code></div></label>
        <label className="settings-row"><span><strong>Metadata retention</strong><small>Request IDs, routing attempts, status, latency and usage.</small></span><div><input type="number" min={1} max={3650} value={settings.metadata_retention_days} onChange={(e) => setSettings({ ...settings, metadata_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
        <label className="settings-row"><span><strong>Captured body retention</strong><small>Only applies to model routes where encrypted body capture is enabled.</small></span><div><input type="number" min={1} max={365} value={settings.body_retention_days} onChange={(e) => setSettings({ ...settings, body_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
      </section>
      <div className="page-actions console-actionbar">
        <span>Changes apply to new requests without restarting the gateway.</span>
        <Button disabled={busy} onClick={() => {
          setBusy(true);
          void api<SettingsUpdateResult>("/api/admin/settings", { method: "PUT", json: settings })
            .then((result) => {
              routingModeCache = result.routing_mode;
              // A mode switch renames aliases in the same transaction, so the
              // save confirmation reports what changed and what could not.
              const parts = ["System settings saved."];
              if (result.aliases_rewritten > 0) parts.push(`${result.aliases_rewritten} model alias${result.aliases_rewritten === 1 ? "" : "es"} renamed for ${result.routing_mode}-wise routing.`);
              if (result.alias_conflicts.length > 0) parts.push(`Kept unchanged to avoid a collision: ${result.alias_conflicts.join(", ")}.`);
              notify(parts.join(" "), result.alias_conflicts.length > 0 ? "danger" : "success");
            })
            .catch((caught) => notify(errorMessage(caught), "danger"))
            .finally(() => setBusy(false));
        }}>{busy ? "Saving…" : "Save settings"}</Button>
      </div>
      <ConfigTransfer notify={notify} />
      <section className="security-baseline">
        <ShieldCheck size={19} />
        <div><strong>Security baseline active</strong><p>Encrypted provider secrets · HttpOnly sessions · CSRF checks · private-network SSRF blocking · strict browser policy</p></div>
      </section>
    </>
  );
}

// ConfigTransfer writes the whole provider, model, key and limit setup to one
// file and replays it. Keys are included by default so an import is a complete
// setup rather than a shell that still needs every secret pasted back in.
function ConfigTransfer({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const [busy, setBusy] = useState<"export" | "import" | null>(null);
  const [includeSecrets, setIncludeSecrets] = useState(true);
  const [result, setResult] = useState<ImportResult | null>(null);

  const exportConfig = async () => {
    setBusy("export");
    try {
      // Export is a POST so the CSRF check applies; a bundle with plaintext keys
      // must not be reachable by a cross-site GET on the session cookie.
      const bundle = await api<Record<string, unknown>>(`/api/admin/config/export?secrets=${includeSecrets}`, { method: "POST" });
      const stamp = new Date().toISOString().replace(/[-:T]/g, "").slice(0, 15);
      const url = URL.createObjectURL(new Blob([JSON.stringify(bundle, null, 2)], { type: "application/json" }));
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `rotakey-config-${stamp}.json`;
      document.body.appendChild(anchor);
      anchor.click();
      anchor.remove();
      URL.revokeObjectURL(url);
      notify(includeSecrets ? "Configuration exported with API keys. Store the file like a password." : "Configuration exported without API keys.");
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(null);
    }
  };

  const importConfig = async (file: File) => {
    setBusy("import");
    setResult(null);
    try {
      const bundle = JSON.parse(await file.text());
      const imported = await api<ImportResult>("/api/admin/config/import", { method: "POST", json: bundle });
      setResult(imported);
      const created = imported.providers_created + imported.models_created + imported.credentials_created;
      notify(`Setup restored: ${imported.providers_created + imported.providers_updated} provider${imported.providers_created + imported.providers_updated === 1 ? "" : "s"}, ${imported.models_created + imported.models_updated} model route${imported.models_created + imported.models_updated === 1 ? "" : "s"}, ${imported.credentials_created + imported.credentials_updated} API key${imported.credentials_created + imported.credentials_updated === 1 ? "" : "s"}${created === 0 ? " (all already present)" : ""}.`, imported.warnings.length > 0 ? "danger" : "success");
    } catch (caught) {
      notify(caught instanceof SyntaxError ? "That file is not valid JSON." : errorMessage(caught), "danger");
    } finally {
      setBusy(null);
    }
  };

  return (
    <section className="settings-list config-transfer">
      {/* These rows hold buttons rather than a single control, so they are plain
          divs: a wrapping label would steal clicks and nest interactive labels. */}
      <div className="settings-row">
        <span><strong>Export configuration</strong><small>Writes every provider, model route, API key and rate limit to one JSON file, sorted A-to-Z. Import it on another install to reproduce this setup.</small></span>
        <div>
          <label className="config-transfer__secrets"><input type="checkbox" checked={includeSecrets} onChange={(event) => setIncludeSecrets(event.target.checked)} /><span>Include API keys</span></label>
          <Button variant="quiet" disabled={busy !== null} onClick={() => void exportConfig()}><Download size={14} /> {busy === "export" ? "Exporting…" : "Export"}</Button>
        </div>
      </div>
      <div className="settings-row">
        <span><strong>Import configuration</strong><small>Replays a bundle in one transaction. Providers match on identifier and models on alias, so re-importing updates instead of duplicating.</small></span>
        <div>
          <input id="config-import-file" type="file" accept="application/json,.json" style={{ display: "none" }} onChange={(event) => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void importConfig(file); }} />
          <Button variant="quiet" disabled={busy !== null} onClick={() => document.getElementById("config-import-file")?.click()}><Upload size={14} /> {busy === "import" ? "Importing…" : "Import"}</Button>
        </div>
      </div>
      {result && (
        <div className="config-transfer__result">
          <strong>Import summary</strong>
          <ul>
            <li>Routing mode set to {result.routing_mode}-wise</li>
            <li>Providers: {result.providers_created} created · {result.providers_updated} updated</li>
            <li>Model routes: {result.models_created} created · {result.models_updated} updated</li>
            <li>API keys: {result.credentials_created} created · {result.credentials_updated} updated{result.credentials_skipped > 0 ? ` · ${result.credentials_skipped} skipped` : ""}</li>
          </ul>
          {result.credentials_unverified > 0 && <InlineNotice tone="danger">{result.credentials_unverified} imported API key{result.credentials_unverified === 1 ? " was" : "s were"} saved without contacting the provider. Use Test on each provider to confirm they work.</InlineNotice>}
          {result.warnings.map((warning) => <InlineNotice key={warning} tone="danger">{warning}</InlineNotice>)}
        </div>
      )}
    </section>
  );
}

function SecretReveal({ title, keyValue, message, onClose, notify }: { title: string; keyValue: string; message: string; onClose: () => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <Sheet title={title} eyebrow="One-time secret" onClose={() => { if (confirmed || confirm("Close without confirming that the key is saved?")) onClose(); }}>
      <InlineNotice tone="danger">{message}</InlineNotice>
      <div className="secret-value"><code>{keyValue}</code><Button variant="quiet" onClick={() => void copyText(keyValue).then(() => notify("Gateway key copied.")).catch(() => notify("Clipboard access was blocked.", "danger"))}><Clipboard size={14} /> Copy</Button></div>
      <label className="confirmation-check"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /><span>I stored this key securely.</span></label>
      <div className="sheet-actions"><span /><Button disabled={!confirmed} onClick={onClose}>Finish</Button></div>
    </Sheet>
  );
}

function PageSkeleton() {
  return <div className="page-skeleton" aria-label="Loading"><span /><span /><span /><span /></div>;
}

type CredentialDraft = {
  id: string;
  label: string;
  secret: string;
  is_primary: boolean;
};

let credentialDraftSequence = 0;

function newCredentialDraft(): CredentialDraft {
  credentialDraftSequence += 1;
  return {
    id: `credential-draft-${Date.now()}-${credentialDraftSequence}`,
    label: "",
    secret: "",
    is_primary: false,
  };
}

function CredentialEntries({ value, onChange }: { value: CredentialDraft[]; onChange: (value: CredentialDraft[]) => void }) {
  const update = (id: string, patch: Partial<CredentialDraft>) => {
    onChange(value.map((credential) => {
      if (patch.is_primary) {
        return credential.id === id ? { ...credential, ...patch } : { ...credential, is_primary: false };
      }
      return credential.id === id ? { ...credential, ...patch } : credential;
    }));
  };
  return (
    <div className="credential-entry-list">
      <div className="credential-entry-list__intro">
        <div><strong>API keys</strong><small>Add each key separately. Choosing a primary is optional.</small></div>
        <Button type="button" variant="quiet" onClick={() => onChange([...value, newCredentialDraft()])}><Plus size={14} /> Add another API key</Button>
      </div>
      {value.map((credential, index) => (
        <section className="credential-entry" key={credential.id}>
          <header>
            <strong>API key {index + 1}</strong>
            {value.length > 1 && <Button type="button" variant="quiet" onClick={() => onChange(value.filter((item) => item.id !== credential.id))}><Trash2 size={13} /> Remove</Button>}
          </header>
          <div className="field-pair">
            <label className="field"><span>Label</span><input placeholder="Production key" value={credential.label} onChange={(event) => update(credential.id, { label: event.target.value })} /></label>
            <label className="field"><span>API key</span><input type="password" autoComplete="off" placeholder="Paste provider API key" value={credential.secret} onChange={(event) => update(credential.id, { secret: event.target.value })} /></label>
          </div>
          <label className="primary-choice">
            <input type="checkbox" checked={credential.is_primary} onChange={(event) => update(credential.id, { is_primary: event.target.checked })} />
            <span><strong>Primary</strong><small>Try this key first while it has capacity.</small></span>
          </label>
        </section>
      ))}
    </div>
  );
}

function credentialInputs(value: CredentialDraft[], limits: RatePolicy, unverifiedLabels: string[] = []) {
  const unverified = new Set(unverifiedLabels);
  return value
    .filter((credential) => credential.label.trim() && credential.secret.trim())
    .map((credential) => ({
      label: credential.label.trim(),
      secret: credential.secret.trim(),
      is_primary: credential.is_primary,
      enabled: true,
      allow_unverified: unverified.has(credential.label.trim()),
      limits,
    }));
}

function mergeModelCatalogs(catalogs: DiscoveredModel[][]): DiscoveredModel[] {
  const byID = new Map<string, DiscoveredModel>();
  for (const catalog of catalogs) {
    for (const model of catalog ?? []) {
      if (!byID.has(model.id)) byID.set(model.id, model);
    }
  }
  return [...byID.values()].sort((left, right) => left.id.localeCompare(right.id));
}

function defaultPublicAlias(providerSlug: string, upstreamModel: string, mode: RoutingMode = "provider") {
  // Model-wise routing pools every provider publishing the same alias, so a new
  // route must not carry the provider slug that would keep them separate.
  const raw = mode === "model" || upstreamModel.startsWith(`${providerSlug}/`)
    ? upstreamModel
    : `${providerSlug}/${upstreamModel}`;
  const safe = raw.replace(/[^A-Za-z0-9._:/-]+/g, "-").replace(/^-+|-+$/g, "");
  return safe.slice(0, 128);
}

function providerSlugForUI(name: string) {
  const slug = name.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
  return slug.length >= 2 ? slug : "provider";
}

function routeInputsFromSelection(selected: Record<string, string>, catalogIDs = new Set<string>()): ModelDraft[] {
  return Object.entries(selected).map(([upstreamModel, publicAlias]) => ({
    public_alias: publicAlias.trim(),
    upstream_model: upstreamModel,
    manual: !catalogIDs.has(upstreamModel),
    supports_chat: true,
    supports_responses: false,
    supports_messages: true,
    default_max_output_tokens: 1024,
    input_cost_per_million_usd: 0,
    output_cost_per_million_usd: 0,
    request_cost_usd: undefined,
    tokenizer: "heuristic",
    capture_bodies: false,
    strip_parameters: [],
    enabled: true,
  }));
}

// poolSizeByAlias counts how many provider routes publish each public alias, so
// model-wise mode can show that one name is backed by several providers.
function poolSizeByAlias(providers: Provider[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const provider of providers) {
    for (const model of provider.models) {
      counts[model.public_alias] = (counts[model.public_alias] ?? 0) + 1;
    }
  }
  return counts;
}

function normalizeProviders(providers: Provider[] | null | undefined): Provider[] {
  return (providers ?? []).map((provider) => ({
    ...provider,
    api_format: provider.api_format ?? "openai",
    anthropic_version: provider.anthropic_version ?? "2023-06-01",
    extra_headers: provider.extra_headers ?? {},
    models: (provider.models ?? []).map((model) => ({ ...model, supports_messages: model.supports_messages ?? true, strip_parameters: model.strip_parameters ?? [], capability_status: model.capability_status ?? "unverified", capability_profile: model.capability_profile ?? {}, input_cost_per_million_usd: model.input_cost_per_million_usd ?? 0, output_cost_per_million_usd: model.output_cost_per_million_usd ?? 0, request_cost_usd: model.request_cost_usd })),
    credentials: (provider.credentials ?? []).map((credential) => ({
      ...credential,
      validation_error: credential.validation_error ?? "",
    })),
  }));
}

function formatNumber(value: number) {
  return new Intl.NumberFormat("en", { notation: value > 9999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(value);
}

function formatCompact(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(value);
}

function formatUSD(value: number) {
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: value < 1 ? 4 : 2 }).format(value || 0);
}

function formatLatency(value: number) {
  return value >= 1000 ? `${(value / 1000).toFixed(value >= 10_000 ? 0 : 1)}s` : `${Math.round(value)}ms`;
}

function formatChartDate(value?: string) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("en", { month: "short", day: "numeric" }).format(new Date(value));
}

async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // HTTP/IP deployments often block the modern Clipboard API. Fall through.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.readOnly = true;
  textarea.style.position = "fixed";
  textarea.style.inset = "0 auto auto -9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, value.length);
  try {
    if (!document.execCommand("copy")) throw new Error("Clipboard copy failed");
  } finally {
    textarea.remove();
  }
}

function errorMessage(caught: unknown) {
  if (caught instanceof APIError || caught instanceof Error) return caught.message;
  return "The operation could not be completed.";
}

export default App;
