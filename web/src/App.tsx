import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
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
  FlaskConical,
  Github,
  KeyRound,
  LogOut,
  Menu,
  Moon,
  Plus,
  Power,
  RefreshCw,
  Route,
  Search,
  Send,
  Settings as SettingsIcon,
  ShieldCheck,
  Sun,
  Trash2,
  Upload,
  Square,
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
  statusLabel,
  Toggle,
  useDrawerOverlay
} from "./components";
import { useConfirm, type ConfirmRequest } from "./ConfirmDialog";
import {
  emptyCreditTotals,
  emptyPolicy,
  type Credential,
  type CreditTotals,
  type DiscoveredModel,
  type ImportResult,
  type ModelRoute,
  type Overview,
  type Provider,
  type ProviderStateResult,
  type RatePolicy,
  type RequestLog,
  type RoutingMode,
  type Settings,
  type SettingsUpdateResult
} from "./types";

type Page = "overview" | "providers" | "models" | "playground" | "logs" | "access" | "settings";
type AuthPhase = "loading" | "setup" | "login" | "app";

const navItems: Array<{ id: Page; label: string; icon: typeof Activity }> = [
  { id: "overview", label: "Overview", icon: CircleGauge },
  { id: "providers", label: "Providers", icon: Database },
  { id: "models", label: "Model routes", icon: Route },
  { id: "playground", label: "Playground", icon: FlaskConical },
  { id: "logs", label: "Request logs", icon: FileClock },
  { id: "access", label: "Gateway key", icon: KeyRound },
  { id: "settings", label: "Settings", icon: SettingsIcon }
];

const pageFromLocation = (): Page => {
  const segment = location.pathname.replace(/^\/admin\/?/, "").split("/")[0] as Page;
  return navItems.some((item) => item.id === segment) ? segment : "overview";
};

// The routing mode decides whether a new public alias carries the provider slug,
// so it is fetched once and shared by every form that proposes an alias. The
// subscriber set is what makes it shared rather than merely cached: when Settings
// saves a new mode, every mounted form re-labels itself instead of waiting for a
// remount, and concurrent mounts await one request instead of each firing their own.
let routingModeCache: RoutingMode | null = null;
let routingModeRequest: Promise<RoutingMode> | null = null;
const routingModeSubscribers = new Set<(mode: RoutingMode) => void>();

export function publishRoutingMode(mode: RoutingMode) {
  routingModeCache = mode;
  routingModeSubscribers.forEach((notifySubscriber) => notifySubscriber(mode));
}

function useRoutingMode(): RoutingMode {
  const [mode, setMode] = useState<RoutingMode>(routingModeCache ?? "provider");
  useEffect(() => {
    routingModeSubscribers.add(setMode);
    if (routingModeCache) {
      setMode(routingModeCache);
    } else {
      routingModeRequest ??= api<Settings>("/api/admin/settings")
        .then((settings) => (settings.routing_mode === "model" ? "model" : "provider"))
        .finally(() => { routingModeRequest = null; });
      void routingModeRequest.then(publishRoutingMode).catch(() => undefined);
    }
    return () => { routingModeSubscribers.delete(setMode); };
  }, []);
  return mode;
}

/** Reads a media query in JS so behaviour (focus order, inert) can follow the
 *  same breakpoint the stylesheets use, rather than a second guess at it. */
function useMediaQuery(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const list = window.matchMedia(query);
    const onChange = () => setMatches(list.matches);
    onChange();
    list.addEventListener("change", onChange);
    return () => list.removeEventListener("change", onChange);
  }, [query]);
  return matches;
}

type Note = { id: number; tone: "success" | "danger"; message: string };

/** Past this many the stack is taller than the corner it lives in, and the oldest
 *  message has been on screen long enough to have been read. Errors are exempt from
 *  the timer but not from this — the newest four are the ones that matter. */
const toastLimit = 4;

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
  const [toasts, setToasts] = useState<Note[]>([]);
  // One timer per message, keyed by id. A timer that outlives its message — because
  // the operator dismissed it, or it was pushed off the end of the stack — fires
  // against an id that is no longer there and does nothing.
  const toastTimers = useRef(new Map<number, number>());
  const toastID = useRef(0);

  // Notifications stack instead of replacing each other. One slot meant a run of
  // messages from a single action — importing a config reports a warning per
  // skipped row — showed only whichever arrived last, and the operator had no way
  // to know the others existed. Failures stay until dismissed, because an error
  // nobody read is an error nobody fixed; everything else leaves on its own, and
  // longer text gets proportionally longer to read it in.
  const notify = useCallback((message: string, tone: "success" | "danger" = "success") => {
    const id = ++toastID.current;
    setToasts((current) => {
      // The same sentence twice is one thing happening twice, not two things. It
      // moves to the front of the queue and re-announces rather than stacking a
      // duplicate the operator now has to dismiss twice.
      const next = [...current.filter((note) => note.message !== message), { id, tone, message }];
      return next.slice(-toastLimit);
    });
    if (tone === "danger") return;
    const linger = Math.min(12000, Math.max(4200, message.length * 55));
    toastTimers.current.set(
      id,
      window.setTimeout(() => {
        toastTimers.current.delete(id);
        setToasts((current) => current.filter((note) => note.id !== id));
      }, linger)
    );
  }, []);

  const dismissToast = useCallback((id: number) => {
    const timer = toastTimers.current.get(id);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      toastTimers.current.delete(id);
    }
    setToasts((current) => current.filter((note) => note.id !== id));
  }, []);

  useEffect(
    () => () => {
      toastTimers.current.forEach((timer) => window.clearTimeout(timer));
      toastTimers.current.clear();
    },
    []
  );

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

  // Whether the rail is off-canvas is a CSS decision at 900px; the shell needs to
  // know it too, so the hidden rail can be taken out of the tab order.
  const isCompact = useMediaQuery("(max-width: 900px)");

  // Escape closes the mobile rail, and while it is open the page behind it does
  // not scroll — otherwise the drawer slides over content that moved underneath.
  // Focus moves into the drawer on open and back to the menu button on close, so
  // a keyboard operator is never left tabbing through a panel they cannot see.
  const menuButton = useRef<HTMLButtonElement | null>(null);
  const rail = useRef<HTMLElement | null>(null);
  // Set when the drawer closes because the operator picked a section rather than
  // dismissing it. Returning focus to the hamburger would be right for a dismissal
  // and wrong here: the page behind has just been replaced, and focus belongs at
  // the top of the new one.
  const navigated = useRef(false);
  const workspace = useRef<HTMLElement | null>(null);
  useEffect(() => {
    if (!menuOpen) return;
    rail.current?.querySelector<HTMLAnchorElement>(".nav-item")?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("keydown", onKeyDown);
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKeyDown);
      document.body.style.overflow = previousOverflow;
      if (navigated.current) {
        navigated.current = false;
        workspace.current?.focus();
        return;
      }
      menuButton.current?.focus();
    };
  }, [menuOpen]);

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
    if (menuOpen) navigated.current = true;
    setPage(next);
    setMenuOpen(false);
    const search = query ? `?${new URLSearchParams(query).toString()}` : "";
    history.pushState({}, "", `/admin/${next}${search}`);
  };

  // Navigation entries are links, not buttons: an operator can middle-click a
  // section into a new tab, and the status bar shows where each one goes. The
  // click handler keeps it a single-page transition when it is a plain click.
  const navigateFromLink = (event: React.MouseEvent<HTMLAnchorElement>, next: Page) => {
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || event.button !== 0) return;
    event.preventDefault();
    navigate(next);
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

  const noteRow = (note: Note) => (
    <div className={`toast toast--${note.tone}`} key={note.id}>
      {note.tone === "success" ? (
        <Check size={16} aria-hidden="true" />
      ) : (
        <AlertTriangle size={16} aria-hidden="true" />
      )}
      <span className="toast__message">{note.message}</span>
      <button
        className="icon-button toast__close"
        onClick={() => dismissToast(note.id)}
        aria-label="Dismiss this message"
      >
        <X size={15} aria-hidden="true" />
      </button>
    </div>
  );
  const failures = toasts.filter((note) => note.tone === "danger");
  const confirmations = toasts.filter((note) => note.tone !== "danger");

  return (
    <div className="app-shell">
      <a className="skip-link" href="#workspace">Skip to content</a>
      <button
        className={`mobile-scrim ${menuOpen ? "is-visible" : ""}`}
        onClick={() => setMenuOpen(false)}
        aria-label="Close navigation"
        tabIndex={-1}
      />
      {/* Off-canvas at mobile widths, the rail is still in the document, so it is
          marked inert while closed — otherwise Tab lands on invisible links. */}
      <aside className={`sidebar ${menuOpen ? "is-open" : ""}`} inert={isCompact && !menuOpen} ref={rail}>
        <div className="wordmark">
          <span className="wordmark__mark" aria-hidden="true">
            <Cable size={18} aria-hidden="true" />
          </span>
          <span>
            <strong>ROTAKEY</strong>
            <small>routing control plane</small>
          </span>
        </div>
        <nav aria-label="Primary navigation">
          {navItems.map(({ id, label, icon: Icon }) => (
            <a
              key={id}
              className={`nav-item ${page === id ? "is-active" : ""}`}
              href={`/admin/${id}`}
              onClick={(event) => navigateFromLink(event, id)}
              aria-current={page === id ? "page" : undefined}
            >
              <Icon size={17} aria-hidden="true" />
              <span>{label}</span>
            </a>
          ))}
        </nav>
        <div className="sidebar__bottom">
          {/* latest_version is optional in the payload, so both readouts are gated
              on the string itself rather than on the flag. A release check that
              reports an update without naming the version rendered "Rotakey v". */}
          {version?.update_available && version.latest_version && (
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
            <BookOpen size={15} aria-hidden="true" />
            <span>Operator guide</span>
          </a>
          <a
            className="sidebar__github"
            href="https://github.com/jisunahamed/rotakey"
            target="_blank"
            rel="noreferrer"
          >
            <Github size={15} aria-hidden="true" />
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
            <button className="icon-button" onClick={() => void logout()} aria-label={`Sign out ${username}`}>
              <LogOut size={17} aria-hidden="true" />
            </button>
          </div>
          <div className="sidebar__version" title={version?.commit ? `Commit ${version.commit}` : undefined}>
            Rotakey v{version?.current_version ?? "0.2.7"}
            {version?.update_available && version.latest_version ? <span>new v{version.latest_version}</span> : <span>up to date</span>}
          </div>
        </div>
      </aside>

      {/* While the drawer is over the page, the page behind it is inert: without
          this, Tab walks out of the open drawer into the content it covers. The
          scrim sits outside this element so dismissing by click still works. */}
      <div className="workspace" inert={isCompact && menuOpen}>
        <header className="mobile-header">
          <button className="icon-button" onClick={() => setMenuOpen(true)} aria-label="Open navigation" aria-expanded={menuOpen} ref={menuButton}>
            <Menu size={19} aria-hidden="true" />
          </button>
          <strong>Rotakey</strong>
          <ThemeButton theme={theme} setTheme={setTheme} />
        </header>
        <main className="main-pane" id="workspace" tabIndex={-1} ref={workspace}>
          {page === "overview" && <OverviewPage navigate={navigate} notify={notify} />}
          {page === "providers" && <ProvidersPage notify={notify} />}
          {page === "models" && <ModelsPage navigate={navigate} notify={notify} />}
          {page === "playground" && <PlaygroundPage navigate={navigate} notify={notify} />}
          {page === "logs" && <LogsPage />}
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
      <div className="toast-dock">
        {/* Two regions, each with a fixed role, and a message renders into whichever
            one matches its tone. One shared wrapper that swapped role and aria-live
            at the same moment its text arrived was no better than inserting the
            region late: a screen reader that had already registered the node as
            polite could announce an error quietly, or miss it. Failures sit above
            confirmations so an error is never pushed off-screen by a success. */}
        <div className="toast-stack" role="alert" aria-live="assertive">
          {failures.map(noteRow)}
        </div>
        <div className="toast-stack" role="status" aria-live="polite">
          {confirmations.map(noteRow)}
        </div>
      </div>
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
        <Cable size={24} aria-hidden="true" />
        <div className="boot-line">
          <span />
        </div>
        <p>Starting Rotakey</p>
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
      setError(caught instanceof Error ? caught.message : "The gateway did not answer. Check that it is running, then try again.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="auth-shell">
      <section className="auth-panel auth-panel--setup">
        <div className="auth-panel__intro">
          <p className="eyebrow">First run · owner setup</p>
          <h1>Set up Rotakey.</h1>
          <p>
            Create the only admin account, then save the generated gateway key. Providers stay
            private behind one API.
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
            {busy ? "Creating owner and gateway key…" : "Create owner and gateway key"}
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
  const nextLabel = next === "system" ? "match the system" : next;
  // The label names the action, not the current value: "Theme: dark" left the
  // operator guessing what pressing it would do.
  return (
    <button className="icon-button" onClick={() => setTheme(next)} aria-label={`Switch theme to ${nextLabel}`} title={`Theme: ${theme === "system" ? "matching the system" : theme}`}>
      <Icon size={17} aria-hidden="true" />
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
  // Matches the stylesheet breakpoint where the inspector stops being a column
  // and starts covering the console.
  const inspectorFloating = useMediaQuery("(max-width: 1050px)");
  const [refreshing, setRefreshing] = useState(false);
  // Every request carries the generation the range was on when it left. The `all`
  // range is much slower than `1h`, so without this a stale reply lands after a
  // switch and the chart contradicts the highlighted button.
  const generation = useRef(0);
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
    const mine = ++generation.current;
    setError("");
    setRefreshing(true);
    try {
      const result = normalizeOverview(await api<Overview>(`/api/admin/overview?range=${range}`));
      if (mine !== generation.current) return;
      setOverview(result);
      setSelected((current) => current && overviewTargetExists(result, current) ? current : null);
      setExpandedProviders((current) => new Set([...current].filter((id) => result.providers.some((provider) => provider.id === id))));
    } catch (caught) {
      if (mine !== generation.current) return;
      setError(caught instanceof Error ? caught.message : "The gateway did not answer. It may still be starting up.");
    } finally {
      if (mine === generation.current) setRefreshing(false);
    }
  }, [range]);
  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 10_000);
    return () => {
      window.clearInterval(timer);
      // Bumping on teardown discards whatever is still in flight for the range
      // being left behind, so it cannot land on the next one.
      generation.current += 1;
    };
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
      {/* A first load that fails leaves nothing to show, and the command bar that
          holds Refresh lives inside the overview block below — so on that path the
          page used to be one sentence with no control on it at all. The other three
          list pages answer a dead first load with an empty state that carries the
          retry; this one now does the same, and keeps the inline notice for the case
          where a refresh failed but the last good numbers are still on screen. */}
      {!overview && error ? (
        <>
          <h1 className="sr-only">Gateway overview</h1>
          <EmptyState
            level={2}
            title="Overview could not be loaded"
            description={error}
            action={<Button onClick={() => void load()}><RefreshCw size={14} aria-hidden="true" /> Try again</Button>}
          />
        </>
      ) : error ? (
        <InlineNotice tone="danger">{error}</InlineNotice>
      ) : null}
      {overview && (
        <div className="ops-console">
          {/* Every other page opens with a PageHeader h1. This one opens straight
              into the command bar, so the heading that names the page is present
              but unstyled — without it the document starts at h2 and the page has
              no title to jump to. */}
          <h1 className="sr-only">Gateway overview</h1>
          <header className="ops-commandbar">
            <div className="ops-commandbar__identity">
              <span className={`gateway-pulse ${overview.summary.routes_ready === overview.summary.routes_total ? "" : "is-warning"}`} aria-hidden="true" />
              <span>
                <small>Unified gateway · {overview.summary.gateway_key_ready ? "authenticated" : "key missing"}</small>
                <code title={overview.base_url || `${location.origin}/v1`}>{overview.base_url || `${location.origin}/v1`}</code>
              </span>
              <button
                className="console-icon copy-base-url"
                aria-label="Copy unified base URL"
                onClick={() => {
                  void copyText(overview.base_url || `${location.origin}/v1`)
                    .then(() => notify("Base URL copied."))
                    .catch(() => notify(clipboardBlocked, "danger"));
                }}
              ><Clipboard size={14} aria-hidden="true" /><span>Copy</span></button>
            </div>
            <div className="ops-commandbar__actions">
              {/* A group of mutually exclusive choices, so it is announced as one
                  named control rather than four unrelated buttons. */}
              <div className="range-switcher" role="group" aria-label="Overview time range">
                {(["1h", "24h", "7d", "all"] as const).map((value) => (
                  <button
                    key={value}
                    className={range === value ? "is-active" : ""}
                    aria-pressed={range === value}
                    aria-label={rangeLabel(value)}
                    onClick={() => {
                      setRange(value);
                      history.replaceState({}, "", `/admin/overview?range=${value}`);
                    }}
                  >{value}</button>
                ))}
              </div>
              <button className={`console-refresh ${refreshing ? "is-refreshing" : ""}`} onClick={() => void load()} aria-label={refreshing ? "Syncing the overview" : "Refresh the overview"}>
                <RefreshCw size={14} aria-hidden="true" /> {refreshing ? "Syncing" : "Refresh"}
              </button>
            </div>
          </header>

          <section className={`status-ledger${overview.summary.credit.tracked_keys > 0 ? " status-ledger--credit" : ""}`} aria-label={`${rangeLabel(range)} gateway status`}>
            <LedgerMetric label="Routes ready" value={`${overview.summary.routes_ready}/${overview.summary.routes_total}`} tone={overview.summary.routes_ready < overview.summary.routes_total ? "danger" : "healthy"} />
            <LedgerMetric label="API keys ready" value={`${overview.summary.keys_ready}/${overview.summary.keys_total}`} tone={overview.summary.keys_warning ? "warning" : "healthy"} />
            <LedgerMetric label="Requests" value={formatNumber(overview.summary.requests)} />
            <LedgerMetric label="Errors" value={`${formatNumber(overview.summary.errors)} · ${(overview.summary.error_rate * 100).toFixed(1)}%`} tone={overview.summary.errors ? "danger" : "default"} />
            <LedgerMetric label="P95 latency" value={`${formatNumber(overview.summary.latency_p95_ms)} ms`} />
            <LedgerMetric label="Tokens" value={formatCompact(overview.summary.tokens)} />
            <LedgerMetric label="Estimated cost" value={formatUSD(overview.summary.estimated_cost_usd)} />
            {overview.summary.credit.tracked_keys > 0 && (
              <LedgerMetric
                label="Balance left"
                value={formatUSD(overview.summary.credit.remaining_usd)}
                tone={creditTone(overview.summary.credit)}
              />
            )}
          </section>

          <div className={`ops-console__workspace${selected ? " has-inspector" : ""}`}>
            {/* While the inspector floats over the console, the console behind it
                is inert — otherwise Tab leaves the drawer for controls it covers. */}
            <div className="ops-console__main" inert={inspectorFloating && inspectorOpen && Boolean(selected)}>
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
                    action={<Button onClick={() => navigate("providers")}><Plus size={15} aria-hidden="true" /> Add first provider</Button>}
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
                <ConsolePanelHeader eyebrow="Request evidence" title="Recent failures" detail={`Selected range · ${rangeLabel(range)}`} />
                {overview.recent_failures.length === 0 ? (
                  <p className="console-empty">No failed requests in this range.</p>
                ) : (
                  <div className="failure-list">
                    {overview.recent_failures.map((failure) => (
                      <button
                        key={failure.request_id}
                        aria-label={`HTTP ${failure.status_code} on ${failure.model_alias} via ${failure.provider_name}, ${formatRelativeTime(failure.created_at)}. Open in request logs.`}
                        onClick={() => navigate("logs", { q: failure.request_id, status: String(failure.status_code) })}
                      >
                        <span className="failure-code" aria-hidden="true">{failure.status_code}</span>
                        <span aria-hidden="true"><code title={failure.model_alias}>{failure.model_alias}</code><small title={failure.error_code || failure.provider_name}>{failure.error_code || failure.provider_name}</small></span>
                        <span aria-hidden="true"><strong title={failure.credential_label || "No key"}>{failure.credential_label || "No key"}</strong><small>{failure.latency_ms} ms</small></span>
                        <time dateTime={failure.created_at} aria-hidden="true">{formatRelativeTime(failure.created_at)}</time>
                        <ChevronRight size={14} aria-hidden="true" />
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
                    inspection.valid ? `API key is valid · ${inspection.models.length} models visible.` : inspection.warning || keyCheckFailed,
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

/** creditTone mirrors the backend's 20% low-balance threshold so a key the API
 * has already alerted on does not still read as healthy on the dashboard. */
function creditTone(credit: CreditTotals): "default" | "warning" | "danger" {
  if (credit.exhausted_keys > 0 || credit.remaining_usd <= 0) return "danger";
  if (credit.balance_usd > 0 && credit.remaining_usd / credit.balance_usd <= 0.2) return "warning";
  return "default";
}

/** segmentTrafficLabel answers "which key served how many calls, and what is left
 * on it" in one line. Empty when the key tracks no balance and served no traffic,
 * so the row falls back to its routing state. */
function segmentTrafficLabel(segment: Overview["routes"][number]["segments"][number]) {
  const credit = segment.credit;
  if (!credit) return "";
  const parts = [`${formatNumber(credit.requests ?? 0)} calls`, `${formatUSD(credit.remaining_usd)} left`];
  if ((credit.errors ?? 0) > 0) parts.push(`${formatNumber(credit.errors ?? 0)} failed`);
  return parts.join(" · ");
}

function ConsolePanelHeader({ eyebrow, title, detail }: { eyebrow: string; title: string; detail?: string }) {
  return (
    <header className="console-panel__header">
      <div><span>{eyebrow}</span><h2>{title}</h2></div>
      {/* The detail is the first thing to ellipsis when a panel narrows, so it
          keeps its full text on a title. */}
      {detail && <small title={detail}>{detail}</small>}
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
      <ConsolePanelHeader eyebrow="Signal timeline" title={`Traffic · ${rangeLabel(overview.range)}`} detail={`P50 ${overview.summary.latency_p50_ms} ms`} />
      <div className="signal-chart">
        {/* A line chart has no text equivalent, so the label carries the shape of
            the data: the peaks and the number of buckets that saw errors. */}
        <svg viewBox="0 0 720 170" role="img" aria-label={`Requests and P95 latency over ${overview.range}. Peak ${formatNumber(requestMax)} requests per bucket, peak P95 ${formatLatency(latencyMax)}. ${errorPoints.filter((point) => point.active).length} of ${overview.series.length} buckets recorded errors.`}>
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
        <div className="signal-legend" aria-hidden="true">
          <span><i className="is-request" /> requests</span>
          <span><i className="is-latency" /> p95 latency</span>
          <span><i className="is-error" /> error bucket</span>
        </div>
      </div>
    </section>
  );
}

/** The range is a URL enum, and reading it out raw produced labels like "all
 *  gateway status" and "Traffic · all". These are the same words the range
 *  switcher's own buttons announce, so a heading and its control agree. */
const rangeNames: Record<Overview["range"], string> = {
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  all: "All time"
};

function rangeLabel(range: Overview["range"]) {
  return rangeNames[range] ?? String(range);
}

function ModelUsageRanking({ routes, range }: { routes: Overview["routes"]; range: Overview["range"] }) {
  const ranking = [...routes].filter((route) => route.requests > 0).sort((a, b) => b.estimated_cost_usd - a.estimated_cost_usd || b.tokens - a.tokens || b.requests - a.requests).slice(0, 6);
  return <section className="console-panel model-ranking"><ConsolePanelHeader eyebrow="Usage ledger" title="Model ranking" detail={`${rangeLabel(range)} · ranked by estimated cost`} />
    {/* Four columns of figures with a header row is a table, and it is read as one:
        without the roles a screen reader announces twenty-four loose numbers with
        no indication of which column any of them belongs to. Below 650px the table
        scrolls sideways inside the panel, and a scroll container that only a
        pointer can move is unreachable — tabIndex makes the arrow keys work. */}
    {ranking.length === 0 ? <p className="console-empty">No model usage in this range.</p> : <div className="model-ranking__table" role="table" aria-label={`Model usage ranked by estimated cost, ${rangeLabel(range)}`} tabIndex={0}><div className="model-ranking__head" role="row"><span role="columnheader">Model</span><span role="columnheader">Requests</span><span role="columnheader">Tokens</span><span role="columnheader">Cost</span></div>{ranking.map((route, index) => <div className="model-ranking__row" role="row" key={route.id}><strong role="rowheader"><i aria-hidden="true">{index + 1}</i><code title={route.alias}>{route.alias}</code></strong><span role="cell">{formatNumber(route.requests)}</span><span role="cell">{formatCompact(route.tokens)}</span><span role="cell">{formatUSD(route.estimated_cost_usd)}</span></div>)}</div>}
  </section>;
}

function AttentionQueue({ alerts, onSelect }: { alerts: Overview["alerts"]; onSelect: (alert: Overview["alerts"][number]) => void }) {
  // Six rows is what the panel holds without becoming the whole page. The heading
  // counts every alert, so a longer queue used to promise more than the list showed
  // and the rest could not be reached from here at all.
  const shown = 6;
  const [expanded, setExpanded] = useState(false);
  const visible = expanded ? alerts : alerts.slice(0, shown);
  const hidden = alerts.length - visible.length;
  return (
    <section className="console-panel attention-panel">
      <ConsolePanelHeader eyebrow="Attention queue" title={alerts.length ? `${alerts.length} need attention` : "All clear"} />
      {alerts.length === 0 ? (
        <div className="all-clear"><Check size={16} aria-hidden="true" /><span><strong>No intervention needed</strong><small>Routes and API keys are ready.</small></span></div>
      ) : (
        <div className="attention-list">
          {visible.map((alert) => (
            <button key={alert.id} className={`attention-item attention-item--${alert.severity}`} onClick={() => onSelect(alert)} aria-label={`${alert.severity === "critical" ? "Critical" : alert.severity === "warning" ? "Warning" : "Note"}: ${alert.title}. ${alert.detail}`}>
              <AlertTriangle size={14} aria-hidden="true" />
              {/* The row is one line by design and the detail is ellipsised, so the
                  full sentence is on the title as well — clicking opens it in the
                  inspector, but a sighted operator should not have to click to read
                  a sentence that is only a few characters too long. */}
              <span aria-hidden="true"><strong title={alert.title}>{alert.title}</strong><small title={alert.detail}>{alert.detail}</small></span>
              <ChevronRight size={13} aria-hidden="true" />
            </button>
          ))}
          {(hidden > 0 || expanded) && (
            <button type="button" className="link-button attention-more" onClick={() => setExpanded((current) => !current)}>
              {hidden > 0 ? `Show ${hidden} more` : `Show only the first ${shown}`}
            </button>
          )}
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
          <ChevronDown size={15} aria-hidden="true" />
          <StatusDot state={!provider.enabled ? "disabled" : provider.keys_ready ? "healthy" : "exhausted"} />
          <span><strong title={provider.name}>{provider.name}</strong><small title={`${provider.models_ready}/${provider.models_total} routes ready · ${provider.keys_ready}/${provider.keys_total} keys ready${provider.credit.tracked_keys > 0 ? ` · ${formatUSD(provider.credit.remaining_usd)} balance left` : ""}`}>{provider.models_ready}/{provider.models_total} routes ready · {provider.keys_ready}/{provider.keys_total} keys ready{provider.credit.tracked_keys > 0 ? ` · ${formatUSD(provider.credit.remaining_usd)} balance left` : ""}</small></span>
        </button>
        <div className="limit-preview">
          {capacityDimensions.map((dimension) => <LimitCell key={dimension} dimension={dimension} limit={provider.capacity[dimension]} />)}
        </div>
        <button className="provider-inspect" onClick={() => onSelect({ type: "provider", id: provider.id })} aria-label={`Inspect ${provider.name}`}>Inspect</button>
      </div>
      {/* The container is always in the document so aria-controls resolves. It
          used to be mounted only while expanded, which pointed the toggle at a
          missing ID — a screen reader announced a collapsed control with nothing
          to expand. The rows themselves stay unmounted until they are needed. */}
      <div className="route-debug-list" id={`provider-models-${provider.id}`} hidden={!expanded}>
        {expanded && <>
          <div className="route-debug-columns" aria-hidden="true">
            <span>Public alias</span><span>Traffic</span><span>Keys</span><span>Next key</span><span>Model override</span><span />
          </div>
          {routes.length ? routes.map((route) => (
            <RouteDebugRow key={route.id} route={route} selected={selected} onSelect={onSelect} />
          )) : <p className="provider-model-empty">No routes yet. Add one on the Model routes page.</p>}
        </>}
      </div>
    </section>
  );
}

/** One bucket's capacity, as the two strings every capacity readout needs: the
 *  sentence a screen reader or a title attribute gets, and the abbreviation that
 *  fits in a four-character cell. Both the overview's limit cells and the
 *  provider page's capacity strip render the same bucket, and they had drifted —
 *  an unknown remaining read "?" in one and "? / 400K" in the other. The compact
 *  form is unspaced because the narrower of the two cells ellipsises at about
 *  eight characters. */
function limitReading(limit?: { limit: number; remaining: number; unlimited: boolean; unknown: boolean }) {
  if (!limit) return { sentence: "no limit set", compact: "—" };
  if (limit.unlimited) return { sentence: "unlimited", compact: "∞" };
  if (limit.unknown) {
    return {
      sentence: `remaining unknown of ${formatNumber(limit.limit)}`,
      compact: `?/${formatCompact(limit.limit)}`
    };
  }
  return {
    sentence: `${formatNumber(limit.remaining)} of ${formatNumber(limit.limit)} left`,
    compact: `${formatCompact(limit.remaining)}/${formatCompact(limit.limit)}`
  };
}

function LimitCell({ dimension, limit }: { dimension: string; limit?: Overview["providers"][number]["capacity"][string] }) {
  const ratio = limit && !limit.unlimited && limit.limit > 0 ? limit.remaining / limit.limit : 1;
  const tone = limit?.unknown ? "unknown" : ratio <= 0 ? "critical" : ratio <= 0.2 ? "warning" : "healthy";
  // The cell is four characters wide by design, so the reading it abbreviates goes
  // on the title in full: "88K/400K" is a rounding of the number an operator is
  // about to make a capacity decision on.
  const reading = limitReading(limit);
  return (
    <span className={`limit-cell limit-cell--${tone}`} title={`${dimension.toUpperCase()} provider capacity: ${reading.sentence}`}>
      <small>{dimension}</small>
      <strong>{reading.compact}</strong>
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
  // The column headings above this row are decorative, so the row states its own
  // figures: without this a screen reader reads five bare numbers in a row.
  const rowLabel = [
    route.alias,
    statusLabel(state),
    `${formatNumber(route.requests)} requests`,
    `${(route.error_rate * 100).toFixed(1)}% errors`,
    `${route.healthy_credentials} of ${route.total_credentials} keys ready`,
    route.next_credential_id ? `next key ${route.segments.find((segment) => segment.cursor)?.label ?? "unnamed"}` : "no key available",
    modelBottleneck === NO_MODEL_LIMIT ? "no model limit, the provider limit applies" : `model limit ${modelBottleneck}`
  ].join(" · ");
  return (
    <button
      className={`route-debug-row ${isSelected ? "is-selected" : ""}`}
      onClick={() => onSelect({ type: "route", id: route.id })}
      aria-current={isSelected}
      aria-label={rowLabel}
    >
      <span className="route-debug-row__identity" aria-hidden="true">
        <StatusDot state={state} />
        <span><code>{route.alias}</code><small>{route.upstream_model !== route.alias ? `Upstream · ${route.upstream_model}` : route.supports_responses ? "Chat + Responses" : "Chat Completions"}</small></span>
      </span>
      <span className="route-traffic" aria-hidden="true"><strong>{formatNumber(route.requests)}</strong><small>{(route.error_rate * 100).toFixed(1)}% err · {formatNumber(route.latency_p95_ms)} ms</small></span>
      <span className="route-key-count" aria-hidden="true"><strong>{route.healthy_credentials}/{route.total_credentials}</strong><small>ready</small></span>
      <span className="route-next-key" aria-hidden="true"><strong>{route.segments.find((segment) => segment.cursor)?.label || "—"}</strong><small>{route.next_credential_id ? "next" : "unavailable"}</small></span>
      <span className="route-bottleneck" aria-hidden="true"><strong>{modelBottleneck}</strong><small>{modelBottleneck === NO_MODEL_LIMIT ? "provider limit applies" : "model limit"}</small></span>
      <ChevronRight size={14} aria-hidden="true" />
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
  // Below 1050px this stops being a pane beside the console and becomes a fixed
  // panel over it, so it takes focus, keeps Tab inside itself, closes on Escape
  // and hands focus back on close. The hook has to run before the empty-state
  // return so the hook order does not change with the selection.
  const drawer = useDrawerOverlay({ open, active: useMediaQuery("(max-width: 1050px)"), onClose });
  if (!provider && !route && !credential) {
    return <aside className={`overview-inspector is-empty${open ? " is-open" : ""}`}><CircleGauge size={20} aria-hidden="true" /><strong>Select a provider, route or key</strong><p>Inspect the next key, limiting bucket and reset without leaving Overview.</p></aside>;
  }
  return (
    <aside className={`overview-inspector${open ? " is-open" : ""}`} ref={drawer as React.Ref<HTMLElement>} tabIndex={-1}>
      <header>
        <div><span>{credential ? "API key" : route ? "Model route" : "Provider"}</span><h2>{credential?.label || route?.alias || provider?.name}</h2></div>
        <button className="console-icon inspector-close" onClick={onClose} aria-label="Close inspector"><X size={15} aria-hidden="true" /></button>
      </header>
      {route && (
        <div className="route-trace" aria-label="Selected route debugger trace">
          <TraceNode label="model" value={route.alias} />
          <ArrowRight size={15} aria-hidden="true" />
          <TraceNode label="provider" value={route.provider} />
          <ArrowRight size={15} aria-hidden="true" />
          <TraceNode label="next key" value={route.segments.find((segment) => segment.cursor)?.label || "none"} tone={!route.next_credential_id ? "danger" : "default"} />
          <ArrowRight size={15} aria-hidden="true" />
          <TraceNode label="limit" value={formatRouteBottleneck(route)} />
        </div>
      )}
      {credential && (
        <>
          {credential.validation_error && <InlineNotice tone="danger">{credential.validation_error}</InlineNotice>}
          {credential.credit?.exhausted && (
            <InlineNotice tone="danger">
              This key is out of balance, so the router skips it. Raise the balance per key on the provider page to bring it back.
            </InlineNotice>
          )}
          <div className="inspector-definition">
            <Definition label="Status" value={statusLabel(credential.status)} />
            <Definition label="Key ending" value={`•••• ${credential.secret_suffix}`} />
            <Definition label="Routing role" value={[credential.primary ? "Primary" : "", credential.cursor ? "next" : ""].filter(Boolean).join(" · ") || "fallback"} />
            <Definition label="Last checked" value={credential.last_validated_at ? formatRelativeTime(credential.last_validated_at) : "not recorded"} />
            <Definition
              label="Balance left"
              value={credential.credit ? formatUSD(credential.credit.remaining_usd) : "not tracked"}
            />
            <Definition label="Spent" value={credential.credit ? formatUSD(credential.credit.spent_usd) : "—"} />
            {credential.credit && (
              <>
                <Definition label="Calls on this key" value={formatNumber(credential.credit.requests ?? 0)} />
                <Definition label="Failed calls" value={formatNumber(credential.credit.errors ?? 0)} />
                <Definition label="Tokens on this key" value={formatCompact(credential.credit.tokens ?? 0)} />
              </>
            )}
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
            <Definition label="Provider" value={provider.enabled ? "On" : "Off"} />
            <Definition
              label="Balance per key"
              value={provider.default_key_balance_usd == null ? "not tracked" : formatUSD(provider.default_key_balance_usd)}
            />
            {provider.credit.tracked_keys > 0 && (
              <>
                <Definition label="Balance left" value={formatUSD(provider.credit.remaining_usd)} />
                <Definition label="Spent" value={formatUSD(provider.credit.spent_usd)} />
                <Definition label="Keys out of balance" value={`${provider.credit.exhausted_keys}/${provider.credit.tracked_keys} tracked`} />
                {(provider.credit.unattributed_spent_usd ?? 0) > 0 && (
                  <Definition
                    label="Charged provider-wide"
                    value={formatUSD(provider.credit.unattributed_spent_usd ?? 0)}
                  />
                )}
              </>
            )}
          </div>
          {(provider.credit.unattributed_spent_usd ?? 0) > 0 && (
            <p className="inspector-note">
              Some requests ended before the gateway recorded which key served them. That spend comes off this
              provider's remaining balance instead of one key's, so the totals here can run ahead of the per-key figures.
            </p>
          )}
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
          <Rotor
            keys={route.segments}
            stalled={!route.next_credential_id}
            stalledNote="No key in this pool can serve the next request. Check the keys below."
          />
          <div className="inspector-key-list">
            <span>Key order</span>
            {route.segments.map((segment) => (
              <button key={segment.id} onClick={() => onSelectCredential(segment.id)} aria-label={`${segment.label}, ${segment.cursor ? "serves the next request" : statusLabel(segment.status)}${segmentTrafficLabel(segment) ? `, ${segmentTrafficLabel(segment)}` : ""}`}>
                <StatusDot state={segment.status} /><strong title={segment.label}>{segment.label}</strong><small title={segmentTrafficLabel(segment)}>{segmentTrafficLabel(segment) || (segment.cursor ? "next" : statusLabel(segment.status))}</small><ChevronRight size={13} aria-hidden="true" />
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
          }}><RefreshCw size={14} aria-hidden="true" /> {busy ? "Checking…" : "Check key"}</Button>
        ) : provider ? (
          <Button variant="quiet" disabled={busy} onClick={() => {
            setBusy(true);
            void onTest().finally(() => setBusy(false));
          }}><Activity size={14} aria-hidden="true" /> {busy ? "Checking…" : "Check every key"}</Button>
        ) : null}
        {provider && <Button variant="quiet" onClick={() => navigate("providers", { provider: provider.id })}>Open provider</Button>}
        {route && <Button variant="quiet" onClick={() => navigate("providers", { provider: route.provider_id })}>Manage model limits</Button>}
        {route && <Button variant="quiet" onClick={() => navigate("logs", { q: route.alias })}>View route logs</Button>}
      </div>
    </aside>
  );
}

/** The rotor: one segment per API key in the pool, in the order the router walks
 *  them, coloured by the state that decides whether it will be used, with the key
 *  serving the next request marked. It answers the question the inspector is opened
 *  for — which key is up, and how much of the pool can still serve — in a single
 *  glance, where the list below it answers the same question one row at a time.
 *
 *  It earns its place at forty keys rather than at four: a pool that size is a long
 *  scroll, and the band is the only reading that fits the whole of it on screen.
 *
 *  The track is decorative: every segment's information is already in the list
 *  underneath it as a labelled, focusable row, or on the Providers page in the key
 *  rows below. So the track is hidden from assistive technology and the caption
 *  above it carries the reading in words. */
function Rotor({
  keys,
  stalled = false,
  stalledNote
}: {
  keys: Array<{ id: string; status: string; cursor?: boolean; unknown?: boolean }>;
  /** True when nothing in the pool can serve the next request. The track has no
   *  cursor to point at, which has to be said in words as well as drawn. */
  stalled?: boolean;
  stalledNote?: string;
}) {
  if (keys.length === 0) return null;
  const servable = keys.filter((key) => key.status === "healthy").length;
  return (
    <div className={`rotor${stalled ? " rotor--stalled" : ""}`}>
      <div className="rotor__caption">
        <span>Key rotation</span>
        <span className="rotor__tally">
          {servable}/{keys.length} can serve
        </span>
      </div>
      <div className="rotor__track" aria-hidden="true">
        {keys.map((key) => (
          <span
            key={key.id}
            className={`rotor__segment rotor__segment--${key.unknown ? "unknown" : key.status}${key.cursor ? " rotor__segment--cursor" : ""}`}
          />
        ))}
      </div>
      {stalled && stalledNote && <p className="rotor__stalled-note">{stalledNote}</p>}
    </div>
  );
}

function TraceNode({ label, value, tone = "default" }: { label: string; value: string; tone?: "default" | "danger" }) {
  return <span className={`trace-node trace-node--${tone}`}><small>{label}</small><strong>{value}</strong></span>;
}

function Definition({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  // Two of these sit side by side in a narrow inspector and the value ellipsises
  // rather than widening the pane, so the full text stays on the title. A provider
  // name is the usual casualty: it is the one value an operator names themselves.
  return <div><span>{label}</span><strong className={mono ? "is-mono" : ""} title={value}>{value}</strong></div>;
}

/** A limit is either the provider's shared key limit or one set for a single model.
 *  The enum reaching the screen said "shared" and "model" with no noun, which reads
 *  as a category rather than as the limit doing the limiting. */
function limitScopeLabel(scope: string | undefined) {
  return scope === "model" ? "model limit" : "shared key limit";
}

function HeadroomReadout({ label, headroom }: { label: string; headroom?: Overview["routes"][number]["next_request_headroom"] }) {
  if (!headroom) {
    return <div className="headroom-readout is-unlimited"><span>{label}</span><strong>Unlimited</strong><small>No rate limit is set on this path.</small></div>;
  }
  const ratio = headroom.limit ? Math.max(0, Math.min(1, headroom.remaining / headroom.limit)) : 1;
  return (
    <div className={`headroom-readout ${ratio <= 0 ? "is-critical" : ratio <= 0.2 ? "is-warning" : ""}`}>
      <span>{label} · {limitScopeLabel(headroom.scope)}</span>
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

/** Narrows away the nulls `filter(Boolean)` leaves behind in the type, so the
 *  headroom comparisons below read the fields directly instead of asserting. */
function isHeadroom(
  headroom: Overview["routes"][number]["next_request_headroom"]
): headroom is NonNullable<Overview["routes"][number]["next_request_headroom"]> {
  return Boolean(headroom);
}

/** Whichever bucket has the least room left as a share of its own limit is the one
 *  that will stop the next request, so that is the one worth showing. */
function tightestHeadroom(
  candidates: Array<NonNullable<Overview["routes"][number]["next_request_headroom"]>>
) {
  return candidates.reduce((tightest, candidate) => (
    candidate.remaining / Math.max(candidate.limit, 1) < tightest.remaining / Math.max(tightest.limit, 1)
      ? candidate
      : tightest
  ));
}

/** Shown where a route has no model-scoped bucket at all, so the provider's shared
 *  limit is the only thing governing it. */
const NO_MODEL_LIMIT = "No model limit";

function formatRouteBottleneck(route: Overview["routes"][number]) {
  const candidates = [route.next_request_headroom, route.next_token_headroom].filter(isHeadroom);
  if (!route.next_credential_id) return "no route";
  if (candidates.length === 0) return "unlimited";
  const tightest = tightestHeadroom(candidates);
  return `${formatCompact(tightest.remaining)}/${formatCompact(tightest.limit)} ${tightest.dimension}`;
}

function formatModelBottleneck(route: Overview["routes"][number]) {
  const candidates = [route.next_request_headroom, route.next_token_headroom]
    .filter(isHeadroom)
    .filter((headroom) => headroom.scope === "model");
  if (candidates.length === 0) return NO_MODEL_LIMIT;
  const tightest = tightestHeadroom(candidates);
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
  const reset = new Date(value).getTime();
  if (Number.isNaN(reset)) return "unknown";
  const milliseconds = reset - now;
  if (milliseconds <= 0) return "now";
  if (milliseconds < 60_000) return `in ${Math.ceil(milliseconds / 1000)}s`;
  if (milliseconds < 3_600_000) return `in ${Math.ceil(milliseconds / 60_000)}m`;
  return `at ${new Date(reset).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

function formatRelativeTime(value: string) {
  const stamp = new Date(value).getTime();
  // Every comparison against NaN is false, so an unparseable timestamp used to
  // fall through this ladder and render the literal text "NaNd ago".
  if (Number.isNaN(stamp)) return "at an unknown time";
  const seconds = Math.max(0, Math.floor((Date.now() - stamp) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function ProvidersPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const ask = useConfirm();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(location.search).get("provider") || "");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [openSection, setOpenSection] = useState<"models" | "credentials" | null>(null);
  const [providerInspectorOpen, setProviderInspectorOpen] = useState(false);
  // Below 900px the inspector is a fixed drawer over the provider list, so it
  // behaves like one for a keyboard too. Above it, nothing changes.
  const inspectorFloating = useMediaQuery("(max-width: 900px)");
  const providerDrawer = useDrawerOverlay({
    open: providerInspectorOpen,
    active: inspectorFloating,
    onClose: () => setProviderInspectorOpen(false)
  });
  const [testing, setTesting] = useState(false);
  const [deleting, setDeleting] = useState(false);
  // The panel holds ids, not the records themselves. A snapshot taken when the
  // sheet opened went stale the moment the ten-second reload landed, so an edit
  // could be saved against limits or a label that had already changed upstream.
  const [panel, setPanel] = useState<
    null
    | { type: "wizard" }
    | { type: "provider"; providerID: string }
    | { type: "model"; providerID: string; modelID?: string }
    | { type: "import"; providerID: string }
    | { type: "credential"; providerID: string; credentialID?: string }
  >(null);
  // The ten-second refresh and an explicit one after a save overlap, and the two
  // are not guaranteed to land in the order they were sent. Without this, the
  // slower of the two wins: a provider deleted a moment ago comes back in the
  // list, and gets re-selected because it is still the id in the address bar.
  const generation = useRef(0);

  const load = useCallback(async () => {
    const mine = ++generation.current;
    setError("");
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      if (mine !== generation.current) return;
      const normalized = normalizeProviders(result.providers);
      setProviders(normalized);
      setSelectedID((current) => normalized.some((provider) => provider.id === current) ? current : normalized[0]?.id || "");
    } catch (caught) {
      if (mine !== generation.current) return;
      setError(caught instanceof Error ? caught.message : "Providers could not be loaded.");
    } finally {
      if (mine === generation.current) setLoading(false);
    }
  }, []);
  useEffect(() => {
    void load();
    const refresh = window.setInterval(() => void load(), 10_000);
    return () => window.clearInterval(refresh);
  }, [load]);

  const selected = providers.find((provider) => provider.id === selectedID);
  // Sheets read their provider out of the live list, so the ten-second reload
  // keeps them current instead of leaving them on a snapshot.
  const panelProvider = panel && panel.type !== "wizard"
    ? providers.find((provider) => provider.id === panel.providerID)
    : undefined;
  useEffect(() => setOpenSection(null), [selectedID]);
  // The selected provider lives in the address bar, so a reload or a shared link
  // reopens the same upstream instead of dropping the operator on the first one.
  useEffect(() => {
    if (!selectedID) return;
    const url = new URL(location.href);
    if (url.searchParams.get("provider") === selectedID) return;
    url.searchParams.set("provider", selectedID);
    history.replaceState(history.state, "", url);
  }, [selectedID]);
  const complete = (message: string) => {
    setPanel(null);
    notify(message);
    void load();
  };

  // A panel holds ids, and the ten-second reload can find that one of them is gone
  // — someone deleted the provider in another tab, or a key was revoked upstream.
  // The panel used to just disappear mid-edit. Worse, a credential id that stopped
  // resolving left the panel mounted in "Add API key" mode with the operator's
  // fields still filled in, so the next Save was a POST that created a second key
  // instead of a PUT that updated the first. Now the panel closes and says which
  // record went away.
  const missingRecord = (() => {
    if (loading || !panel || panel.type === "wizard") return null;
    const owner = providers.find((provider) => provider.id === panel.providerID);
    if (!owner) return "provider";
    if (panel.type === "credential" && panel.credentialID
      && !owner.credentials.some((credential) => credential.id === panel.credentialID)) return "credential";
    if (panel.type === "model" && panel.modelID
      && !owner.models.some((model) => model.id === panel.modelID)) return "model";
    return null;
  })();
  useEffect(() => {
    if (!missingRecord) return;
    setPanel(null);
    notify(
      missingRecord === "provider"
        ? "That provider is no longer here, so the panel closed. Anything unsaved in it was not applied."
        : missingRecord === "credential"
          ? "That API key is no longer here, so the panel closed. Anything unsaved in it was not applied."
          : "That model route is no longer here, so the panel closed. Anything unsaved in it was not applied.",
      "danger"
    );
  }, [missingRecord, notify]);

  return (
    <div className="resource-page provider-page">
      <PageHeader
        eyebrow="Upstream setup"
        title="Providers"
        description="Setup stays provider-wise. Every application still calls one model-wise gateway."
        actions={<Button onClick={() => setPanel({ type: "wizard" })}><Plus size={15} aria-hidden="true" /> Add provider</Button>}
      />
      {/* On a failed first load the empty state below already carries the message
          and the retry, so the banner would say the same thing twice. It appears
          only when a refresh fails over a list that is still on screen. */}
      {error && providers.length > 0 && (
        <InlineNotice tone="danger">
          {error}{" "}
          <button type="button" className="link-button" onClick={() => void load()}>Try again</button>
        </InlineNotice>
      )}
      {loading ? <PageSkeleton /> : error && providers.length === 0 ? (
        <EmptyState
          level={2}
          title="Providers could not be loaded"
          description={error}
          action={<Button onClick={() => void load()}><RefreshCw size={14} aria-hidden="true" /> Try again</Button>}
        />
      ) : providers.length === 0 ? (
        <EmptyState
          level={2}
          title="Connect the first upstream"
          description="You will define its base URL, model aliases, API keys and limits before enabling traffic."
          action={<Button onClick={() => setPanel({ type: "wizard" })}><Plus size={15} aria-hidden="true" /> Add provider</Button>}
        />
      ) : (
        <div className="resource-layout">
          {/* The list is inert while the drawer covers it, for the same reason the
              nav drawer makes the page behind it inert. */}
          <section className="resource-list" aria-label="Providers" inert={inspectorFloating && providerInspectorOpen && Boolean(selected)}>
            {providers.map((provider) => {
              const healthy = provider.credentials.filter((credential) => credentialPoolState(credential) === "healthy").length;
              return (
                <button
                  key={provider.id}
                  className={`resource-item ${selectedID === provider.id ? "is-selected" : ""}`}
                  aria-current={selectedID === provider.id}
                  onClick={() => { setSelectedID(provider.id); setProviderInspectorOpen(true); }}
                >
                  <StatusDot state={!provider.enabled ? "disabled" : healthy ? "healthy" : "exhausted"} />
                  <span><strong title={provider.name}>{provider.name} <em className={`protocol-badge is-${provider.api_format}`}>{provider.api_format === "anthropic" ? "Anthropic" : "OpenAI"}</em></strong><small title={provider.base_url}>{provider.base_url}</small></span>
                  <span className="resource-item__count">{provider.models.length} model{provider.models.length === 1 ? "" : "s"}</span>
                  <ChevronRight size={15} aria-hidden="true" />
                </button>
              );
            })}
          </section>
          {selected && (
            <section className={`resource-inspector${providerInspectorOpen ? " is-open" : ""}`} ref={providerDrawer as React.Ref<HTMLElement>} tabIndex={-1}>
              {!selected.enabled && (
                <InlineNotice tone="danger">
                  This provider is turned off. Its routes are excluded from every request until it is turned back on.
                </InlineNotice>
              )}
              <UnusableKeysNotice
                provider={selected}
                notify={notify}
                onDone={() => void load()}
              />
              <div className="inspector-header">
                <div>
                  <p className="eyebrow">{selected.api_format === "anthropic" ? "Anthropic-compatible upstream" : "OpenAI-compatible upstream"}</p>
                  <h2>{selected.name}</h2>
                  <code>{selected.base_url}</code>
                </div>
                <div className="button-row">
                  <button className="console-icon resource-inspector-close" onClick={() => setProviderInspectorOpen(false)} aria-label="Close provider inspector"><X size={15} aria-hidden="true" /></button>
                  <ProviderPowerButton provider={selected} onDone={complete} notify={notify} />
                  <Button
                    variant="quiet"
                    disabled={testing}
                    onClick={() => {
                      setTesting(true);
                      void testProvider(selected, notify).finally(() => setTesting(false));
                    }}
                  ><Activity size={15} aria-hidden="true" /> {testing ? "Checking…" : "Check every key"}</Button>
                  <Button variant="quiet" onClick={() => setPanel({ type: "provider", providerID: selected.id })}>Edit</Button>
                  <Button
                    variant="danger"
                    disabled={deleting}
                    aria-label={`Delete provider ${selected.name}`}
                    onClick={() => {
                      void (async () => {
                        // Naming what goes with it: the routes are the aliases callers
                        // send, and deleting them breaks those callers immediately.
                        const routes = `${selected.models.length} model route${selected.models.length === 1 ? "" : "s"}`;
                        const keys = `${selected.credentials.length} API key${selected.credentials.length === 1 ? "" : "s"}`;
                        const confirmed = await ask({
                          title: `Delete ${selected.name}?`,
                          body: `Its ${routes} and ${keys} are removed with it. Requests to those aliases start failing at once, and this cannot be undone.`,
                          confirmLabel: "Delete provider",
                          detail: selected.models.length
                            ? selected.models.map((model) => model.public_alias).join("\n")
                            : undefined
                        });
                        if (!confirmed) return;
                        setDeleting(true);
                        try {
                          await api<void>(`/api/admin/providers/${selected.id}`, { method: "DELETE" });
                          complete(`${selected.name} deleted.`);
                        } catch (caught) {
                          notify(errorMessage(caught), "danger");
                        } finally {
                          setDeleting(false);
                        }
                      })();
                    }}
                  ><Trash2 size={14} aria-hidden="true" /> {deleting ? "Deleting…" : "Delete"}</Button>
                </div>
              </div>
              <div className="inspector-stats">
                <span><strong>{selected.models.length}</strong> model route{selected.models.length === 1 ? "" : "s"}</span>
                <span><strong>{selected.credentials.length}</strong> API key{selected.credentials.length === 1 ? "" : "s"}</span>
                <span><strong>{selected.timeout_seconds}s</strong> timeout</span>
                <ProviderCreditStat provider={selected} />
              </div>
              <ProviderCapacityStrip provider={selected} />
              <Rotor
                keys={selected.credentials.map((credential) => ({
                  id: credential.id,
                  status: credentialPoolState(credential)
                }))}
                stalled={selected.credentials.length > 0 && !selected.credentials.some((credential) => credentialPoolState(credential) === "healthy")}
                stalledNote="No key on this provider can serve a request. Open the marked keys below."
              />
              <ResourceDisclosure
                title="Model routes"
                description="Load the provider model catalog, select routes, or add one manually."
                summary={`${selected.models.length} route${selected.models.length === 1 ? "" : "s"}`}
                open={openSection === "models"}
                onToggle={() => setOpenSection((current) => current === "models" ? null : "models")}
                action={(
                  <div className="button-row">
                    <Button variant="quiet" onClick={() => setPanel({ type: "import", providerID: selected.id })}><RefreshCw size={14} aria-hidden="true" /> Load models</Button>
                    <Button variant="quiet" onClick={() => setPanel({ type: "model", providerID: selected.id })}><Plus size={14} aria-hidden="true" /> Add route by hand</Button>
                  </div>
                )}
              >
                {selected.models.length === 0 ? (
                  <p className="inline-empty">No routes yet. Load models, or add one route by hand.</p>
                ) : (
                  <div className="dense-table">
                    {selected.models.map((model) => (
                      <button
                        key={model.id}
                        className="dense-row"
                        aria-label={`Edit route ${model.public_alias}, ${model.enabled ? "on" : "off"}, sent upstream as ${model.upstream_model}`}
                        onClick={() => setPanel({ type: "model", providerID: selected.id, modelID: model.id })}
                      >
                        <StatusDot state={model.enabled ? "healthy" : "disabled"} />
                        <span><code>{model.public_alias}</code><small>→ {model.upstream_model}</small></span>
                        <span>
                          {model.supports_responses ? "Responses native" : "Responses translated"}
                          {model.supports_messages ? " · Messages" : ""}
                          {model.strip_parameters.length > 0 ? ` · removes ${model.strip_parameters.join(", ")}` : ""}
                        </span>
                        <ChevronRight size={14} aria-hidden="true" />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceDisclosure>
              <ResourceDisclosure
                title="API keys"
                description="A primary key is tried first. Without one, healthy keys use balanced round-robin."
                summary={`${selected.credentials.filter((item) => credentialPoolState(item) === "healthy").length}/${selected.credentials.length} keys ready`}
                open={openSection === "credentials"}
                onToggle={() => setOpenSection((current) => current === "credentials" ? null : "credentials")}
                action={<Button variant="quiet" onClick={() => setPanel({ type: "credential", providerID: selected.id })}><Plus size={14} aria-hidden="true" /> Add API keys</Button>}
              >
                {selected.credentials.length === 0 ? (
                  <p className="inline-empty">No API keys yet. Add one to start routing traffic here.</p>
                ) : (
                  <div className="dense-table">
                    {selected.credentials.map((credential) => (
                      <button
                        key={credential.id}
                        className={`dense-row ${credential.validation_error ? "has-warning" : ""}`}
                        aria-label={`Edit API key ${credential.label}, ${credential.validation_error || statusLabel(credentialPoolState(credential))}`}
                        onClick={() => setPanel({ type: "credential", providerID: selected.id, credentialID: credential.id })}
                      >
                        <StatusDot state={credentialPoolState(credential)} />
                        <span>
                          <strong title={credential.label}>{credential.label}</strong>
                          <small title={credential.validation_error ? credential.validation_error : `${credential.is_primary ? "Primary · " : ""}•••• ${credential.secret_suffix}${credentialBalanceNote(credential)}`}>
                            {credential.validation_error
                              ? credential.validation_error
                              : `${credential.is_primary ? "Primary · " : ""}•••• ${credential.secret_suffix}${credentialBalanceNote(credential)}`}
                          </small>
                        </span>
                        <LimitSummary policy={credential.limits} />
                        <ChevronRight size={14} aria-hidden="true" />
                      </button>
                    ))}
                  </div>
                )}
              </ResourceDisclosure>
            </section>
          )}
        </div>
      )}
      {panel?.type === "wizard" && <ProviderWizard onClose={() => setPanel(null)} onComplete={complete} />}
      {/* `missingRecord` gates all four so the panel is never rendered against a
          record that has gone: one render in add-mode with the edit fields still
          filled is enough to turn the next Save into a duplicate. */}
      {panelProvider && !missingRecord && panel?.type === "provider" && <ProviderForm provider={panelProvider} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panelProvider && !missingRecord && panel?.type === "model" && <ModelForm provider={panelProvider} model={panelProvider.models.find((model) => model.id === panel.modelID)} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panelProvider && !missingRecord && panel?.type === "import" && <ModelImportForm provider={panelProvider} onClose={() => setPanel(null)} onComplete={complete} notify={notify} />}
      {panelProvider && !missingRecord && panel?.type === "credential" && (panel.credentialID
        ? <CredentialForm provider={panelProvider} credential={panelProvider.credentials.find((credential) => credential.id === panel.credentialID)} onClose={() => setPanel(null)} onComplete={complete} onRefresh={() => void load()} notify={notify} />
        : <CredentialBatchForm provider={panelProvider} onClose={() => setPanel(null)} onComplete={complete} onRefresh={() => void load()} notify={notify} />)}
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
          <ChevronDown size={15} aria-hidden="true" />
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

/** credentialBalanceNote appends the credit left to a key's row, and returns an
 * empty string for untracked keys so the pool list looks unchanged on installs
 * that do not use balances. */
function credentialBalanceNote(credential: Credential) {
  if (credential.balance_usd === null || credential.balance_usd === undefined) return "";
  const remaining = Math.max(0, credential.balance_usd - credential.balance_spent_usd);
  return remaining <= 0 ? " · out of balance" : ` · ${formatUSD(remaining)} left`;
}

/** The one state name that describes whether the router will reach for this key,
 *  folding together the three separate reasons it might not: the operator turned it
 *  off, its balance is spent, or the upstream put it in cooldown or quarantine.
 *  The rotor and the status dot both need that single answer — a key that is
 *  `healthy` but out of balance is skipped, and drawing it green would be a lie. */
function credentialPoolState(credential: Credential) {
  if (!credential.enabled) return "disabled";
  if (credential.balance_usd != null && credential.balance_usd - credential.balance_spent_usd <= 0) return "exhausted";
  return credential.status;
}

/** A key that cannot serve and will not recover on its own, which is the exact set
 *  the delete button acts on. It reads `credentialPoolState` rather than the raw
 *  fields so the banner's count, the rotor and the status dot can never disagree.
 *
 *  Deliberately not included: a key whose only signal is `validation_error`, which
 *  is also written for a key saved without a successful check or imported from a
 *  config bundle and still routes; and a key in cooldown, which clears itself. */
function isUnusableKey(credential: Credential) {
  const state = credentialPoolState(credential);
  return state === "quarantined" || state === "exhausted";
}

/** unusableKeyReason is the one-line "why" shown beside each key in the confirm
 *  dialog, so the operator reads what is going before agreeing to it. */
function unusableKeyReason(credential: Credential) {
  return credentialPoolState(credential) === "quarantined"
    ? "rejected by the provider"
    : "out of balance";
}

/** ProviderCreditStat reports what is left across the account rather than what was
 * loaded, and stays silent when no key on the provider tracks a balance. Spend the
 * gateway could not pin on one key still comes off the figure, because the operator
 * needs the number they can actually spend. */
function ProviderCreditStat({ provider }: { provider: Provider }) {
  const tracked = provider.credentials.filter((credential) => credential.balance_usd !== null && credential.balance_usd !== undefined);
  if (!tracked.length) return null;
  const remaining = Math.max(0, tracked.reduce(
    (total, credential) => total + Math.max(0, (credential.balance_usd ?? 0) - credential.balance_spent_usd), 0
  ) - safeNumber(provider.balance_spent_usd));
  const exhausted = tracked.filter((credential) => (credential.balance_usd ?? 0) - credential.balance_spent_usd <= 0).length;
  return (
    <span title={exhausted ? `${exhausted} of ${tracked.length} tracked keys are out of balance and are skipped.` : `Across ${tracked.length} tracked key${tracked.length === 1 ? "" : "s"}.`}>
      <strong>{formatUSD(remaining)}</strong> balance left{exhausted ? ` · ${exhausted} key${exhausted === 1 ? "" : "s"} out of balance` : ""}
    </span>
  );
}

const capacityDimensions = ["rps", "rpm", "rpd", "tps", "tpm", "tpd", "tpr"] as const;

/** Spoken names for the seven buckets. "RPS" read aloud is three letters, so each
 *  cell of the capacity grid carries the words instead. */
const dimensionNames: Record<(typeof capacityDimensions)[number], string> = {
  rps: "requests per second",
  rpm: "requests per minute",
  rpd: "requests per day",
  tps: "tokens per second",
  tpm: "tokens per minute",
  tpd: "tokens per day",
  tpr: "tokens per request"
};

function ProviderCapacityStrip({ provider }: { provider: Provider }) {
  const capacity = provider.capacity;
  return (
    <section className="pool-capacity" aria-label={`${provider.name} API key pool capacity`}>
      <header>
        <div>
          <p className="eyebrow">Combined key limits</p>
          <h3>Total provider capacity</h3>
        </div>
        <span>{capacity?.ready_keys ?? 0}/{capacity?.total_keys ?? provider.credentials.length} keys ready</span>
      </header>
      {/* Seven label-and-value pairs, read as a list so the count is announced and
          each cell states its bucket in words rather than as three letters. */}
      <div className="pool-capacity__limits" role="list">
        {capacityDimensions.map((dimension) => {
          const limit = capacity?.limits?.[dimension];
          const reading = limitReading(limit);
          return (
            <div
              key={dimension}
              role="listitem"
              /* The figure ellipsises inside a seventh of the strip, so the full
                 reading stays reachable by pointer as well as by screen reader. */
              title={`${dimension.toUpperCase()}: ${reading.sentence}`}
              aria-label={`${dimensionNames[dimension]}: ${reading.sentence}`}
            >
              <span aria-hidden="true">{dimension.toUpperCase()}</span>
              <strong aria-hidden="true">{reading.compact}</strong>
              <small aria-hidden="true">{dimension === "tpr" ? "max / request" : limit?.unlimited ? "unlimited" : "remaining / total"}</small>
            </div>
          );
        })}
      </div>
      <p>Shared limits from every ready API key are combined. Usage lowers remaining capacity; adding or removing a key recalculates the total.</p>
    </section>
  );
}

// ProviderPowerButton turns a provider's traffic off without opening the edit
// form. Disabling is the safe response to a broken upstream, so it must not
// depend on the rest of the form validating or on the base URL re-check.
function ProviderPowerButton({
  provider,
  onDone,
  notify
}: {
  provider: Provider;
  onDone: (message: string) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const turningOff = provider.enabled;

  const apply = async () => {
    setBusy(true);
    try {
      const result = await api<ProviderStateResult>(`/api/admin/providers/${provider.id}/state`, {
        method: "PUT",
        json: { enabled: !turningOff }
      });
      // The warnings describe live traffic that just stopped, so they are shown
      // as the notification rather than folded into a success line.
      if (result.warnings.length > 0) {
        result.warnings.forEach((warning) => notify(warning, "danger"));
        onDone(`${provider.name} turned off.`);
        return;
      }
      onDone(turningOff
        ? `${provider.name} turned off. Its routes stop receiving traffic.`
        : `${provider.name} turned on.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  return (
    <Button
      variant="quiet"
      disabled={busy}
      aria-label={`${turningOff ? "Turn off" : "Turn on"} provider ${provider.name}`}
      onClick={() => {
        void (async () => {
          // Turning off stops real traffic, so it is confirmed; turning on only
          // adds capacity and needs no prompt.
          if (turningOff) {
            const confirmed = await ask({
              title: `Turn off ${provider.name}?`,
              body: "Its model routes stop receiving traffic until it is turned back on. Nothing is deleted.",
              confirmLabel: "Turn off provider"
            });
            if (!confirmed) return;
          }
          await apply();
        })();
      }}
    >
      <Power size={14} aria-hidden="true" /> {busy ? "Saving…" : turningOff ? "Turn off" : "Turn on"}
    </Button>
  );
}

/** UnusableKeysNotice is the provider inspector's account of keys that cannot
 *  serve, and the one place they can be cleared.
 *
 *  It splits two things the old banner ran together. A quarantined or spent key is
 *  not carrying traffic and there is an action to take, so it is stated plainly and
 *  offered a delete. A key that merely carries a validation note still routes, so it
 *  gets a quieter line: wording that as failure taught the operator to ignore both. */
function UnusableKeysNotice({
  provider,
  notify,
  onDone
}: {
  provider: Provider;
  notify: (message: string, tone?: "success" | "danger") => void;
  onDone: () => void;
}) {
  const ask = useConfirm();
  // Its own flag, not the inspector's provider-level `deleting`: the two buttons
  // sit side by side and sharing one would disable the wrong control.
  const [deletingUnusable, setDeletingUnusable] = useState(false);
  const unusable = provider.credentials.filter(isUnusableKey);
  // A key that is both quarantined and merely noted is already counted above, so
  // this line only speaks for keys whose sole signal is the note.
  const noted = provider.credentials.filter(
    (credential) => credential.validation_error && !isUnusableKey(credential)
  );
  if (unusable.length === 0 && noted.length === 0) return null;
  const many = unusable.length !== 1;

  const remove = async () => {
    const confirmed = await ask({
      title: `Delete ${unusable.length} unusable API key${many ? "s" : ""}?`,
      body: `${many ? "They are" : "It is"} removed from ${provider.name} permanently. `
        + `${many ? "Their" : "Its"} spend history stays in the request log, and this cannot be undone.`,
      confirmLabel: `Delete ${unusable.length} key${many ? "s" : ""}`,
      detail: unusable.map((credential) => `${credential.label} · ${unusableKeyReason(credential)}`).join("\n")
    });
    if (!confirmed) return;
    setDeletingUnusable(true);
    try {
      const result = await api<{
        deleted: { id: string; label: string }[];
        skipped: { id: string; label: string; reason: string }[];
        remaining: number;
      }>(`/api/admin/providers/${provider.id}/credentials/delete-unusable`, {
        method: "POST",
        json: { credential_ids: unusable.map((credential) => credential.id) }
      });
      // Partial outcomes are the normal case here — a key pinned to an Anthropic
      // file cannot be deleted — so the toast reports what was kept and why.
      const kept = result.skipped.length
        ? ` · ${result.skipped.length} kept: ${result.skipped.map((entry) => entry.reason).join("; ")}.`
        : "";
      const empty = result.remaining === 0
        ? " This provider now has no API key, so its routes answer 503 until you add one."
        : "";
      notify(
        `${result.deleted.length} API key${result.deleted.length === 1 ? "" : "s"} deleted.${kept}${empty}`,
        result.skipped.length || result.remaining === 0 ? "danger" : "success"
      );
      onDone();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setDeletingUnusable(false);
    }
  };

  return (
    <>
      {unusable.length > 0 && (
        <InlineNotice tone="danger">
          <span className="notice-line">
            <span>
              {unusable.length} API key{many ? "s" : ""} cannot serve requests.
              {" "}Replace {many ? "them" : "it"}, or delete {many ? "them" : "it"} here.
            </span>
            <Button
              variant="danger"
              disabled={deletingUnusable}
              aria-label={`Delete ${unusable.length} unusable API key${many ? "s" : ""} on ${provider.name}`}
              onClick={() => void remove()}
            >
              <Trash2 size={14} aria-hidden="true" />
              {deletingUnusable ? "Deleting…" : `Delete ${unusable.length} unusable key${many ? "s" : ""}`}
            </Button>
          </span>
        </InlineNotice>
      )}
      {noted.length > 0 && (
        <InlineNotice tone="warning">
          {noted.length === 1
            ? `1 API key was saved without a successful check. It still receives traffic — open it to check it again.`
            : `${noted.length} API keys were saved without a successful check. They still receive traffic — open each one to check it again.`}
        </InlineNotice>
      )}
    </>
  );
}

async function testProvider(provider: Pick<Provider, "id">, notify: (message: string, tone?: "success" | "danger") => void) {
  try {
    const result = await api<{ ok: boolean; valid: number; total: number }>(`/api/admin/providers/${provider.id}/test`, { method: "POST" });
    notify(
      result.ok ? `${result.valid} of ${result.total} API keys passed the check.` : `${result.total - result.valid} of ${result.total} API keys need attention.`,
      result.ok ? "success" : "danger"
    );
  } catch (caught) {
    notify(errorMessage(caught), "danger");
  }
}

function ProviderWizard({ onClose, onComplete }: { onClose: () => void; onComplete: (message: string) => void }) {
  const [step, setStep] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [provider, setProvider] = useState<ProviderDraft>({
    name: "", base_url: "", auth_header: "Authorization", auth_scheme: "Bearer",
    api_format: "openai", anthropic_version: "2023-06-01",
    timeout_seconds: 120, enabled: true, allow_private_network: false, extra_headers: {},
    default_key_balance: "", apply_balance_to_existing_keys: false
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
    // The prefill only fills a still-untouched timeout, so a reply that lands
    // after the sheet closes — or after the operator started typing — is dropped.
    let ignore = false;
    void api<Settings>("/api/admin/settings")
      .then((settings) => {
        if (ignore) return;
        setProvider((current) => (
          current.name || current.base_url ? current : { ...current, timeout_seconds: settings.default_provider_timeout_seconds }
        ));
      })
      .catch(() => undefined);
    return () => { ignore = true; };
  }, []);

  // Four steps of typing sit behind this panel, so a stray backdrop click or an
  // Escape keypress asks before throwing it away.
  const dirty = Boolean(
    provider.name.trim()
    || provider.base_url.trim()
    || credentialDrafts.some((credential) => credential.label.trim() || credential.secret.trim())
    || Object.keys(selectedModels).length > 0
  );

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
          json: { provider: providerPayload(provider), secret: credential.secret }
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
        setError(`${credentials[invalid].label}: ${inspections[invalid].warning || keyCheckFailed}`);
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
      const created = await api<{ id: string }>("/api/admin/providers", { method: "POST", json: providerPayload(provider) });
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
        // The provider was created and something after it failed, so the half-built
        // provider is removed. If that removal also fails there is a provider on the
        // list with no keys, and saying so is the difference between the operator
        // deleting it and wondering where it came from.
        try {
          await api(`/api/admin/providers/${createdID}`, { method: "DELETE" });
        } catch {
          setError(
            `${errorMessage(caught)} The provider was created before this failed and could not be removed — ${provider.name.trim() || "it"} is in the provider list with no API keys. Delete it there, or open it and finish the setup.`
          );
          return;
        }
      }
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet
      title="Add provider"
      eyebrow={`Step ${step + 1} of ${steps.length}`}
      onClose={onClose}
      wide
      dirty={dirty}
      discardMessage="Close this panel? The provider, API keys and model choices entered here are not saved yet."
    >
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
          <div><span>Model routes</span><strong>{Object.keys(selectedModels).length}</strong><small>{discoveredModels.length} discovered from the provider.</small></div>
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
              // The Foundry preset fills in the shape of the base URL and leaves
              // the resource name to the operator. Carrying the template forward
              // would spend the whole key step failing to reach a host that does
              // not exist.
              if (provider.base_url.includes(FOUNDRY_RESOURCE_PLACEHOLDER)) {
                setError(`Replace ${FOUNDRY_RESOURCE_PLACEHOLDER} in the base URL with your Foundry resource name.`);
                return;
              }
              // The balance field says what is wrong with it in place; the step
              // guard has to agree, or Continue carries an unusable figure into the
              // key step and the wizard fails at the very end instead.
              if (balanceInvalid(provider.default_key_balance)) {
                setError(providerBalanceError(provider.default_key_balance));
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
          }}>{busy ? "Checking API keys…" : step === 1 ? "Check keys and load models" : "Continue"}</Button>
        ) : (
          /* The action that opens this wizard is called "Add provider" in both
             places it appears, so the button that completes it says the same thing.
             It used to say "Create provider", which reads as a different action from
             the one the operator started. */
          <Button onClick={() => void finish()} disabled={busy}>{busy ? "Adding provider…" : "Add provider"}</Button>
        )}
      </div>
    </Sheet>
  );
}

type ProviderDraft = {
  name: string; base_url: string; auth_header: string; auth_scheme: string;
  api_format: "openai" | "anthropic"; anthropic_version: string;
  timeout_seconds: number; enabled: boolean; allow_private_network: boolean; extra_headers: Record<string, string>;
  /** Held as text so a blank field stays distinguishable from a zero balance:
   * blank means "do not track", 0 means "nothing left". The API takes a number or
   * null, so this is converted at submit time. */
  default_key_balance: string;
  apply_balance_to_existing_keys: boolean;
};

/** providerPayload converts a draft into the JSON the admin API expects. The
 * balance field crosses as null when blank, which is what tells the server to
 * stop seeding a figure onto new keys. */
function providerPayload(draft: ProviderDraft) {
  const { default_key_balance, apply_balance_to_existing_keys, ...rest } = draft;
  const trimmed = default_key_balance.trim();
  return {
    ...rest,
    default_key_balance_usd: trimmed === "" ? null : Number(trimmed),
    apply_balance_to_existing_keys: apply_balance_to_existing_keys && trimmed !== ""
  };
}

/** providerDraftFrom seeds the edit form from a saved provider. An absent balance
 * becomes a blank field, which is the same thing the operator typed to opt out of
 * tracking it. */
function providerDraftFrom(provider: Provider): ProviderDraft {
  return {
    name: provider.name, base_url: provider.base_url,
    auth_header: provider.auth_header, auth_scheme: provider.auth_scheme,
    api_format: provider.api_format ?? "openai", anthropic_version: provider.anthropic_version ?? "2023-06-01",
    timeout_seconds: provider.timeout_seconds, enabled: provider.enabled,
    allow_private_network: provider.allow_private_network, extra_headers: provider.extra_headers,
    default_key_balance: provider.default_key_balance_usd == null ? "" : String(provider.default_key_balance_usd),
    apply_balance_to_existing_keys: false
  };
}

/** balanceInvalid reports whether a typed balance is unusable. A blank field is
 * valid and means the balance is not tracked, which is why this cannot simply
 * check Number.isFinite. */
function balanceInvalid(value: string) {
  const trimmed = value.trim();
  if (trimmed === "") return false;
  const parsed = Number(trimmed);
  return !Number.isFinite(parsed) || parsed < 0;
}

/** providerBalanceError states what is wrong with a typed balance, or an empty
 * string when there is nothing to say. */
function providerBalanceError(value: string) {
  return balanceInvalid(value)
    ? "Enter the per-key balance as a positive USD amount, or leave it blank to stop tracking it."
    : "";
}

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

const FOUNDRY_HOST_SUFFIX = ".services.ai.azure.com";
const FOUNDRY_RESOURCE_PLACEHOLDER = "YOUR-RESOURCE";
const FOUNDRY_CLAUDE_BASE_URL = `https://${FOUNDRY_RESOURCE_PLACEHOLDER}${FOUNDRY_HOST_SUFFIX}/anthropic/v1`;

/** foundryAddressSuggestion reads an Azure Foundry address and reports the base
 * URL and protocol that resource actually serves. The portal shows a "Target
 * URI" naming one endpoint and carrying an ?api-version= query; saved as a base
 * URL it appends a second /messages and every request 404s. Returns null for an
 * address that is not Foundry's, or is already right. */
function foundryAddressSuggestion(baseURL: string, apiFormat: ProviderDraft["api_format"]) {
  const typed = baseURL.trim();
  if (typed.includes(FOUNDRY_RESOURCE_PLACEHOLDER)) return null;
  let parsed: URL;
  try {
    parsed = new URL(typed);
  } catch {
    // The regular URL validation remains responsible for incomplete input.
    return null;
  }
  if (!parsed.hostname.toLowerCase().endsWith(FOUNDRY_HOST_SUFFIX)) return null;
  const path = parsed.pathname.replace(/\/+$/, "").toLowerCase();
  // A bare host says nothing about which family the operator wants, so the
  // protocol already chosen decides; a path that names one overrules it.
  const claude = path === "" ? apiFormat === "anthropic" : path.startsWith("/anthropic");
  if (path !== "" && !claude && !path.startsWith("/openai")) return null;
  const api_format: ProviderDraft["api_format"] = claude ? "anthropic" : "openai";
  const base_url = `${parsed.origin}${claude ? "/anthropic/v1" : "/openai/v1"}`;
  if (base_url.toLowerCase() === typed.toLowerCase() && api_format === apiFormat) return null;
  return { base_url, api_format, protocolChanged: api_format !== apiFormat };
}

/** applyFoundryAddress accepts the suggestion. The authentication fields move
 * only when the protocol does, so a header the operator typed for a working
 * provider survives a base URL correction. */
function applyFoundryAddress(value: ProviderDraft, suggestion: { base_url: string; api_format: ProviderDraft["api_format"] }): ProviderDraft {
  if (suggestion.api_format === value.api_format) return { ...value, base_url: suggestion.base_url };
  return {
    ...value,
    base_url: suggestion.base_url,
    api_format: suggestion.api_format,
    auth_header: suggestion.api_format === "anthropic" ? "X-Api-Key" : "Authorization",
    auth_scheme: suggestion.api_format === "anthropic" ? "" : "Bearer",
    anthropic_version: value.anthropic_version || "2023-06-01"
  };
}

/** foundryHasNoResourceAPIs reports a provider that publishes only Messages.
 * Foundry serves no Files or Batches endpoint, so naming one as the default
 * Anthropic resource provider fails every upload. The backend refuses the save;
 * this greys the option out before the operator gets that far. */
function foundryHasNoResourceAPIs(provider: Provider) {
  try {
    return new URL(provider.base_url).hostname.toLowerCase().endsWith(FOUNDRY_HOST_SUFFIX);
  } catch {
    return false;
  }
}

function ProviderFields({ value, onChange, existingKeys }: { value: ProviderDraft; onChange: (value: ProviderDraft) => void; existingKeys?: number }) {
  const geminiSuggestion = geminiCompatibilitySuggestion(value.base_url, value.api_format);
  const foundrySuggestion = foundryAddressSuggestion(value.base_url, value.api_format);
  const balanceError = providerBalanceError(value.default_key_balance);
  const balanceErrorID = useId();
  const useOfficialPreset = (kind: "openai" | "anthropic") => onChange({
    ...value,
    name: value.name.trim() || (kind === "openai" ? "OpenAI" : "Anthropic"),
    base_url: kind === "openai" ? "https://api.openai.com/v1" : "https://api.anthropic.com/v1",
    api_format: kind,
    auth_header: kind === "openai" ? "Authorization" : "X-Api-Key",
    auth_scheme: kind === "openai" ? "Bearer" : "",
    anthropic_version: value.anthropic_version || "2023-06-01"
  });
  // Foundry serves Claude through Anthropic's own Messages API, on a base URL
  // built from the resource name. A resource already typed is kept and cleaned;
  // otherwise the field carries the shape, with the name left to fill in.
  const useFoundryPreset = () => {
    let base_url = FOUNDRY_CLAUDE_BASE_URL;
    try {
      const parsed = new URL(value.base_url.trim());
      if (parsed.hostname.toLowerCase().endsWith(FOUNDRY_HOST_SUFFIX)) base_url = `${parsed.origin}/anthropic/v1`;
    } catch {
      // An address that is not a URL yet simply gets the template.
    }
    onChange({
      ...value,
      name: value.name.trim() || "Foundry Claude",
      base_url,
      api_format: "anthropic",
      auth_header: "X-Api-Key",
      auth_scheme: "",
      anthropic_version: value.anthropic_version || "2023-06-01"
    });
  };
  return (
    <div className="form-stack">
      <div className="button-row">
        <span className="muted">Official provider setup</span>
        <Button type="button" variant="quiet" onClick={() => useOfficialPreset("openai")}>Use OpenAI</Button>
        <Button type="button" variant="quiet" onClick={() => useOfficialPreset("anthropic")}>Use Anthropic</Button>
        <Button type="button" variant="quiet" onClick={useFoundryPreset}>Use Foundry Claude</Button>
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
      {foundrySuggestion && <InlineNotice>
        Azure Foundry address detected. This resource serves its deployments from <code>{foundrySuggestion.base_url}</code>
        {foundrySuggestion.protocolChanged && `, through the ${foundrySuggestion.api_format === "anthropic" ? "Anthropic" : "OpenAI"}-compatible protocol`}.{" "}
        <button type="button" className="button button--quiet" onClick={() => onChange(applyFoundryAddress(value, foundrySuggestion))}>
          {foundrySuggestion.protocolChanged ? "Use Foundry base URL and protocol" : "Use Foundry base URL"}
        </button>
      </InlineNotice>}
      {value.base_url.includes(FOUNDRY_RESOURCE_PLACEHOLDER) && <InlineNotice tone="warning">
        Replace <code>{FOUNDRY_RESOURCE_PLACEHOLDER}</code> with your Foundry resource name — the first label of the Target URI the Azure portal shows beside the deployment.
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
      <fieldset>
        <legend>Balance per API key</legend>
        <p className="fieldset-note">
          Type the credit sitting on one key of this account and every key added here starts with that
          figure. Rotakey subtracts the estimated cost of each request it serves and shows what is left.
          Leave it blank to not track balances on this provider.
        </p>
        <label className="field">
          <span>Balance per key <small>USD</small></span>
          <input
            type="number" min={0} step="0.01" inputMode="decimal" placeholder="Not tracked"
            value={value.default_key_balance}
            aria-invalid={balanceError ? true : undefined}
            /* aria-invalid alone says a field is wrong without saying why, so the
               notice below is bound to the input as its description. */
            aria-describedby={balanceError ? balanceErrorID : undefined}
            onChange={(event) => onChange({ ...value, default_key_balance: event.target.value })}
          />
        </label>
        {balanceError && <div id={balanceErrorID}><InlineNotice tone="danger">{balanceError}</InlineNotice></div>}
        {existingKeys !== undefined && existingKeys > 0 && (
          /* Applying the figure to every key needs a figure. The toggle used to be
             live-looking but inert while the field above was blank — clicking it did
             nothing and said nothing. Now it is disabled, and its own description
             says what to do to make it available. */
          <Toggle
            checked={value.apply_balance_to_existing_keys && value.default_key_balance.trim() !== ""}
            disabled={value.default_key_balance.trim() === ""}
            onChange={(apply_balance_to_existing_keys) => onChange({ ...value, apply_balance_to_existing_keys })}
            label={`Apply to all ${existingKeys} existing key${existingKeys === 1 ? "" : "s"}`}
            description={value.default_key_balance.trim() === ""
              ? "Enter a balance per key above to use this."
              : "Records a top-up across the whole account: every key's balance becomes this figure and its recorded spend is cleared."}
          />
        )}
      </fieldset>
      <Toggle checked={value.enabled} onChange={(enabled) => onChange({ ...value, enabled })} label="Provider is on" description="A provider that is off is never considered for routing." />
      <Toggle checked={value.allow_private_network} onChange={(allow_private_network) => onChange({ ...value, allow_private_network })} label="Allow private-network target" description="Also permits HTTP. Enable only for a provider you operate on this VPS or LAN." />
    </div>
  );
}

function ProviderForm({ provider, onClose, onComplete, notify }: { provider: Provider; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  // The panel reads its provider out of the list that reloads every ten seconds,
  // so recomputing the baseline on every render made an unrelated upstream change
  // — a health check, a request counter — read as unsaved local edits: the discard
  // prompt fired on a form nobody had typed in, and saving pushed the older
  // snapshot back over the newer values. The baseline is the provider as it stood
  // when the panel opened, and it only moves when the panel switches resource.
  const baseline = useRef<{ id: string; draft: ProviderDraft } | null>(null);
  if (baseline.current?.id !== provider.id) {
    baseline.current = { id: provider.id, draft: providerDraftFrom(provider) };
  }
  const saved = baseline.current.draft;
  const [draft, setDraft] = useState<ProviderDraft>(saved);
  const [busy, setBusy] = useState(false);
  const dirty = JSON.stringify(draft) !== JSON.stringify(saved);
  return (
    <Sheet
      title={`Edit ${provider.name}`}
      eyebrow="Provider settings"
      onClose={onClose}
      dirty={dirty}
      discardMessage="Close this panel? The provider changes here are not saved yet."
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          // The balance field renders its own bound error, but nothing stopped the
          // submit: a typed "-5" was sent, and the server's rejection came back as
          // a toast that said less than the message already on screen.
          if (balanceInvalid(draft.default_key_balance)) {
            notify(providerBalanceError(draft.default_key_balance), "danger");
            return;
          }
          setBusy(true);
          void api(`/api/admin/providers/${provider.id}`, { method: "PUT", json: providerPayload(draft) })
            .then(() => onComplete(`${draft.name} updated.`))
            .catch((caught) => notify(errorMessage(caught), "danger"))
            .finally(() => setBusy(false));
        }}
      >
        <ProviderFields value={draft} onChange={setDraft} existingKeys={provider.credentials.length} />
        <div className="sheet-actions"><span /><Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save provider"}</Button></div>
      </form>
    </Sheet>
  );
}

type ModelDraft = Omit<ModelRoute, "id" | "provider_id" | "created_at" | "updated_at" | "capability_status" | "capability_profile" | "capabilities_checked_at" | "capability_error"> & { manual?: boolean };

function modelToDraft(model: ModelRoute): ModelDraft {
  return {
    public_alias: model.public_alias,
    upstream_model: model.upstream_model,
    supports_chat: model.supports_chat,
    supports_responses: model.supports_responses,
    supports_messages: model.supports_messages,
    default_max_output_tokens: model.default_max_output_tokens,
    tokenizer: model.tokenizer,
    input_cost_per_million_usd: model.input_cost_per_million_usd,
    output_cost_per_million_usd: model.output_cost_per_million_usd,
    request_cost_usd: model.request_cost_usd,
    capture_bodies: model.capture_bodies,
    strip_parameters: model.strip_parameters ?? [],
    enabled: model.enabled
  };
}

function ModelFields({ value, onChange }: { value: ModelDraft; onChange: (value: ModelDraft) => void }) {
  return (
    <div className="form-stack">
      <label className="field"><span>Public alias <small>Applications put this in the model field</small></span><input required placeholder="groq/llama-3.3-70b" value={value.public_alias} onChange={(e) => onChange({ ...value, public_alias: e.target.value })} /></label>
      <label className="field"><span>Upstream model ID <small>On Azure and Foundry this is the deployment name, not the vendor's name for the model</small></span><input required placeholder="llama-3.3-70b-versatile" value={value.upstream_model} onChange={(e) => onChange({ ...value, upstream_model: e.target.value })} /></label>
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
  const ask = useConfirm();
  const routingMode = useRoutingMode();
  const [draft, setDraft] = useState<ModelDraft>(model ? modelToDraft(model) : {
    public_alias: `${provider.slug}/`, upstream_model: "", supports_chat: true,
    supports_responses: false, supports_messages: true, default_max_output_tokens: 1024,
    tokenizer: "heuristic", input_cost_per_million_usd: 0, output_cost_per_million_usd: 0, request_cost_usd: undefined,
    capture_bodies: false, strip_parameters: [], enabled: true
  });
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const initial = useRef(JSON.stringify(draft));
  const dirty = JSON.stringify(draft) !== initial.current;
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
      onComplete(model ? `Route ${draft.public_alias} updated.` : `Route ${draft.public_alias} created.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };
  return (
    <Sheet
      title={model ? "Edit model route" : "Add model route"}
      eyebrow={provider.name}
      onClose={onClose}
      dirty={dirty}
      discardMessage="Close this panel? The route details here are not saved yet."
      actions={model ? <Button variant="danger" disabled={deleting} onClick={() => {
        void (async () => {
          if (!(await ask(deleteRouteQuestion(model.public_alias)))) return;
          setDeleting(true);
          try {
            await api(`/api/admin/models/${model.id}`, { method: "DELETE" });
            onComplete(`Route ${model.public_alias} deleted.`);
          } catch (caught) {
            notify(errorMessage(caught), "danger");
          } finally {
            setDeleting(false);
          }
        })();
      }}><Trash2 size={14} aria-hidden="true" /> {deleting ? "Deleting…" : "Delete route"}</Button> : undefined}
    >
      <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
        <ModelFields value={draft} onChange={setDraft} />
        <div className="sheet-actions">
          <span />
          <Button type="submit" disabled={busy}>{busy ? "Saving…" : model ? "Save route" : "Create route"}</Button>
        </div>
      </form>
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

type BatchCredentialFailure = {
  label: string;
  secret: string;
  error: string;
  statusCode: number;
};

function CredentialBatchForm({ provider, onClose, onComplete, onRefresh, notify }: {
  provider: Provider;
  onClose: () => void;
  onComplete: (message: string) => void;
  onRefresh: () => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const [keyText, setKeyText] = useState("");
  const [checkProtocol, setCheckProtocol] = useState(true);
  const [firstIsPrimary, setFirstIsPrimary] = useState(false);
  const [limits, setLimits] = useState<RatePolicy>(emptyPolicy);
  const [balance, setBalance] = useState(
    provider.default_key_balance_usd === null || provider.default_key_balance_usd === undefined
      ? ""
      : String(provider.default_key_balance_usd)
  );
  const [failures, setFailures] = useState<BatchCredentialFailure[]>([]);
  const [savedCount, setSavedCount] = useState(0);
  const [duplicateCount, setDuplicateCount] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const pastedSecrets = uniqueKeyLines(keyText);
  const dirty = Boolean(keyText.trim())
    || !checkProtocol
    || firstIsPrimary
    || JSON.stringify(limits) !== JSON.stringify(emptyPolicy())
    || balance !== (provider.default_key_balance_usd === null || provider.default_key_balance_usd === undefined
      ? ""
      : String(provider.default_key_balance_usd));

  const clearResults = () => {
    setFailures([]);
    setSavedCount(0);
    setDuplicateCount(0);
    setError("");
  };

  const save = async () => {
    if (pastedSecrets.length === 0) {
      setError("Paste at least one API key, with one key on each line.");
      return;
    }
    if (pastedSecrets.length > 100) {
      setError("Add no more than 100 API keys at once.");
      return;
    }
    if (balanceInvalid(balance)) {
      setError("Enter a valid non-negative USD balance for each key, or leave it blank.");
      return;
    }
    const tooShort = pastedSecrets.filter((secret) => secret.length < 8);
    const candidates = pastedSecrets.filter((secret) => secret.length >= 8);
    if (candidates.length === 0) {
      setFailures(tooShort.map((secret, index) => ({
        label: `Key ${index + 1}`, secret, error: "This API key is shorter than 8 characters.", statusCode: 0
      })));
      setError("None of the pasted lines contains a complete API key.");
      return;
    }

    const entries = automaticCredentialEntries(
      provider,
      candidates,
      new Map(failures.map((failure) => [failure.secret, failure.label]))
    );
    const trimmedBalance = balance.trim();
    const parsedBalance = trimmedBalance === "" ? null : Number(trimmedBalance);
    setBusy(true);
    setError("");
    setFailures([]);
    setSavedCount(0);
    setDuplicateCount(Math.max(0, keyText.split(/\r?\n/).map((line) => line.trim()).filter(Boolean).length - pastedSecrets.length));
    try {
      const credentials = entries.map((entry, index) => ({
        label: entry.label,
        secret: entry.secret,
        is_primary: firstIsPrimary && index === 0,
        enabled: true,
        limits,
        balance_usd: parsedBalance,
        skip_protocol_check: !checkProtocol
      }));
      const result = await api<{
        saved: { id: string; label: string; models: number; protocol_verified: boolean }[];
        failed: { label: string; error: string; status_code: number }[];
      }>(`/api/admin/providers/${provider.id}/credentials`, {
        method: "POST",
        json: { credentials, save_valid_only: true }
      });
      const byLabel = new Map(entries.map((entry) => [entry.label, entry.secret]));
      const rejected = result.failed.map((failure) => ({
        label: failure.label,
        secret: byLabel.get(failure.label) ?? "",
        error: failure.error || keyCheckFailed,
        statusCode: failure.status_code || 0
      }));
      rejected.push(...tooShort.map((secret, index) => ({
        label: `Invalid line ${index + 1}`, secret, error: "This API key is shorter than 8 characters.", statusCode: 0
      })));

      setSavedCount(result.saved.length);
      setFailures(rejected);
      setKeyText(rejected.map((failure) => failure.secret).filter(Boolean).join("\n"));
      if (result.saved.length > 0) onRefresh();
      if (rejected.length === 0) {
        onComplete(`${result.saved.length} API key${result.saved.length === 1 ? "" : "s"} verified, saved, and added to ${provider.models.length} existing model route${provider.models.length === 1 ? "" : "s"}.`);
      } else if (result.saved.length > 0) {
        notify(`${result.saved.length} valid API key${result.saved.length === 1 ? " was" : "s were"} saved. ${rejected.length} failed and remain here for retry or removal.`, "danger");
      } else {
        setError(`All ${rejected.length} API keys failed verification. Review or remove them below.`);
      }
    } catch (caught) {
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet
      title="Add API keys"
      eyebrow={provider.name}
      onClose={onClose}
      wide
      dirty={dirty}
      discardMessage="Close this panel? The API keys and limits typed here are not saved yet."
    >
      <form onSubmit={(event) => { event.preventDefault(); void save(); }}>
        {error && <InlineNotice tone="danger">{error}</InlineNotice>}
        {savedCount > 0 && <InlineNotice tone="success">{savedCount} valid API key{savedCount === 1 ? " was" : "s were"} saved and joined this provider's existing model routes.</InlineNotice>}
        {duplicateCount > 0 && <InlineNotice>{duplicateCount} duplicate pasted line{duplicateCount === 1 ? " was" : "s were"} ignored.</InlineNotice>}
        <label className="field">
          <span>API keys <small>One key per line</small></span>
          <textarea
            required
            rows={10}
            spellCheck={false}
            autoComplete="off"
            placeholder={`sk-key-one\nsk-key-two\nsk-key-three`}
            value={keyText}
            onChange={(event) => { setKeyText(event.target.value); clearResults(); }}
          />
          <small>Paste the whole list once. Labels are generated automatically; blank and duplicate lines are ignored.</small>
        </label>
        <Toggle
          checked={firstIsPrimary}
          onChange={setFirstIsPrimary}
          label="Use first valid key as primary"
          description="Optional. If the first line fails verification, the provider's current primary key is left unchanged."
        />
        <Toggle
          checked={checkProtocol}
          onChange={(value) => { setCheckProtocol(value); clearResults(); }}
          label="Check inference protocol"
          description="Turn this off when the model catalog works but the provider blocks protocol probes. Every line is still verified through the model catalog."
        />
        <div className="validation-action">
          <div>
            <strong>Verify and save all valid keys</strong>
            <small>One failed key does not block the others. Saved keys immediately join all {provider.models.length} existing model routes.</small>
          </div>
          <code>{pastedSecrets.length} key{pastedSecrets.length === 1 ? "" : "s"}</code>
        </div>
        {failures.length > 0 && (
          <div className="credential-entry-list" aria-label="API keys that failed verification">
            <div className="credential-entry-list__intro"><div><strong>Failed API keys</strong><small>These were not saved. Remove them or leave them here and retry.</small></div></div>
            {failures.map((failure) => (
              <section className="credential-entry" key={`${failure.label}-${failure.secret}`}>
                <header>
                  <span><strong>{failure.label}</strong><small>{maskedSecret(failure.secret)}{failure.statusCode ? ` · HTTP ${failure.statusCode}` : ""}</small></span>
                  <Button
                    type="button"
                    variant="quiet"
                    onClick={() => {
                      const remaining = failures.filter((item) => item !== failure);
                      setFailures(remaining);
                      setKeyText(remaining.map((item) => item.secret).filter(Boolean).join("\n"));
                      if (remaining.length === 0) setError("");
                    }}
                  ><Trash2 size={13} aria-hidden="true" /> Remove</Button>
                </header>
                <InlineNotice tone="danger">{failure.error}</InlineNotice>
              </section>
            ))}
          </div>
        )}
        <fieldset><legend>Shared limits for these API keys</legend><p className="fieldset-note">The same limits are applied separately to every new key. Blank means no limit.</p><RateFields value={limits} onChange={setLimits} /></fieldset>
        <fieldset>
          <legend>Balance for each new API key</legend>
          <p className="fieldset-note">Optional. This amount is assigned to every key in this batch. Leave it blank to keep their balances untracked.</p>
          <label className="field">
            <span>Balance <small>USD per API key</small></span>
            <input type="number" min="0" step="0.000001" placeholder="Not tracked" value={balance} onChange={(event) => setBalance(event.target.value)} />
          </label>
        </fieldset>
        <div className="sheet-actions">
          <span />
          <Button type="submit" disabled={busy}>{busy ? "Verifying and saving…" : `Verify and add ${pastedSecrets.length || "all"} key${pastedSecrets.length === 1 ? "" : "s"}`}</Button>
        </div>
      </form>
    </Sheet>
  );
}

function CredentialForm({ provider, credential, onClose, onComplete, onRefresh, notify }: { provider: Provider; credential?: Credential; onClose: () => void; onComplete: (message: string) => void; onRefresh: () => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const ask = useConfirm();
  const [label, setLabel] = useState(credential?.label ?? "");
  const [secret, setSecret] = useState("");
  const [isPrimary, setIsPrimary] = useState(credential?.is_primary ?? false);
  const [enabled, setEnabled] = useState(credential?.enabled ?? true);
  const [checkProtocol, setCheckProtocol] = useState(true);
  const [limits, setLimits] = useState<RatePolicy>(credential?.limits ?? emptyPolicy());
  // The balance is held as the raw text so an empty field stays distinguishable
  // from a zero balance: empty means "do not track", 0 means "nothing left".
  const [balance, setBalance] = useState(
    credential?.balance_usd === null || credential?.balance_usd === undefined ? "" : String(credential.balance_usd)
  );
  const [resetSpend, setResetSpend] = useState(false);
  const [inspection, setInspection] = useState<CredentialInspection | null>(null);
  const [checkedSecret, setCheckedSecret] = useState("");
  const [selectedModels, setSelectedModels] = useState<Record<string, string>>({});
  const [selectedModel, setSelectedModel] = useState(provider.models[0]?.id ?? "");
  const [modelLimits, setModelLimits] = useState<RatePolicy>(() => credential?.model_limits[provider.models[0]?.id] ?? emptyPolicy());
  const [busy, setBusy] = useState(false);
  // Deleting has its own flag rather than sharing `busy` with save: the button used
  // to grey out with its label unchanged, so on a slow link the operator could not
  // tell whether the request had gone out.
  const [deleting, setDeleting] = useState(false);
  const [labelTouched, setLabelTouched] = useState(false);
  const [secretTouched, setSecretTouched] = useState(false);
  // Saving the model limit is its own request, separate from the panel's Save.
  // Both buttons used to call onComplete, which closes the panel — so saving a
  // model limit threw away every other edit in the form without saying so.
  const [limitBusy, setLimitBusy] = useState<"save" | "clear" | null>(null);
  const fieldID = useId();

  useEffect(() => {
    setModelLimits(credential?.model_limits[selectedModel] ?? emptyPolicy());
  }, [selectedModel, credential?.id]);

  const labelInvalid = labelTouched && !label.trim();
  const secretInvalid = secretTouched && !credential && secret.trim().length < 8;
  // Anything typed here is lost on a stray backdrop click, so the sheet asks
  // first. A saved key being re-checked is not itself a change.
  const dirty = Boolean(
    secret.trim()
    || label !== (credential?.label ?? "")
    || isPrimary !== (credential?.is_primary ?? false)
    || enabled !== (credential?.enabled ?? true)
    || !checkProtocol
    || resetSpend
    || Object.keys(selectedModels).length > 0
  );

  const checkKey = async (): Promise<CredentialInspection | null> => {
    setBusy(true);
    setInspection(null);
    setSelectedModels({});
    try {
      let result: CredentialInspection;
      if (credential && !secret.trim()) {
        result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/models/discover`, {
          method: "POST",
          json: { credential_id: credential.id, skip_protocol_check: !checkProtocol }
        });
      } else {
        if (secret.trim().length < 8) {
          notify("Enter a complete API key before checking it.", "danger");
          return null;
        }
        result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/credentials/inspect`, {
          method: "POST",
          json: { secret, skip_protocol_check: !checkProtocol }
        });
      }
      setInspection(result);
      setCheckedSecret(secret.trim());
      if (!result.valid) {
        notify(result.warning || "The provider rejected this API key.", "danger");
      }
      return result;
    } catch (caught) {
      notify(errorMessage(caught), "danger");
      return null;
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    let checked = inspection;
    const canContinueWithSavedKey = Boolean(credential && checked && !checked.valid && !secret.trim());
    if ((!checked?.valid && !canContinueWithSavedKey) || (secret.trim() && checkedSecret !== secret.trim())) {
      checked = await checkKey();
      if (!checked?.valid) return;
    }
    const trimmedBalance = balance.trim();
    const parsedBalance = trimmedBalance === "" ? null : Number(trimmedBalance);
    if (parsedBalance !== null && (!Number.isFinite(parsedBalance) || parsedBalance < 0)) {
      notify("Enter the API key balance as a positive USD amount, or leave it blank to stop tracking it.", "danger");
      return;
    }
    setBusy(true);
    try {
      let discovered: DiscoveredModel[] = checked?.models ?? [];
      if (credential) {
        const result = await api<{ models: DiscoveredModel[] }>(`/api/admin/credentials/${credential.id}`, {
          method: "PUT",
          json: { label, secret, is_primary: isPrimary, enabled, limits, balance_usd: parsedBalance, reset_spend: resetSpend, skip_protocol_check: !checkProtocol }
        });
        discovered = result.models ?? discovered;
      } else {
        const credentials = [{ label, secret, is_primary: isPrimary, enabled, limits, balance_usd: parsedBalance, skip_protocol_check: !checkProtocol }];
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
    <Sheet
      title={credential ? `Edit ${credential.label}` : "Add API key"}
      eyebrow={provider.name}
      onClose={onClose}
      wide
      dirty={dirty}
      discardMessage="Close this panel? The API key and limits typed here are not saved yet."
      actions={credential ? <Button variant="danger" disabled={busy || deleting} onClick={() => {
        void (async () => {
          const confirmed = await ask({
            title: `Delete API key ${credential.label}?`,
            body: "Requests routed to it move to the provider's other keys. This cannot be undone.",
            confirmLabel: "Delete API key"
          });
          if (!confirmed) return;
          setDeleting(true);
          try {
            await api(`/api/admin/credentials/${credential.id}`, { method: "DELETE" });
            onComplete(`API key ${credential.label} deleted.`);
          } catch (caught) {
            notify(errorMessage(caught), "danger");
          } finally {
            setDeleting(false);
          }
        })();
      }}><Trash2 size={14} aria-hidden="true" /> {deleting ? "Deleting…" : "Delete key"}</Button> : undefined}
    >
      {/* A key that cannot serve is a failure; a note on a key that still routes is
          only something to check. The two are told apart here the same way the
          provider banner and the dashboard alerts tell them apart. */}
      {credential?.validation_error && (isUnusableKey(credential)
        ? <InlineNotice tone="danger">{credential.validation_error}</InlineNotice>
        : <InlineNotice tone="warning">{credential.validation_error} This key still receives traffic.</InlineNotice>)}
      {/* The sheet body is a form, so the browser's own `required` and `type`
          validation runs and Enter submits from any field. */}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          setLabelTouched(true);
          setSecretTouched(true);
          void save();
        }}
      >
      <div className="field-pair">
        <label className="field">
          <span>Label</span>
          <input
            required
            placeholder="Production key"
            value={label}
            aria-invalid={labelInvalid || undefined}
            aria-describedby={labelInvalid ? `${fieldID}-label-error` : undefined}
            onBlur={() => setLabelTouched(true)}
            onChange={(e) => setLabel(e.target.value)}
          />
          {labelInvalid && <small className="field-error" id={`${fieldID}-label-error`}>Give the key a name you will recognise in the logs.</small>}
        </label>
        <label className="field">
          <span>{credential ? "Replacement API key" : "API key"} <small>{credential ? "Leave blank to check the saved key" : ""}</small></span>
          <input
            type="password"
            required={!credential}
            autoComplete="new-password"
            spellCheck={false}
            value={secret}
            aria-invalid={secretInvalid || undefined}
            aria-describedby={secretInvalid ? `${fieldID}-secret-error` : undefined}
            onBlur={() => setSecretTouched(true)}
            onChange={(e) => {
              setSecret(e.target.value);
              setInspection(null);
              setSelectedModels({});
            }}
          />
          {secretInvalid && <small className="field-error" id={`${fieldID}-secret-error`}>Paste the whole key — this one looks truncated.</small>}
        </label>
      </div>
      <Toggle checked={isPrimary} onChange={setIsPrimary} label="Use as primary" description="Optional. This key is tried first while it has capacity; other keys remain fallbacks." />
      <Toggle checked={enabled} onChange={setEnabled} label="Enable API key" description="Re-enabling also clears quarantine and circuit-breaker state." />
      <Toggle
        checked={checkProtocol}
        onChange={(checked) => {
          setCheckProtocol(checked);
          setInspection(null);
          setSelectedModels({});
        }}
        label="Check inference protocol"
        description="Turn this off when the model catalog works but the provider blocks protocol probes. The key can still be saved and used."
      />
      <div className="validation-action">
        <div>
          <strong>{checkProtocol ? "Check the key and load models" : "Verify key and load models"}</strong>
          <small>{checkProtocol ? "Rotakey loads `/models` and verifies the configured inference protocol." : "Rotakey verifies the key through `/models` without sending an inference probe."}</small>
        </div>
        <Button type="button" variant="quiet" disabled={busy} onClick={() => void checkKey()}>
          <RefreshCw size={14} aria-hidden="true" /> {busy ? "Checking…" : checkProtocol ? "Check key" : "Load models"}
        </Button>
      </div>
      {inspection && (
        <InlineNotice tone={inspection.valid ? "success" : "danger"}>
          {inspection.valid
            ? inspection.protocol_verified
              ? `API key and ${inspection.protocol} base URL verified · ${inspection.models.length} models loaded · ${inspection.latency_ms} ms`
              : `API key and model catalog verified · protocol check skipped · ${inspection.models.length} models loaded · ${inspection.latency_ms} ms`
            : inspection.warning || keyCheckFailed}
        </InlineNotice>
      )}
      {inspection && (
        <ModelCatalog
          provider={provider}
          models={inspection.models}
          existing={provider.models}
          selected={selectedModels}
          onChange={setSelectedModels}
        />
      )}
      <fieldset><legend>Shared API key limits</legend><p className="fieldset-note">Requests from every model under this provider consume these limits together. Blank means no limit.</p><RateFields value={limits} onChange={setLimits} /></fieldset>
      <CredentialBalanceFields
        credential={credential}
        balance={balance}
        onBalanceChange={setBalance}
        resetSpend={resetSpend}
        onResetSpendChange={setResetSpend}
      />
      {credential && provider.models.length > 0 && (
        <fieldset>
          <legend>Optional model-specific limit</legend>
          <p className="fieldset-note">Leave this unset to use only the shared key limit. When set, both shared and model limits must have capacity.</p>
          <label className="field"><span>Model route</span><select value={selectedModel} onChange={(e) => setSelectedModel(e.target.value)}>{provider.models.map((model) => <option key={model.id} value={model.id}>{model.public_alias}</option>)}</select></label>
          <RateFields value={modelLimits} onChange={setModelLimits} compact />
          {/* These two save and clear one model's limit on this key and nothing
              else, so they stay in the panel: the operator may have a replacement
              key typed above, and closing would discard it. */}
          <div className="button-row">
            <Button
              type="button"
              variant="quiet"
              disabled={limitBusy !== null}
              onClick={() => {
                setLimitBusy("save");
                void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "PUT", json: modelLimits })
                  .then(() => {
                    notify(`Model limit saved on ${credential.label}.`);
                    onRefresh();
                  })
                  .catch((caught) => notify(errorMessage(caught), "danger"))
                  .finally(() => setLimitBusy(null));
              }}
            >
              {limitBusy === "save" ? "Saving…" : "Save model limit"}
            </Button>
            {credential.model_limits[selectedModel] && (
              <Button
                type="button"
                variant="quiet"
                disabled={limitBusy !== null}
                onClick={() => {
                  setLimitBusy("clear");
                  void api(`/api/admin/credentials/${credential.id}/model-limits/${selectedModel}`, { method: "DELETE" })
                    .then(() => {
                      notify(`${credential.label} is back on the shared key limit for this route.`);
                      onRefresh();
                    })
                    .catch((caught) => notify(errorMessage(caught), "danger"))
                    .finally(() => setLimitBusy(null));
                }}
              >
                {limitBusy === "clear" ? "Clearing…" : "Use shared limit"}
              </Button>
            )}
          </div>
        </fieldset>
      )}
      <div className="sheet-actions">
        <span />
        <Button type="submit" disabled={busy}>{busy ? "Working…" : inspection?.valid ? credential ? "Save API key & routes" : "Add API key & routes" : checkProtocol ? "Check and save" : "Load models and save"}</Button>
      </div>
      </form>
    </Sheet>
  );
}

// CredentialBalanceFields records how much credit sits on one API key. The
// balance is optional on purpose: most installs never track it, and a blank
// field has to keep meaning "untracked" rather than "empty".
function CredentialBalanceFields({ credential, balance, onBalanceChange, resetSpend, onResetSpendChange }: {
  credential?: Credential;
  balance: string;
  onBalanceChange: (value: string) => void;
  resetSpend: boolean;
  onResetSpendChange: (value: boolean) => void;
}) {
  const spent = credential?.balance_spent_usd ?? 0;
  const tracked = balance.trim() !== "";
  const parsed = Number(balance.trim());
  const remaining = tracked && Number.isFinite(parsed) ? Math.max(0, parsed - (resetSpend ? 0 : spent)) : null;
  // Save rejects an unusable figure with a toast, which is gone before the
  // operator looks back at the field. The field says what is wrong itself, and
  // the message is bound to the input so it reaches assistive tech too.
  const invalid = balanceInvalid(balance);
  const errorID = useId();

  return (
    <fieldset>
      <legend>API key balance</legend>
      <p className="fieldset-note">
        Optional. Record the credit on this key and Rotakey subtracts the estimated cost of every request it serves.
        A key that is out of balance is skipped by the router, so traffic moves to your other keys instead of failing upstream.
        Leave it blank to not track a balance on this key.
      </p>
      <div className="field-pair">
        <label className="field">
          <span>Balance <small>USD</small></span>
          <input
            type="number" min="0" step="0.01" inputMode="decimal" placeholder="Not tracked"
            value={balance} onChange={(event) => onBalanceChange(event.target.value)}
            aria-invalid={invalid ? true : undefined}
            aria-describedby={invalid ? errorID : undefined}
          />
        </label>
        {credential && (
          <label className="field">
            <span>Spent so far <small>estimated</small></span>
            {/* readOnly, not disabled: a disabled field is skipped by the tab
                order, so the figure could not be read by keyboard at all. */}
            <input value={formatUSD(spent)} readOnly tabIndex={0} />
          </label>
        )}
      </div>
      {invalid && (
        <div id={errorID}>
          <InlineNotice tone="danger">Enter the balance as a positive USD amount, or leave it blank to stop tracking it.</InlineNotice>
        </div>
      )}
      {remaining !== null && (
        <p className="fieldset-note">
          Remaining after this change: <strong>{formatUSD(remaining)}</strong>
          {remaining <= 0 && " · this key will stop receiving traffic."}
        </p>
      )}
      {credential && spent > 0 && (
        <Toggle
          checked={resetSpend}
          onChange={onResetSpendChange}
          label="Reset the spend counter"
          description={`Use this after topping the key up upstream. Clears the recorded ${formatUSD(spent)} so the new balance starts fresh.`}
        />
      )}
    </fieldset>
  );
}

function ModelImportForm({ provider, onClose, onComplete, notify }: { provider: Provider; onClose: () => void; onComplete: (message: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [inspection, setInspection] = useState<CredentialInspection | null>(null);
  const [selected, setSelected] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);

  // Discovery is refetched by the Reload button and once on open, so it is a
  // memoised callback the effect can honestly depend on.
  const load = useCallback(async () => {
    setBusy(true);
    setInspection(null);
    setSelected({});
    try {
      const result = await api<CredentialInspection>(`/api/admin/providers/${provider.id}/models/discover`, {
        method: "POST",
        json: {}
      });
      setInspection(result);
      if (!result.valid) notify(result.warning || discoveryFailed, "danger");
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  }, [provider.id, notify]);

  useEffect(() => { void load(); }, [load]);

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
    <Sheet
      title="Load provider models"
      eyebrow={provider.name}
      onClose={onClose}
      wide
      dirty={Object.keys(selected).length > 0}
      discardMessage="Close this panel? The selected model routes have not been enabled yet."
    >
      <div className="validation-action">
        <div><strong>Provider model catalog</strong><small>Uses the primary API key first, then records whether that key is valid.</small></div>
        <Button variant="quiet" disabled={busy} onClick={() => void load()}><RefreshCw size={14} aria-hidden="true" /> Reload</Button>
      </div>
      {busy && !inspection && <PageSkeleton />}
      {inspection && (
        <InlineNotice tone={inspection.valid ? "success" : "danger"}>
          {inspection.valid
            ? `${inspection.models.length} models loaded in ${inspection.latency_ms} ms.`
            : inspection.warning || discoveryFailed}
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
  const manualNoteID = useId();
  const manualModelNote = (() => {
    const typed = manualModel.trim();
    if (!typed) return "";
    if (existingIDs.has(typed)) return "This model is already routed on this provider.";
    if (selected[typed] !== undefined) return "This model is already in the selection below.";
    return "";
  })();
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
        <div><strong>Select models to route</strong><small>{selectedCount} selected · {existingIDs.size} already routed</small></div>
        <div className="button-row">
          {selectedCount > 0 && <Button type="button" variant="quiet" onClick={() => onChange({})}>Clear all selected</Button>}
        </div>
      </header>
      <label className="catalog-search">
        <Search size={15} aria-hidden="true" />
        {/* The label wraps only an icon, so the field needs its own name. */}
        <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter model IDs" aria-label="Filter model IDs" />
      </label>
      <label className="catalog-select-all">
        <input type="checkbox" checked={allVisibleSelected} disabled={selectable.length === 0} onChange={(event) => toggleVisible(event.target.checked)} />
        <span>
          <strong>{query.trim() ? "Select all search results" : "Select all loaded models"}</strong>
          <small>{selectedVisibleCount}/{selectable.length} available results selected{existingIDs.size ? ` · ${existingIDs.size} already routed` : ""}</small>
        </span>
      </label>
      <div className="manual-model-entry">
        {/* A typed id that is already routed, or already in the selection, used to
            leave the button dead with nothing said. The note states which of the two
            it is, and is bound to the input so it is announced too. It sits below
            both controls rather than inside the label, so appearing does not push
            the button out of line with the field. */}
        <label className="field">
          <span>Manual model ID <small>Use this when the provider does not expose a model catalog</small></span>
          <input
            value={manualModel}
            onChange={(event) => setManualModel(event.target.value)}
            placeholder="claude-model-id"
            aria-invalid={manualModelNote ? true : undefined}
            aria-describedby={manualModelNote ? manualNoteID : undefined}
          />
        </label>
        <Button type="button" variant="quiet" disabled={!manualModel.trim() || Boolean(manualModelNote)} onClick={() => {
          const upstream = manualModel.trim();
          if (!upstream) return;
          onChange({ ...selected, [upstream]: defaultPublicAlias(provider.slug, upstream, routingMode) });
          setManualModel("");
        }}><Plus size={14} aria-hidden="true" /> Add model ID</Button>
        {manualModelNote && <small className="field-error manual-model-entry__note" id={manualNoteID}>{manualModelNote}</small>}
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
                  {/* Every row repeats this label, so the model it belongs to goes
                      in the accessible name — otherwise a screen reader hears
                      "Public alias" once per selected model with no distinction. */}
                  <input
                    value={selected[model.id]}
                    onChange={(event) => onChange({ ...selected, [model.id]: event.target.value })}
                    aria-label={`Public alias for ${model.id}`}
                  />
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

type PlaygroundProtocol = "auto" | "chat" | "responses" | "messages";
type PlaygroundPanel = "models" | "run" | "settings";
type PlaygroundModel = ModelRoute & { provider: Provider; credentials: Credential[] };
type PlaygroundRequestDraft = {
  prompt: string;
  system: string;
  protocol: PlaygroundProtocol;
  maxTokens: number;
  temperature: number;
};
type PlaygroundRun = {
  id: number;
  modelAlias: string;
  prompt: string;
  response?: string;
  error?: string;
  protocol: PlaygroundProtocol;
  latencyMS: number;
  inputTokens: number;
  outputTokens: number;
};

type PlaygroundRequestState = {
  phase: "idle" | "sending" | "waiting" | "completed" | "failed" | "cancelled";
  startedAt: number;
  elapsedMS: number;
  protocol: PlaygroundProtocol;
  error?: string;
};

function formatElapsed(milliseconds: number) {
  return `${(milliseconds / 1000).toFixed(1)}s`;
}

function playgroundRequestLabel(phase: PlaygroundRequestState["phase"]) {
  switch (phase) {
    case "sending": return "Request sent";
    case "waiting": return "Waiting for model response";
    case "completed": return "Response received";
    case "failed": return "Request failed";
    case "cancelled": return "Request cancelled";
    default: return "Ready to send";
  }
}

function recordValue(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function playgroundText(payload: Record<string, unknown>): string {
  if (typeof payload.output_text === "string") return payload.output_text;
  const choices = Array.isArray(payload.choices) ? payload.choices : [];
  const message = recordValue(recordValue(choices[0]).message);
  if (typeof message.content === "string") return message.content;
  const anthropicContent = Array.isArray(payload.content) ? payload.content : [];
  const anthropicText = anthropicContent.map((item) => recordValue(item).text).filter((item): item is string => typeof item === "string").join("\n");
  if (anthropicText) return anthropicText;
  const output = Array.isArray(payload.output) ? payload.output : [];
  const responseText = output.flatMap((item) => {
    const content = recordValue(item).content;
    return Array.isArray(content) ? content : [];
  }).map((item) => recordValue(item).text).filter((item): item is string => typeof item === "string").join("\n");
  return responseText || JSON.stringify(payload, null, 2);
}

function playgroundUsage(payload: Record<string, unknown>) {
  const usage = recordValue(payload.usage);
  return {
    input: Number(usage.prompt_tokens ?? usage.input_tokens ?? 0),
    output: Number(usage.completion_tokens ?? usage.output_tokens ?? 0)
  };
}

function playgroundResponseProtocol(payload: Record<string, unknown>, requested: PlaygroundProtocol): PlaygroundProtocol {
  if (payload.type === "message") return "messages";
  if (typeof payload.object === "string" && payload.object.startsWith("response")) return "responses";
  return requested === "auto" ? "chat" : requested;
}

function PlaygroundPage({
  navigate,
  notify
}: {
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(location.search).get("model") || "");
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [activePanel, setActivePanel] = useState<PlaygroundPanel>("models");
  const compact = useMediaQuery("(max-width: 1050px)");
  const [probeResults, setProbeResults] = useState<Record<string, ModelProbeResult>>({});
  const [checkingAll, setCheckingAll] = useState(false);
  const [checkingID, setCheckingID] = useState("");
  const [bulkBusy, setBulkBusy] = useState(false);
  const [probeProgress, setProbeProgress] = useState({ completed: 0, total: 0 });
  const stopSweep = useRef(false);
  const mounted = useRef(true);
  const [prompt, setPrompt] = useState("");
  const [systemPrompt, setSystemPrompt] = useState("");
  const [protocol, setProtocol] = useState<PlaygroundProtocol>("auto");
  const [maxTokens, setMaxTokens] = useState(1024);
  const [temperature, setTemperature] = useState(0.7);
  const [runs, setRuns] = useState<PlaygroundRun[]>([]);
  const [running, setRunning] = useState(false);
  const [lastRequest, setLastRequest] = useState<PlaygroundRequestDraft | null>(null);
  const [requestState, setRequestState] = useState<PlaygroundRequestState>({ phase: "idle", startedAt: 0, elapsedMS: 0, protocol: "auto" });
  const requestController = useRef<AbortController | null>(null);
  const requestPhaseTimer = useRef<number | null>(null);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      stopSweep.current = true;
      requestController.current?.abort();
      if (requestPhaseTimer.current !== null) window.clearTimeout(requestPhaseTimer.current);
    };
  }, []);

  useEffect(() => {
    if (!running || !requestState.startedAt) return;
    const timer = window.setInterval(() => {
      setRequestState((current) => ({ ...current, elapsedMS: performance.now() - current.startedAt }));
    }, 100);
    return () => window.clearInterval(timer);
  }, [running, requestState.startedAt]);

  const load = useCallback(async (background = false) => {
    if (!background) setLoading(true);
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      const normalized = normalizeProviders(result.providers);
      if (!mounted.current) return;
      setProviders(normalized);
      setLoadError("");
      const available = normalized.flatMap((provider) => provider.models);
      setSelectedID((current) => available.some((model) => model.id === current) ? current : available[0]?.id || "");
    } catch (caught) {
      if (mounted.current) setLoadError(errorMessage(caught));
    } finally {
      if (mounted.current) setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);
  useEffect(() => {
    if (!selectedID) return;
    const url = new URL(location.href);
    url.searchParams.set("model", selectedID);
    history.replaceState(history.state, "", url);
  }, [selectedID]);

  const models: PlaygroundModel[] = providers.flatMap((provider) => provider.models.map((model) => ({ ...model, provider, credentials: provider.credentials })));
  const readyKeys = (model: PlaygroundModel) => model.credentials.filter((credential) => credentialPoolState(credential) === "healthy").length;
  const modelState = (model: PlaygroundModel) => {
    const probe = probeResults[model.id];
    if (!model.enabled) return "disabled";
    if (probe?.state === "checking") return "checking";
    if (probe?.state === "failed" || model.capability_status === "failed") return "failed";
    if (readyKeys(model) === 0 || probe?.state === "blocked") return "waiting";
    if (probe?.state === "passed" || probe?.state === "warning" || model.capability_status === "probe_verified" || model.capability_status === "catalog_verified") return "live";
    return "unverified";
  };
  const selected = models.find((model) => model.id === selectedID);
  const filtered = models.filter((model) => {
    const needle = query.trim().toLowerCase();
    return (!providerFilter || model.provider.id === providerFilter) &&
      (!statusFilter || modelState(model) === statusFilter) &&
      (!needle || `${model.public_alias} ${model.upstream_model} ${model.provider.name}`.toLowerCase().includes(needle));
  });
  const failedModels = models.filter((model) => modelState(model) === "failed");

  const checkModel = async (model: PlaygroundModel, quiet = false) => {
    setCheckingID(model.id);
    setProbeResults((current) => ({ ...current, [model.id]: { state: "checking" } }));
    try {
      const result = await api<{ warning?: string }>(`/api/admin/models/${model.id}/probe`, { method: "POST" });
      if (!mounted.current) return;
      setProbeResults((current) => ({ ...current, [model.id]: result.warning ? { state: "warning", error: result.warning } : { state: "passed" } }));
      if (!quiet) notify(result.warning ? "Model is provider-listed; the live probe was inconclusive." : `${model.public_alias} is live.`);
    } catch (caught) {
      if (!mounted.current) return;
      const state = caught instanceof APIError && caught.code === "model_probe_blocked" ? "blocked" : "failed";
      setProbeResults((current) => ({ ...current, [model.id]: { state, error: errorMessage(caught) } }));
      if (!quiet) notify(errorMessage(caught), "danger");
    } finally {
      if (mounted.current) setCheckingID("");
    }
  };

  const checkAll = async () => {
    if (checkingAll || models.length === 0) return;
    stopSweep.current = false;
    setCheckingAll(true);
    setProbeProgress({ completed: 0, total: models.length });
    let cursor = 0;
    let completed = 0;
    const worker = async () => {
      while (!stopSweep.current && cursor < models.length) {
        const model = models[cursor++];
        await checkModel(model, true);
        completed++;
        if (mounted.current) setProbeProgress({ completed, total: models.length });
      }
    };
    await Promise.all(Array.from({ length: Math.min(3, models.length) }, worker));
    if (!mounted.current) return;
    setCheckingAll(false);
    notify(stopSweep.current ? `Model sweep stopped after ${completed} checks.` : `${completed} model routes checked.`);
    await load(true);
  };

  const bulkDisableFailed = async () => {
    if (bulkBusy || failedModels.length === 0) return;
    if (!(await ask({ title: `Disable ${failedModels.length} failed route${failedModels.length === 1 ? "" : "s"}?`, body: "The routes remain configured but stop receiving traffic until they are enabled again.", confirmLabel: `Disable ${failedModels.length}`, detail: failedModels.map((model) => model.public_alias).join("\n") }))) return;
    setBulkBusy(true);
    let changed = 0;
    let failed = 0;
    for (const model of failedModels) {
      try {
        await api(`/api/admin/models/${model.id}`, { method: "PUT", json: { ...modelToDraft(model), enabled: false } });
        changed++;
      } catch {
        failed++;
      }
    }
    setBulkBusy(false);
    notify(`${changed} failed route${changed === 1 ? "" : "s"} disabled${failed ? ` · ${failed} could not be changed` : ""}.`, failed ? "danger" : "success");
    await load(true);
  };

  const bulkDeleteFailed = async () => {
    if (bulkBusy || failedModels.length === 0) return;
    if (!(await ask({ title: `Delete ${failedModels.length} failed route${failedModels.length === 1 ? "" : "s"}?`, body: "These public aliases stop immediately and cannot be restored.", confirmLabel: `Delete ${failedModels.length}`, detail: failedModels.map((model) => model.public_alias).join("\n") }))) return;
    setBulkBusy(true);
    let changed = 0;
    let failed = 0;
    for (const model of failedModels) {
      try {
        await api(`/api/admin/models/${model.id}`, { method: "DELETE" });
        changed++;
      } catch {
        failed++;
      }
    }
    setBulkBusy(false);
    notify(`${changed} failed route${changed === 1 ? "" : "s"} deleted${failed ? ` · ${failed} could not be deleted` : ""}.`, failed ? "danger" : "success");
    await load(true);
  };

  const runPrompt = async (requestOverride?: PlaygroundRequestDraft) => {
    if (!selected || running) return;
    const request = requestOverride || { prompt: prompt.trim(), system: systemPrompt, protocol, maxTokens, temperature };
    if (!request.prompt.trim()) return;
    const target = selected;
    const requestPrompt = request.prompt.trim();
    const started = performance.now();
    const controller = new AbortController();
    requestController.current = controller;
    setLastRequest(request);
    setRequestState({ phase: "sending", startedAt: started, elapsedMS: 0, protocol: request.protocol });
    requestPhaseTimer.current = window.setTimeout(() => {
      if (mounted.current) setRequestState((current) => current.phase === "sending" ? { ...current, phase: "waiting" } : current);
    }, 350);
    setRunning(true);
    try {
      const payload = await api<Record<string, unknown>>("/api/admin/playground/run", {
        method: "POST",
        signal: controller.signal,
        json: {
          model: target.public_alias,
          prompt: requestPrompt,
          system: request.system,
          protocol: request.protocol,
          max_tokens: request.maxTokens,
          temperature: request.temperature
        }
      });
      const usage = playgroundUsage(payload);
      setRuns((current) => [{
        id: Date.now(), modelAlias: target.public_alias, prompt: requestPrompt, response: playgroundText(payload),
        protocol: playgroundResponseProtocol(payload, request.protocol), latencyMS: Math.round(performance.now() - started),
        inputTokens: usage.input, outputTokens: usage.output
      }, ...current].slice(0, 20));
      setRequestState({ phase: "completed", startedAt: started, elapsedMS: performance.now() - started, protocol: playgroundResponseProtocol(payload, request.protocol) });
      setPrompt("");
    } catch (caught) {
      const elapsedMS = performance.now() - started;
      if (controller.signal.aborted) {
        setRequestState({ phase: "cancelled", startedAt: started, elapsedMS, protocol: request.protocol });
        return;
      }
      setRuns((current) => [{
        id: Date.now(), modelAlias: target.public_alias, prompt: requestPrompt, error: errorMessage(caught), protocol: request.protocol,
        latencyMS: Math.round(elapsedMS), inputTokens: 0, outputTokens: 0
      }, ...current].slice(0, 20));
      setRequestState({ phase: "failed", startedAt: started, elapsedMS, protocol: request.protocol, error: errorMessage(caught) });
    } finally {
      if (requestController.current === controller) requestController.current = null;
      if (requestPhaseTimer.current !== null) {
        window.clearTimeout(requestPhaseTimer.current);
        requestPhaseTimer.current = null;
      }
      if (mounted.current) setRunning(false);
    }
  };

  if (loading) return <div className="resource-page"><PageHeader eyebrow="Model lab" title="Playground" description="Run and manage every model route from one workspace." /><PageSkeleton /></div>;
  if (loadError && models.length === 0) return <EmptyState level={2} title="Playground could not be loaded" description={loadError} action={<Button onClick={() => void load()}><RefreshCw size={14} aria-hidden="true" /> Try again</Button>} />;
  if (models.length === 0) return <EmptyState level={2} title="No model routes to test" description="Add a route to a provider first." action={<Button onClick={() => navigate("providers")}><ArrowRight size={14} aria-hidden="true" /> Go to providers</Button>} />;

  return (
    <div className="resource-page playground-page">
      <PageHeader eyebrow="Model lab" title="Playground" description="Check availability, run real prompts, and manage route settings without leaving the workspace." />
      {loadError && <InlineNotice tone="danger">{loadError} Showing the last loaded model list.</InlineNotice>}
      <div className="playground-tabs" role="tablist" aria-label="Playground panels">
        {(["models", "run", "settings"] as PlaygroundPanel[]).map((panel) => <button key={panel} role="tab" aria-selected={activePanel === panel} onClick={() => setActivePanel(panel)}>{panel === "run" ? "Run" : panel[0].toUpperCase() + panel.slice(1)}</button>)}
      </div>
      <div className="playground-workbench">
        <section className={`playground-models${activePanel === "models" ? " is-active" : ""}`}>
          <header className="playground-pane-title"><div><span>Routes</span><strong>{filtered.length} of {models.length}</strong></div><Button variant="quiet" disabled={checkingAll} onClick={() => void checkAll()}><Activity size={13} aria-hidden="true" /> Check all</Button></header>
          <div className="playground-filters">
            <label><Search size={14} aria-hidden="true" /><span className="sr-only">Search model routes</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Alias, model or provider" /></label>
            <select aria-label="Filter by provider" value={providerFilter} onChange={(event) => setProviderFilter(event.target.value)}><option value="">All providers</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select>
            <select aria-label="Filter by status" value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="">All statuses</option><option value="live">Live</option><option value="failed">Failed</option><option value="waiting">Waiting</option><option value="disabled">Disabled</option><option value="unverified">Unverified</option></select>
          </div>
          {checkingAll && <div className="playground-progress"><span style={{ width: `${probeProgress.total ? probeProgress.completed / probeProgress.total * 100 : 0}%` }} /><small>{probeProgress.completed}/{probeProgress.total}</small><button onClick={() => { stopSweep.current = true; }}>Stop</button></div>}
          {failedModels.length > 0 && <div className="playground-bulk-actions"><span>{failedModels.length} failed</span><button disabled={bulkBusy || checkingAll} onClick={() => void bulkDisableFailed()}><Power size={12} aria-hidden="true" /> Disable</button><button disabled={bulkBusy || checkingAll} onClick={() => void bulkDeleteFailed()}><Trash2 size={12} aria-hidden="true" /> Delete</button></div>}
          <div className="playground-model-list">
            {filtered.map((model) => {
              const state = modelState(model);
              return <div key={model.id} className={selectedID === model.id ? "is-selected" : ""}>
                <button className="playground-model-select" onClick={() => { setSelectedID(model.id); if (compact) setActivePanel("run"); }} aria-current={selectedID === model.id}>
                  <span className={`playground-model-dot is-${state}`} role="img" aria-label={state === "live" ? "Live" : state === "failed" ? "Failed" : state === "checking" ? "Checking" : state === "waiting" ? "Waiting for a key" : state === "disabled" ? "Disabled" : "Unverified"} />
                  <span><code title={model.public_alias}>{model.public_alias}</code><small>{model.provider.name} · {state} · {readyKeys(model)}/{model.credentials.length} keys</small></span>
                </button>
                <button className="console-icon" disabled={checkingAll || checkingID === model.id} onClick={() => void checkModel(model)} aria-label={`Check ${model.public_alias}`} title="Check model"><RefreshCw size={13} aria-hidden="true" /></button>
              </div>;
            })}
            {filtered.length === 0 && <p className="inline-empty">No model routes match these filters.</p>}
          </div>
        </section>

        <section className={`playground-runner${activePanel === "run" ? " is-active" : ""}`}>
          <header className="playground-pane-title"><div><span>Test target</span><strong title={selected?.public_alias}>{selected?.public_alias}</strong><small>{selected?.provider.name} · {selected ? modelState(selected) : "unavailable"}</small></div>{selected && <Button variant="quiet" onClick={() => setActivePanel("settings")}>Settings</Button>}</header>
            <div className="playground-protocols" role="group" aria-label="Request protocol">{(["auto", "chat", "responses", "messages"] as PlaygroundProtocol[]).map((item) => <button key={item} className={protocol === item ? "is-active" : ""} onClick={() => setProtocol(item)}>{item === "messages" ? "Messages" : item[0].toUpperCase() + item.slice(1)}</button>)}</div>
          <div className={`playground-live-status is-${requestState.phase}`} role="status" aria-live="polite">
            <span className="playground-live-status__dot" aria-hidden="true" />
            <div><strong>{playgroundRequestLabel(requestState.phase)}</strong><small>{selected?.provider.name} · {requestState.protocol === "auto" ? "auto protocol" : requestState.protocol}</small></div>
            <time>{formatElapsed(requestState.elapsedMS)}</time>
            {requestState.phase === "failed" && lastRequest && <button type="button" onClick={() => void runPrompt(lastRequest)} disabled={running}><RefreshCw size={13} aria-hidden="true" /> Retry</button>}
          </div>
          <div className={`playground-error-detail${requestState.phase === "failed" && requestState.error ? "" : " is-empty"}`} aria-hidden={requestState.phase !== "failed" || !requestState.error}>
            {requestState.phase === "failed" && requestState.error && <><AlertTriangle size={15} aria-hidden="true" /><pre>{requestState.error}</pre></>}
          </div>
          <div className="playground-transcript" aria-live="polite">
            {runs.length === 0 ? <div className="playground-empty"><FlaskConical size={22} aria-hidden="true" /><strong>Ready for a real gateway request</strong><p>The selected route uses its configured provider, key rotation, failover, translation and cost tracking.</p></div> : runs.map((run) => <article key={run.id} className={run.error ? "has-error" : ""}>
              <header><span>{run.protocol}</span><small>{run.latencyMS} ms · {run.inputTokens} in · {run.outputTokens} out</small></header>
              <div className="playground-user-message"><strong>You</strong><p>{run.prompt}</p></div>
              <div className="playground-model-message"><strong>{run.error ? `${run.modelAlias} error` : run.modelAlias}</strong><pre>{run.error || run.response}</pre></div>
            </article>)}
          </div>
          <form className="playground-composer" onSubmit={(event) => { event.preventDefault(); void runPrompt(); }}>
            <details><summary>Request options</summary><label><span>System prompt</span><textarea value={systemPrompt} onChange={(event) => setSystemPrompt(event.target.value)} rows={3} /></label><div><label><span>Max output</span><input type="number" min={1} max={1000000} value={maxTokens} onChange={(event) => setMaxTokens(Number(event.target.value))} /></label><label><span>Temperature</span><input type="number" min={0} max={2} step={0.1} value={temperature} onChange={(event) => setTemperature(Number(event.target.value))} /></label></div></details>
            <label><span className="sr-only">Prompt</span><textarea rows={3} value={prompt} onChange={(event) => setPrompt(event.target.value)} placeholder="Send a prompt through the selected route" onKeyDown={(event) => { if ((event.ctrlKey || event.metaKey) && event.key === "Enter") { event.preventDefault(); void runPrompt(); } }} /></label>
            {running ? <button type="button" className="playground-send" onClick={() => requestController.current?.abort()} aria-label="Stop request" title="Stop request"><Square size={16} aria-hidden="true" /></button> : <button type="submit" className="playground-send" disabled={!selected || !prompt.trim()} aria-label="Send prompt" title="Send prompt"><Send size={17} aria-hidden="true" /></button>}
          </form>
        </section>

        <aside className={`playground-settings${activePanel === "settings" ? " is-active" : ""}`}>
          {selected && <PlaygroundSettings key={selected.id} model={selected} navigate={navigate} notify={notify} onUpdated={() => load(true)} />}
        </aside>
      </div>
    </div>
  );
}

function PlaygroundSettings({
  model,
  navigate,
  notify,
  onUpdated
}: {
  model: PlaygroundModel;
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
  onUpdated: () => Promise<void>;
}) {
  const ask = useConfirm();
  const [draft, setDraft] = useState<ModelDraft>(() => modelToDraft(model));
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  useEffect(() => setDraft(modelToDraft(model)), [model.updated_at]);
  const save = async () => {
    setSaving(true);
    try {
      await api(`/api/admin/models/${model.id}`, { method: "PUT", json: draft });
      notify(`Route ${draft.public_alias} updated.`);
      await onUpdated();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setSaving(false);
    }
  };
  const remove = async () => {
    if (!(await ask(deleteRouteQuestion(model.public_alias)))) return;
    setDeleting(true);
    try {
      await api(`/api/admin/models/${model.id}`, { method: "DELETE" });
      notify(`Route ${model.public_alias} deleted.`);
      await onUpdated();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setDeleting(false);
    }
  };
  const toggleEnabled = async () => {
    const enabled = !draft.enabled;
    setSaving(true);
    try {
      await api(`/api/admin/models/${model.id}`, { method: "PUT", json: { ...draft, enabled } });
      setDraft((current) => ({ ...current, enabled }));
      notify(`${model.public_alias} ${enabled ? "enabled" : "disabled"}.`);
      await onUpdated();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setSaving(false);
    }
  };
  return <>
    <header className="playground-pane-title"><div><span>Route settings</span><strong>{model.provider.name}</strong><small>{model.upstream_model}</small></div><div className="button-row"><button className="console-icon" disabled={saving || deleting} onClick={() => void toggleEnabled()} aria-label={`${draft.enabled ? "Disable" : "Enable"} ${model.public_alias}`} title={draft.enabled ? "Disable route" : "Enable route"}><Power size={14} aria-hidden="true" /></button><button className="console-icon" onClick={() => navigate("logs", { q: model.public_alias })} aria-label="Open request logs" title="Open request logs"><FileClock size={14} aria-hidden="true" /></button></div></header>
    <form className="playground-settings-form" onSubmit={(event) => { event.preventDefault(); void save(); }}>
      <ModelFields value={draft} onChange={setDraft} />
      <div className="playground-settings-actions"><Button variant="danger" type="button" disabled={saving || deleting} onClick={() => void remove()}><Trash2 size={13} aria-hidden="true" /> {deleting ? "Deleting…" : "Delete"}</Button><Button type="submit" disabled={saving || deleting}>{saving ? "Saving…" : "Save settings"}</Button></div>
    </form>
  </>;
}

type ModelProbeResult = {
  state: "checking" | "passed" | "warning" | "blocked" | "failed";
  error?: string;
};

function ModelsPage({
  navigate,
  notify
}: {
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [credentialsOpen, setCredentialsOpen] = useState(false);
  const [modelInspectorOpen, setModelInspectorOpen] = useState(false);
  // Below 900px the inspector covers the route list, so it takes focus, keeps Tab
  // inside itself and closes on Escape like any other overlay.
  const inspectorFloating = useMediaQuery("(max-width: 900px)");
  const modelDrawer = useDrawerOverlay({
    open: modelInspectorOpen,
    active: inspectorFloating,
    onClose: () => setModelInspectorOpen(false)
  });
  const [probeResults, setProbeResults] = useState<Record<string, ModelProbeResult>>({});
  const [probeProgress, setProbeProgress] = useState({ completed: 0, total: 0, passed: 0, listed: 0, blocked: 0, failed: 0 });
  const [bulkChecking, setBulkChecking] = useState(false);
  // What the live region says, as opposed to the counter the eye reads. A sweep of
  // two hundred routes announced every single one; this speaks at quarters.
  const [sweepNote, setSweepNote] = useState("");
  // A sweep issues one request per route and there is no way to abort them all, so
  // it is stopped between routes: the loop checks this before taking the next one.
  const stopSweep = useRef(false);
  // Set false on unmount so the sweep stops writing to state and raising toasts for
  // a page the operator has already left.
  const mounted = useRef(true);
  useEffect(() => () => {
    mounted.current = false;
    stopSweep.current = true;
  }, []);
  const [deletingFailed, setDeletingFailed] = useState(false);
  const [rechecking, setRechecking] = useState(false);
  const [deletingRoute, setDeletingRoute] = useState(false);
  const [selectedID, setSelectedID] = useState(() => new URLSearchParams(location.search).get("model") || "");
  const routingMode = useRoutingMode();
  // Loads are versioned so a slow reply cannot overwrite a newer one. A route
  // deleted from the inspector triggers an immediate reload while a background one
  // is still in flight; without this the older list wins and the deleted route is
  // back on screen and re-selected.
  const generation = useRef(0);
  // Only the first load blanks the page. A reload after a probe or a delete keeps
  // the list and the open inspector in place, because replacing the workbench
  // with a skeleton throws away the operator's position mid-task.
  const load = useCallback(async (background = false) => {
    const mine = ++generation.current;
    if (!background) setLoading(true);
    try {
      const result = await api<{ providers: Provider[] }>("/api/admin/providers");
      if (mine !== generation.current) return;
      const normalized = normalizeProviders(result.providers);
      setProviders(normalized);
      setError("");
      const available = normalized.flatMap((provider) => provider.models);
      setSelectedID((current) => available.some((model) => model.id === current) ? current : available[0]?.id || "");
    } catch (caught) {
      if (mine !== generation.current) return;
      setError(errorMessage(caught));
      if (!background) notify(errorMessage(caught), "danger");
    } finally {
      if (mine === generation.current) setLoading(false);
    }
  }, [notify]);
  const reload = useCallback(() => load(true), [load]);
  useEffect(() => {
    void load();
  }, [load]);
  useEffect(() => setCredentialsOpen(false), [selectedID]);
  // The open route lives in the address bar for the same reason the provider does:
  // a reload, or a link pasted to someone else, comes back to the same route.
  useEffect(() => {
    if (!selectedID) return;
    const url = new URL(location.href);
    if (url.searchParams.get("model") === selectedID) return;
    url.searchParams.set("model", selectedID);
    history.replaceState(history.state, "", url);
  }, [selectedID]);
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
  const healthyKeyCount = (model: (typeof models)[number]) => model.credentials.filter((item) => credentialPoolState(item) === "healthy").length;
  const failedProbeIDs = models.filter((model) => healthyKeyCount(model) > 0 && (probeResults[model.id]?.state === "failed" || (!probeResults[model.id] && model.capability_status === "failed"))).map((model) => model.id);

  const checkAllModels = async () => {
    if (bulkChecking || models.length === 0) return;
    const targets = models.filter((model) => healthyKeyCount(model) > 0);
    const blockedModels = models.filter((model) => healthyKeyCount(model) === 0);
    setProbeResults(Object.fromEntries(models.map((model) => [model.id, healthyKeyCount(model) > 0
      ? { state: "checking" as const }
      : { state: "blocked" as const, error: "waiting for a healthy API key" }])));
    setProbeProgress({ completed: blockedModels.length, total: models.length, passed: 0, listed: 0, blocked: blockedModels.length, failed: 0 });
    setBulkChecking(true);
    stopSweep.current = false;
    setSweepNote(`Checking ${targets.length} model route${targets.length === 1 ? "" : "s"}.`);
    let cursor = 0;
    let completed = 0;
    let passed = 0;
    let listed = 0;
    let blocked = blockedModels.length;
    let failed = 0;
    // One announcement per probed model is 200 announcements on a real install,
    // which is a screen reader talking over itself for a minute. The readout keeps
    // ticking for the eye; the live region speaks at quarters and at the end.
    const milestone = Math.max(1, Math.ceil(targets.length / 4));
    const worker = async () => {
      while (cursor < targets.length) {
        if (stopSweep.current || !mounted.current) return;
        const model = targets[cursor++];
        try {
          const result = await api<{ warning?: string }>(`/api/admin/models/${model.id}/probe`, { method: "POST" });
          if (result.warning) {
            listed++;
            if (mounted.current) setProbeResults((current) => ({ ...current, [model.id]: { state: "warning", error: result.warning } }));
          } else {
            passed++;
            if (mounted.current) setProbeResults((current) => ({ ...current, [model.id]: { state: "passed" } }));
          }
        } catch (caught) {
          if (caught instanceof APIError && caught.code === "model_probe_blocked") {
            blocked++;
            if (mounted.current) setProbeResults((current) => ({ ...current, [model.id]: { state: "blocked", error: errorMessage(caught) } }));
          } else {
            failed++;
            if (mounted.current) setProbeResults((current) => ({ ...current, [model.id]: { state: "failed", error: errorMessage(caught) } }));
          }
        } finally {
          completed++;
          if (mounted.current) {
            setProbeProgress({ completed: completed + blockedModels.length, total: models.length, passed, listed, blocked, failed });
            if (completed % milestone === 0 && completed < targets.length) {
              setSweepNote(`${completed} of ${targets.length} checked.`);
            }
          }
        }
      }
    };
    await worker();
    // The page can be left while a sweep of two hundred routes is running, and
    // every write after that point is against a component that is gone.
    if (!mounted.current) return;
    setBulkChecking(false);
    const stopped = stopSweep.current;
    const summary = `${passed} model${passed === 1 ? "" : "s"} live${listed ? ` · ${listed} provider-listed` : ""} · ${blocked} waiting for keys${failed ? ` · ${failed} unavailable` : ""}.`;
    setSweepNote(stopped ? `Stopped after ${completed} of ${targets.length}. ${summary}` : summary);
    notify(
      stopped ? `Check stopped after ${completed} of ${targets.length} routes. ${summary}` : summary,
      failed ? "danger" : "success"
    );
    await load(true);
  };

  const deleteFailedModels = async () => {
    if (deletingFailed || failedProbeIDs.length === 0) return;
    const aliases = models.filter((model) => failedProbeIDs.includes(model.id)).map((model) => model.public_alias);
    // The dialog gets the whole list in a scrolling block rather than the twelve a
    // window.confirm() could fit: this is the one action that deletes many routes at
    // once, so the operator has to be able to read what goes.
    const confirmed = await ask({
      title: `Delete ${aliases.length} unavailable route${aliases.length === 1 ? "" : "s"}?`,
      body: "Requests using these aliases stop immediately, and the routes cannot be restored.",
      confirmLabel: `Delete ${aliases.length} route${aliases.length === 1 ? "" : "s"}`,
      detail: aliases.join("\n")
    });
    if (!confirmed) return;
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
    await load(true);
  };

  return (
    <div className="resource-page model-page">
      <PageHeader eyebrow="Public contract" title="Model routes" description={routingMode === "model" ? "Model-wise routing: aliases sharing a name are served as one pool, rotating across providers and API keys. Callers see the name once." : "Inspect aliases, apply one model limit to one or every API key, and open only the credential detail you need."} />
      {loading ? <PageSkeleton /> : error && models.length === 0 ? (
        <EmptyState
          level={2}
          title="Model routes could not be loaded"
          description={error}
          action={<Button onClick={() => void load()}><RefreshCw size={14} aria-hidden="true" /> Try again</Button>}
        />
      ) : models.length === 0 ? (
        <EmptyState
          level={2}
          title="No model routes yet"
          description="A model route is the alias your callers ask for. Add one inside a provider and it appears in /v1/models."
          action={<Button onClick={() => navigate("providers")}><ArrowRight size={14} aria-hidden="true" /> Go to providers</Button>}
        />
      ) : (
        <div className="ide-resource-workbench">
          {error && <InlineNotice tone="danger">{error} Showing the last data that loaded.</InlineNotice>}
          <section className="ide-resource-list" inert={inspectorFloating && modelInspectorOpen && Boolean(selected)}>
            <div className="ide-filter model-filter">
              <Search size={14} aria-hidden="true" />
              <label><span className="sr-only">Filter model routes</span><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter aliases or upstream IDs" /></label>
              <label><span className="sr-only">Provider</span><select value={providerFilter} onChange={(event) => setProviderFilter(event.target.value)}><option value="">All providers</option>{providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}</select></label>
            </div>
            <section className={`model-sweep${bulkChecking ? " is-running" : ""}`}>
              {/* The counter is for the eye and changes once per route, so it is
                  hidden from the live region; the region carries the milestone
                  sentence instead. */}
              <div className="model-sweep__readout" aria-hidden="true">
                <span>Live model sweep</span>
                <strong>{bulkChecking ? `${probeProgress.completed}/${probeProgress.total} checked` : probeProgress.total ? `${probeProgress.passed} live${probeProgress.listed ? ` · ${probeProgress.listed} listed` : ""} · ${probeProgress.blocked} waiting${probeProgress.failed ? ` · ${probeProgress.failed} unavailable` : ""}` : `${models.filter((model) => healthyKeyCount(model) > 0).length} routes ready · ${models.filter((model) => healthyKeyCount(model) === 0).length} waiting for keys`}</strong>
              </div>
              <p className="sr-only" role="status" aria-live="polite">{sweepNote}</p>
              <div className="model-sweep__track" role="progressbar" aria-label="Model check progress" aria-valuemin={0} aria-valuemax={probeProgress.total || models.length} aria-valuenow={probeProgress.completed}>
                <span style={{ width: `${probeProgress.total ? (probeProgress.completed / probeProgress.total) * 100 : 0}%` }} />
              </div>
              <div className="button-row">
                {failedProbeIDs.length > 0 && <Button variant="danger" disabled={bulkChecking || deletingFailed} onClick={() => void deleteFailedModels()}><Trash2 size={13} aria-hidden="true" /> {deletingFailed ? "Deleting…" : `Delete ${failedProbeIDs.length} failed route${failedProbeIDs.length === 1 ? "" : "s"}`}</Button>}
                {/* A sweep of every route takes minutes and used to have no way out
                    but a page reload, which threw away the results already in. */}
                {bulkChecking ? (
                  <Button variant="quiet" onClick={() => { stopSweep.current = true; }}>Stop checking</Button>
                ) : (
                  <Button variant="quiet" disabled={deletingFailed} onClick={() => void checkAllModels()}><Activity size={13} aria-hidden="true" /> Check all models</Button>
                )}
              </div>
            </section>
            {/* Column labels for the eye only: each row below is a button, not a
                table row, so the labels cannot be associated as headers. Every
                row states its own values in its accessible name instead. */}
            <header aria-hidden="true"><span>Public alias</span><span>Provider</span><span>Keys</span></header>
            <div>
              {filtered.map((model) => {
                const ready = model.credentials.filter((item) => item.enabled && item.status === "healthy").length;
                const probe = probeResults[model.id];
                const capabilityLabel = ready === 0 ? "waiting for a healthy API key" : probe?.state === "checking" ? "checking now" : probe?.state === "passed" ? "live" : probe?.state === "warning" ? `listed by provider · check inconclusive: ${probe.error || "try a request to use another key"}` : probe?.state === "blocked" ? `waiting · ${probe.error || "healthy API key required"}` : probe?.state === "failed" ? `unavailable · ${probe.error || "probe failed"}` : model.capability_status === "probe_verified" ? "probe verified" : model.capability_status === "catalog_verified" ? "catalog verified" : model.capability_status === "failed" ? `unavailable · ${model.capability_error || "probe failed"}` : "unverified";
                const pooled = routingMode === "model" && poolSizes[model.public_alias] > 1;
                return (
                  <button
                    key={model.id}
                    className={selectedID === model.id ? "is-selected" : ""}
                    onClick={() => { setSelectedID(model.id); setModelInspectorOpen(true); }}
                    aria-current={selectedID === model.id}
                    aria-label={`${model.public_alias}, ${capabilityLabel}, on ${model.provider.name}${pooled ? ` in a pool of ${poolSizes[model.public_alias]} providers` : ""}, ${ready} of ${model.credentials.length} key${model.credentials.length === 1 ? "" : "s"} ready`}
                  >
                    <span aria-hidden="true"><StatusDot state={probe?.state === "passed" || probe?.state === "warning" ? "healthy" : !model.enabled || ready === 0 || probe?.state === "blocked" ? "disabled" : probe?.state === "failed" || model.capability_status === "failed" ? "exhausted" : "healthy"} /><code title={model.public_alias}>{model.public_alias}</code><small title={`${model.upstream_model === model.public_alias ? (model.supports_responses ? "Chat + Responses" : "Chat Completions") : model.upstream_model} · ${capabilityLabel}`}>{model.upstream_model === model.public_alias ? (model.supports_responses ? "Chat + Responses" : "Chat Completions") : model.upstream_model} · {capabilityLabel}</small></span>
                    <span aria-hidden="true" title={pooled ? `${model.provider.name} · pool of ${poolSizes[model.public_alias]}` : model.provider.name}>{pooled ? `${model.provider.name} · pool of ${poolSizes[model.public_alias]}` : model.provider.name}</span>
                    <span aria-hidden="true">{ready}/{model.credentials.length}</span>
                    <ChevronRight size={13} aria-hidden="true" />
                  </button>
                );
              })}
            </div>
          </section>
          {selected && (
            <aside className={`ide-resource-inspector${modelInspectorOpen ? " is-open" : ""}`} ref={modelDrawer as React.Ref<HTMLElement>} tabIndex={-1}>
              <header className="ide-inspector-titlebar">
                <div><span>Model route</span><h2 title={selected.public_alias}>{selected.public_alias}</h2><code title={selected.upstream_model}>{selected.upstream_model}</code></div>
                <div className="button-row"><button className="console-icon resource-inspector-close" onClick={() => setModelInspectorOpen(false)} aria-label="Close model inspector"><X size={15} aria-hidden="true" /></button><Button variant="quiet" disabled={rechecking || deletingRoute} onClick={() => {
                  setRechecking(true);
                  void api<{ warning?: string }>(`/api/admin/models/${selected.id}/probe`, { method: "POST" })
                    .then((result) => {
                      notify(result.warning
                        ? "The model is listed by the provider. Its live check was inconclusive, so the route remains available."
                        : "Model check passed.");
                      return reload();
                    })
                    .catch((caught) => { notify(errorMessage(caught), "danger"); return reload(); })
                    .finally(() => setRechecking(false));
                }}><Activity size={13} aria-hidden="true" /> {rechecking ? "Checking…" : "Check model"}</Button><Button variant="danger" disabled={rechecking || deletingRoute} onClick={() => {
                  void (async () => {
                    if (!(await ask(deleteRouteQuestion(selected.public_alias)))) return;
                    setDeletingRoute(true);
                    try {
                      await api(`/api/admin/models/${selected.id}`, { method: "DELETE" });
                      notify(`Route ${selected.public_alias} deleted.`);
                      await reload();
                    } catch (caught) {
                      notify(errorMessage(caught), "danger");
                    } finally {
                      setDeletingRoute(false);
                    }
                  })();
                }}><Trash2 size={13} aria-hidden="true" /> {deletingRoute ? "Deleting…" : "Delete route"}</Button></div>
              </header>
              <div className="inspector-definition">
                <Definition label="Provider" value={selected.provider.name} />
                {routingMode === "model" && poolSizes[selected.public_alias] > 1 && <Definition label="Pooled with" value={`${poolSizes[selected.public_alias] - 1} other provider route${poolSizes[selected.public_alias] === 2 ? "" : "s"}`} />}
                <Definition label="Route" value={selected.enabled ? "On" : "Off"} />
                <Definition label="Capability check" value={healthyKeyCount(selected) === 0 ? "Waiting for a healthy API key" : capabilityLabelFor(selected.capability_status)} />
                <Definition label="Chat endpoint" value={protocolLabelFor(selected.capability_profile?.chat || (selected.supports_chat ? "native" : "off"))} />
                <Definition label="Responses" value={protocolLabelFor(selected.capability_profile?.responses || (selected.supports_responses ? "native" : "translated"))} />
                <Definition label="Messages" value={protocolLabelFor(selected.capability_profile?.messages || (selected.supports_messages ? "native" : "off"))} />
                <Definition label="Streaming" value={protocolLabelFor(selected.capability_profile?.streaming)} />
                <Definition label="Tools" value={protocolLabelFor(selected.capability_profile?.tools)} />
                <Definition label="Thinking" value={protocolLabelFor(selected.capability_profile?.thinking)} />
                <Definition label="Output ceiling" value={`${selected.default_max_output_tokens} tokens`} />
                <Definition label="Tokenizer" value={selected.tokenizer} mono />
              </div>
              {selected.strip_parameters.length > 0 && <InlineNotice>Removes unsupported fields: <code>{selected.strip_parameters.join(", ")}</code></InlineNotice>}
              <ModelLimitEditor model={selected} credentials={selected.credentials} notify={notify} onSaved={reload} />
              <Rotor
                keys={selected.credentials.map((credential) => ({
                  id: credential.id,
                  status: credentialPoolState(credential)
                }))}
                stalled={selected.credentials.length > 0 && !selected.credentials.some((credential) => credentialPoolState(credential) === "healthy")}
                stalledNote="No key can serve this route. Open its provider to check the keys."
              />
              <section className={`ide-inspector-section inspector-disclosure${credentialsOpen ? " is-open" : ""}`}>
                <button type="button" onClick={() => setCredentialsOpen((current) => !current)} aria-expanded={credentialsOpen}>
                  <ChevronDown size={14} aria-hidden="true" /><span><strong>Key order</strong><small>{selected.credentials.length} API key{selected.credentials.length === 1 ? "" : "s"}</small></span>
                </button>
                {credentialsOpen && <div className="inspector-disclosure__body">{selected.credentials.map((credential) => (
                  <div key={credential.id} className={credential.validation_error ? "has-warning" : ""}>
                    <StatusDot state={credentialPoolState(credential)} />
                    <strong title={credential.label}>{credential.label}</strong>
                    <small>{credential.is_primary ? "Primary" : statusLabel(credentialPoolState(credential))}</small>
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
  const ask = useConfirm();
  const [target, setTarget] = useState("all");
  const [draft, setDraft] = useState<RatePolicy>(emptyPolicy());
  const [busy, setBusy] = useState(false);
  const targetCredential = credentials.find((credential) => credential.id === target);
  const hasDraftLimit = Object.values(draft).some((value) => value !== null);
  // The Models page reloads in the background, which hands this editor a fresh
  // credentials array every time. Reading it through a ref keeps the draft from
  // being reset mid-edit by a reload the operator did not ask for.
  const credentialsRef = useRef(credentials);
  credentialsRef.current = credentials;
  useEffect(() => {
    setTarget("all");
    setDraft(emptyPolicy());
  }, [model.id]);
  useEffect(() => {
    if (target === "all") {
      setDraft(emptyPolicy());
      return;
    }
    const chosen = credentialsRef.current.find((credential) => credential.id === target);
    setDraft(chosen?.model_limits[model.id] ?? emptyPolicy());
  }, [target, model.id]);
  const targets = target === "all" ? credentials : targetCredential ? [targetCredential] : [];
  const scope = target === "all"
    ? `all ${credentials.length} API key${credentials.length === 1 ? "" : "s"}`
    : targetCredential?.label ?? "this API key";
  // "All API keys" is the default, so both buttons rewrite the whole pool on one
  // click unless the operator confirms the blast radius first.
  const confirmFanOut = async (question: Omit<ConfirmRequest, "tone">) =>
    target !== "all" || (await ask({ ...question, tone: "primary" }));
  const save = async () => {
    if (targets.length === 0) return;
    if (!(await confirmFanOut({
      title: `Save this limit on ${scope}?`,
      body: `Any limit already set for ${model.public_alias} on those keys is replaced. Shared provider limits still apply.`,
      confirmLabel: "Save model limit"
    }))) return;
    setBusy(true);
    try {
      await Promise.all(targets.map((credential) => api(`/api/admin/credentials/${credential.id}/model-limits/${model.id}`, { method: "PUT", json: draft })));
      notify(`Model limit saved on ${targets.length} API key${targets.length === 1 ? "" : "s"}.`);
      await onSaved();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };
  const clear = async () => {
    if (targets.length === 0) return;
    if (!(await confirmFanOut({
      title: `Use the shared limit on ${scope}?`,
      body: `This model's own limit is removed from them, and ${model.public_alias} falls back to each key's shared limit.`,
      confirmLabel: "Use shared limit"
    }))) return;
    setBusy(true);
    try {
      await Promise.all(targets.map((credential) => api(`/api/admin/credentials/${credential.id}/model-limits/${model.id}`, { method: "DELETE" })));
      setDraft(emptyPolicy());
      notify(`Shared limit in use on ${targets.length} API key${targets.length === 1 ? "" : "s"}.`);
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
      <div className="button-row"><Button disabled={busy || targets.length === 0 || !hasDraftLimit} onClick={() => void save()}>{busy ? "Saving…" : "Save model limit"}</Button><Button variant="quiet" disabled={busy || targets.length === 0} onClick={() => void clear()}>Use shared limit</Button></div>
    </section>
  );
}

function LogsPage() {
  const [logs, setLogs] = useState<RequestLog[]>([]);
  const initialParams = useMemo(() => new URLSearchParams(location.search), []);
  const [query, setQuery] = useState(() => initialParams.get("q") || "");
  const [status, setStatus] = useState(() => initialParams.get("status") || "");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  // Only the id is state. Holding the whole record meant the two-second poll
  // handed back a new object every tick, which re-rendered the inspector and
  // re-fired the body decrypt thirty times a minute.
  const [selectedID, setSelectedID] = useState<string | null>(null);
  const [bodies, setBodies] = useState<{ request: string | null; response: string | null } | null>(null);
  const [bodiesError, setBodiesError] = useState("");
  const [bodyAttempt, setBodyAttempt] = useState(0);
  const [attemptsOpen, setAttemptsOpen] = useState(false);
  const [bodiesOpen, setBodiesOpen] = useState(false);
  const [logInspectorOpen, setLogInspectorOpen] = useState(false);
  // The list is the newest 100 rows and it reloads every two seconds, so on a busy
  // gateway the request being read scrolls out of the window while it is open. The
  // inspector used to close itself at that moment, mid-sentence. It now keeps the
  // last copy of the record it was showing and says the row has left the list, so
  // reading a failure is not a race against traffic.
  const [detached, setDetached] = useState<RequestLog | null>(null);
  // Below 900px the inspector covers the log list, so it behaves as an overlay:
  // focus moves in, Tab stays inside, Escape closes, focus returns to the row.
  const inspectorFloating = useMediaQuery("(max-width: 900px)");
  const logDrawer = useDrawerOverlay({
    open: logInspectorOpen,
    active: inspectorFloating,
    onClose: () => setLogInspectorOpen(false)
  });
  const generation = useRef(0);
  // Typing filters the list server-side, so the request is held back until the
  // operator pauses. Without the delay every keystroke was its own round trip
  // and a slow reply for "gpt" could land on top of the reply for "gpt-4".
  const [debouncedQuery, setDebouncedQuery] = useState(query);
  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query), 250);
    return () => window.clearTimeout(timer);
  }, [query]);
  // The list polls every two seconds, so a backend outage must not raise a toast
  // per tick. The banner states it once and the poll keeps trying quietly.
  const load = useCallback(async (silent = false) => {
    const mine = ++generation.current;
    if (!silent) setLoading(true);
    try {
      const result = await api<{ logs: RequestLog[] }>(`/api/admin/logs?limit=100&q=${encodeURIComponent(debouncedQuery)}&status=${encodeURIComponent(status)}`);
      if (mine !== generation.current) return;
      const normalized = (result.logs ?? []).map((log) => ({
        ...log,
        attempts: log.attempts ?? [],
        routing_decisions: log.routing_decisions ?? [],
      }));
      setLogs(normalized);
      setError("");
    } catch (caught) {
      if (mine !== generation.current) return;
      setError(errorMessage(caught));
    } finally {
      if (!silent && mine === generation.current) setLoading(false);
    }
  }, [debouncedQuery, status]);
  // The poll reads the newest `load` from a ref, so the two-second cadence is not
  // torn down and restarted on every keystroke.
  const loadRef = useRef(load);
  loadRef.current = load;
  useEffect(() => {
    void load();
  }, [load]);
  // Changing a filter is a request for a different list, so the open request is let
  // go rather than held as one that aged out — it did not age out, it stopped
  // matching what was asked for.
  useEffect(() => {
    setSelectedID(null);
    setDetached(null);
    setLogInspectorOpen(false);
  }, [debouncedQuery, status]);
  useEffect(() => {
    const timer = window.setInterval(() => void loadRef.current(true), 2000);
    return () => {
      window.clearInterval(timer);
      generation.current += 1;
    };
  }, []);
  const listed = useMemo(() => logs.find((log) => log.id === selectedID) ?? null, [logs, selectedID]);
  // A row that aged out is still shown, from the copy kept when it was last in the
  // list. `detached` is only consulted for the id currently selected, so it cannot
  // resurrect a previous selection.
  const selected = listed ?? (detached && detached.id === selectedID ? detached : null);
  const agedOut = selected !== null && listed === null;
  useEffect(() => {
    if (listed) setDetached(listed);
  }, [listed]);
  useEffect(() => {
    const params = new URLSearchParams();
    if (query) params.set("q", query);
    if (status) params.set("status", status);
    history.replaceState({}, "", `/admin/logs${params.size ? `?${params}` : ""}`);
  }, [query, status]);
  useEffect(() => {
    setBodies(null);
    setBodiesError("");
    setAttemptsOpen(Boolean(selected && selected.status_code >= 400 && selected.attempts.length));
    setBodiesOpen(false);
  }, [selectedID]);
  useEffect(() => {
    if (!selectedID || !bodiesOpen) return;
    let ignore = false;
    void api<{ request: string | null; response: string | null }>(`/api/admin/logs/${selectedID}/bodies`)
      .then((result) => {
        if (ignore) return;
        setBodies(result);
        setBodiesError("");
      })
      .catch((caught) => {
        if (!ignore) setBodiesError(errorMessage(caught));
      });
    return () => { ignore = true; };
  }, [selectedID, bodiesOpen, bodyAttempt]);

  return (
    <div className="resource-page log-page">
      <PageHeader eyebrow="Live request evidence" title="Request logs" description="Running requests update every two seconds. Completed metadata is retained; bodies appear only where encrypted capture is enabled." actions={<Button variant="quiet" onClick={() => void load()}><RefreshCw size={15} aria-hidden="true" /> Refresh</Button>} />
      {error && (
        <InlineNotice tone="danger">
          {error} The list keeps retrying every two seconds.{" "}
          <button className="link-button" onClick={() => void load()}>Try again</button>
        </InlineNotice>
      )}
      <div className="ide-resource-workbench log-workbench">
        <section className="ide-resource-list" aria-label="Request logs" inert={inspectorFloating && logInspectorOpen && Boolean(selected)}>
          <div className="ide-filter log-filter">
            <Search size={14} aria-hidden="true" />
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
          {/* Column labels for the eye only. Each row below is a button, not a
              table row, so the labels cannot be associated as real headers —
              instead every row states its own values in its accessible name. */}
          <header aria-hidden="true"><span>Request</span><span>Model / route</span><span>Status</span><span>Latency</span><span /></header>
          <div className="log-resource-rows">
            {loading ? (
              <PageSkeleton />
            ) : logs.length === 0 ? (
              query || status ? (
                <EmptyState
                  level={2}
                  title="No requests match these filters"
                  description="Nothing in the retained window matches the search or status you picked."
                  action={<Button variant="quiet" onClick={() => { setQuery(""); setStatus(""); }}>Clear filters</Button>}
                />
              ) : (
                <EmptyState level={2} title="No requests yet" description="Send a request through /v1 and it appears here within two seconds." />
              )
            ) : logs.map((log) => (
              <button
                key={log.id}
                className={`${selectedID === log.id ? "is-selected" : ""}${log.running ? " is-running" : ""}`}
                aria-current={selectedID === log.id}
                aria-label={`${log.model_alias} via ${log.provider_name}, ${log.running ? "still running" : `HTTP ${log.status_code}`}, ${log.latency_ms} ms, at ${formatClockTime(log.created_at)}`}
                onClick={() => { setSelectedID(log.id); setLogInspectorOpen(true); }}
              >
                <span aria-hidden="true">
                  <StatusDot state={log.running ? "unknown" : log.status_code >= 500 ? "exhausted" : log.status_code >= 400 ? "partial" : "healthy"} />
                  <strong>{formatClockTime(log.created_at)}</strong>
                  <small>{log.request_id}</small>
                </span>
                <span aria-hidden="true"><code title={log.model_alias}>{log.model_alias}</code><small title={`${log.provider_name} · ${routingStageLabel(log)}${log.error_code ? ` · ${log.error_code}` : ""}`}>{log.provider_name} · {routingStageLabel(log)}{log.error_code ? ` · ${log.error_code}` : ""}</small></span>
                <strong aria-hidden="true" className={`status-code status-code--${log.running ? "running" : log.status_code >= 500 ? "fault" : log.status_code >= 400 ? "warning" : "healthy"}`}>{log.running ? "Run" : log.status_code}</strong>
                <small aria-hidden="true">{log.latency_ms} ms</small>
                <ChevronRight size={13} aria-hidden="true" />
              </button>
            ))}
          </div>
        </section>
        {selected ? (
          <aside className={`ide-resource-inspector log-resource-inspector${logInspectorOpen ? " is-open" : ""}`} ref={logDrawer as React.Ref<HTMLElement>} tabIndex={-1}>
            <header className="ide-inspector-titlebar">
              <div><span>{selected.endpoint} · {selected.running ? "Running" : `HTTP ${selected.status_code}`}</span><h2 title={selected.request_id}>{selected.request_id}</h2><code title={selected.model_alias}>{selected.model_alias}</code></div>
              <div className="button-row"><strong className={`status-code status-code--${selected.running ? "running" : selected.status_code >= 500 ? "fault" : selected.status_code >= 400 ? "warning" : "healthy"}`}>{selected.running ? "Running" : selected.status_code}</strong><button className="console-icon resource-inspector-close" onClick={() => setLogInspectorOpen(false)} aria-label="Close log inspector"><X size={15} aria-hidden="true" /></button></div>
            </header>
            {/* Label-then-value pairs read in order, so the pairing is already clear;
                the list role is what gives the group a count and lets a screen
                reader step between the four rather than through loose text. */}
            <div className="log-detail-grid" role="list">
              <div role="listitem"><span>Provider</span><strong>{selected.provider_name}</strong></div>
              <div role="listitem"><span>Served by</span><strong>{routingStageLabel(selected)}</strong></div>
              <div role="listitem"><span>Latency</span><strong>{selected.latency_ms} ms</strong></div>
              <div role="listitem"><span>Tokens</span><strong title={`${selected.input_tokens} input · ${selected.output_tokens} output`}>{formatCompact(selected.input_tokens)} in · {formatCompact(selected.output_tokens)} out</strong></div>
            </div>
            {agedOut && (
              <InlineNotice>
                This request has scrolled out of the newest 100, so it is no longer in the list. What is shown here is the last reading of it.
              </InlineNotice>
            )}
            {selected.running ? <InlineNotice>Request is running. This inspector will switch to the final status automatically.</InlineNotice> : selected.status_code >= 400 && <LogDiagnosis log={selected} />}
            <section className={`inspector-disclosure log-disclosure${attemptsOpen ? " is-open" : ""}`}>
              <button type="button" onClick={() => setAttemptsOpen((current) => !current)} aria-expanded={attemptsOpen}><ChevronDown size={14} aria-hidden="true" /><span><strong>Routing attempts</strong><small>{selected.attempts.length} recorded</small></span></button>
              {attemptsOpen && <div className="attempt-list">{selected.attempts.length ? selected.attempts.map((attempt, index) => <div key={`${attempt.credential_id}-${index}`}><span>{index + 1}</span><strong>{attempt.credential_label}</strong><code>{attemptSummary(attempt)}</code><small>{attempt.duration_ms} ms</small>{attempt.error_message && <p>{attempt.error_message}</p>}</div>) : <p className="console-empty">No upstream attempt was needed.</p>}</div>}
            </section>
            {!selected.running && selected.body_captured ? (
              <section className={`inspector-disclosure log-disclosure${bodiesOpen ? " is-open" : ""}`}>
                <button type="button" onClick={() => setBodiesOpen((current) => !current)} aria-expanded={bodiesOpen}><ChevronDown size={14} aria-hidden="true" /><span><strong>Encrypted bodies</strong><small>{selected.body_truncated ? "captured · truncated" : "captured"}</small></span></button>
                {bodiesOpen && (bodiesError ? (
                  <InlineNotice tone="danger">{bodiesError}{" "}<button className="link-button" onClick={() => { setBodiesError(""); setBodyAttempt((n) => n + 1); }}>Try again</button></InlineNotice>
                ) : (
                  <div className="body-grid"><section><h3>Request {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies ? bodies.request ?? "No captured request body." : "Decrypting…"}</pre></section><section><h3>Response {selected.body_truncated && <small>truncated</small>}</h3><pre>{bodies ? bodies.response ?? "No captured response body." : "Decrypting…"}</pre></section></div>
                ))}
              </section>
            ) : !selected.running && <InlineNotice>Body capture was off for this model route. Only metadata is available.</InlineNotice>}
          </aside>
        ) : (
          <aside className="ide-resource-inspector ide-resource-inspector--empty">
            <FileClock size={20} aria-hidden="true" /><strong>Select a request</strong><p>Inspect routing attempts, token usage, latency and optional encrypted bodies.</p>
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
        <AlertTriangle size={15} aria-hidden="true" />
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
            <small>{decision.reset_at ? <>reset <LiveResetTime value={decision.reset_at} /></> : decision.scope ? limitScopeLabel(decision.scope) : "routing"}</small>
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

/** The same question is asked from the edit panel and from the models inspector. It
 *  was written out twice, so a change to one wording silently disagreed with the
 *  other about what deleting a route does. */
function deleteRouteQuestion(alias: string) {
  return {
    title: `Delete route ${alias}?`,
    body: "Requests using this alias stop immediately, and the route cannot be restored — you would add it again from scratch.",
    confirmLabel: "Delete route"
  } as const;
}

/** What the console says when a provider turns a key away. Three call sites showed
 *  this — the credential panel, the provider wizard and the overview inspector —
 *  and all three used to say only "API key validation failed", which names the
 *  step that failed and no way out of it. */
const keyCheckFailed =
  "The provider rejected this key. Replace it, or check it again if you have just created it.";

/** Loading a provider's catalog fails for one of two reasons and the operator can
 *  act on both, so both are named. The two call sites — the load-models panel's
 *  toast and its notice — used to word this differently, which read as two
 *  different failures. */
const discoveryFailed =
  "The provider did not return a model list. Check the API key and the base URL, then try again.";

/** Copying can be refused by the browser — an insecure origin, or a denied
 *  permission — and the operator still needs the value, so the message ends with the
 *  way to get it. Four call sites copy something; only one of them used to say this
 *  much. */
const clipboardBlocked = "Your browser blocked the copy. Select the text and copy it manually.";

/** The capability enum comes off the route row as a database value —
 *  `probe_verified`, `catalog_verified`, `unverified` — and the inspector used to
 *  print it with the underscore swapped for a space, which told the operator how
 *  the column is stored rather than what is known about the route. */
const capabilityPhrase: Record<string, string> = {
  probe_verified: "Checked live",
  catalog_verified: "Listed by the provider",
  failed: "Unavailable",
  unverified: "Not checked yet"
};

/** The per-protocol capability enum, same reasoning. "translated" on its own does
 *  not say who translates or that the call still works. */
const protocolPhrase: Record<string, string> = {
  native: "Native",
  translated: "Translated by the gateway",
  off: "Off",
  unknown: "Not checked yet"
};

function capabilityLabelFor(value: string | undefined) {
  return capabilityPhrase[value ?? "unverified"] ?? capabilityPhrase.unverified;
}

function protocolLabelFor(value: string | undefined) {
  return protocolPhrase[value ?? "unknown"] ?? protocolPhrase.unknown;
}

/** One attempt's outcome as a single line. An attempt that never reached a status
 *  code carries only an error, and the old expression printed it on both sides of
 *  the separator — "connection_error · connection_error". replaced_parameters was
 *  truthy-checked as an object, so an empty map left a dangling "· replaced". */
function attemptSummary(attempt: RequestLog["attempts"][number]) {
  const parts: string[] = [];
  if (attempt.status_code) parts.push(String(attempt.status_code));
  if (attempt.error) parts.push(attempt.error);
  if (parts.length === 0) parts.push("no response");
  if (attempt.removed_parameters?.length) parts.push(`removed ${attempt.removed_parameters.join(", ")}`);
  const replaced = Object.entries(attempt.replaced_parameters ?? {});
  if (replaced.length) parts.push(`replaced ${replaced.map(([from, to]) => `${from} → ${to}`).join(", ")}`);
  if (attempt.switched_endpoint) parts.push(`switched to /${attempt.switched_endpoint}`);
  return parts.join(" · ");
}

/** What served the request, which is usually an API key but not always: a call the
 *  gateway rejected on its own never reached one. The inspector labels this row
 *  "Served by" rather than "API key" because two of the three fallbacks below are
 *  not keys, and a label that names the wrong thing is worse than a general one. */
function routingStageLabel(log: RequestLog) {
  if (log.credential_label) return log.credential_label;
  if (log.provider_name === "gateway") return "Gateway validation";
  if (log.attempts?.length) return "No key was reached";
  return "Rejected before routing";
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
    responses_endpoint_missing: "This provider has no Responses endpoint, so the request was retried as Chat Completions. Turn off \"Responses natively\" on this route to skip the extra attempt.",
  };
  return messages[code] || code.replaceAll("_", " ");
}

function formatDuration(milliseconds: number) {
  if (milliseconds < 60_000) return `${Math.max(1, Math.ceil(milliseconds / 1_000))}s`;
  if (milliseconds < 3_600_000) return `${Math.ceil(milliseconds / 60_000)}m`;
  return `${Math.ceil(milliseconds / 3_600_000)}h`;
}

function AccessPage({ gatewayKey, onNewKey, notify }: { gatewayKey: string; onNewKey: (key: string) => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const ask = useConfirm();
  const [settings, setSettings] = useState<Settings | null>(null);
  const [error, setError] = useState("");
  const [codexReady, setCodexReady] = useState<number | null>(null);
  // A gateway that is down and a fresh install with no routes both used to leave
  // `codexReady` null, so the heading read "Set up Codex" either way and the
  // operator had no way to tell which. The failure is now its own state, and it is
  // retried by the same Try again button as the settings load.
  const [codexError, setCodexError] = useState(false);
  const [rotating, setRotating] = useState(false);
  const [attempt, setAttempt] = useState(0);
  // Both loads are one-shot and both write state. Navigating away mid-flight, or
  // pressing Try again twice, otherwise resolves into an unmounted component or
  // lets the older reply win.
  useEffect(() => {
    let ignore = false;
    void api<Settings>("/api/admin/settings")
      .then((value) => { if (ignore) return; setSettings(value); setError(""); })
      .catch((caught) => { if (!ignore) setError(errorMessage(caught)); });
    return () => { ignore = true; };
  }, [gatewayKey, attempt]);
  useEffect(() => {
    let ignore = false;
    void api<Overview>("/api/admin/overview?range=1h")
      .then((value) => { if (ignore) return; setCodexReady(safeNumber(value.summary?.routes_ready)); setCodexError(false); })
      .catch(() => { if (!ignore) { setCodexReady(null); setCodexError(true); } });
    return () => { ignore = true; };
  }, [attempt]);
  const rotateKey = () => {
    if (rotating) return;
    void (async () => {
      const confirmed = await ask({
        title: "Rotate the gateway key now?",
        body: "The current key stops working immediately, and every client using it starts failing until you paste the new one. The new key is shown only once — if you close that panel without saving it, it cannot be recovered.",
        confirmLabel: "Rotate gateway key"
      });
      if (!confirmed) return;
      setRotating(true);
      try {
        const result = await api<{ gateway_key: string }>("/api/admin/access/rotate", { method: "POST" });
        onNewKey(result.gateway_key);
        notify("Gateway key rotated. Save the new key before closing the panel.");
      } catch (caught) {
        notify(errorMessage(caught), "danger");
      } finally {
        setRotating(false);
      }
    })();
  };
  const rootURL = (settings?.base_url || location.origin).replace(/\/$/, "");
  const openAIURL = `${rootURL}/v1`;
  return (
    <>
      <PageHeader eyebrow="Unified authentication" title="Gateway key" description="One active key works with OpenAI SDKs, Anthropic SDKs and Claude Code. Upstream provider secrets never leave Rotakey." />
      {error && (
        <InlineNotice tone="danger">
          {error} The examples below use this page's own address until the settings load.{" "}
          <button className="link-button" onClick={() => setAttempt((n) => n + 1)}>Try again</button>
        </InlineNotice>
      )}
      <section className="access-key-panel">
        <div className="key-prefix"><ShieldCheck size={20} aria-hidden="true" /><span><small>Current key prefix</small><code>{settings?.gateway_key_prefix ? `${settings.gateway_key_prefix}••••••••••••` : error ? "unavailable" : "Loading…"}</code></span></div>
        <div>
          <strong>Rotating the key</strong>
          <p>Rotation revokes the current key in the same database transaction and shows the replacement once. Update every application before you close that panel. Use the button at the bottom of this page.</p>
        </div>
      </section>
      <section className="section-block code-example">
        <div className="section-heading"><div><p className="eyebrow">OpenAI SDKs</p><h2>Chat Completions and Responses</h2></div><Button variant="quiet" onClick={() => void copyText(openAIURL).then(() => notify("OpenAI base URL copied.")).catch(() => notify(clipboardBlocked, "danger"))}><Clipboard size={14} aria-hidden="true" /> Copy base URL</Button></div>
        <pre>{`curl "${openAIURL}/chat/completions" \\
  -H "Authorization: Bearer $ROTAKEY_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"model":"provider/model-alias","messages":[{"role":"user","content":"Hello"}]}'`}</pre>
      </section>
      <section className="section-block code-example">
        <div className="section-heading">
          <div><p className="eyebrow">Codex CLI &amp; Desktop</p><h2>{codexError ? "Set up Codex" : codexReady === null ? "Set up Codex" : `${codexReady} model route${codexReady === 1 ? "" : "s"} ready`}</h2>{codexError && <p className="section-note">The route count is unavailable — the gateway did not answer. The setup command below still applies.</p>}</div>
          <div className="button-row">
            <a className="button button--quiet" href="https://github.com/jisunahamed/rotakey/blob/main/docs/CODEX.md" target="_blank" rel="noreferrer"><BookOpen size={14} aria-hidden="true" /> Full guide</a>
            <Button variant="quiet" onClick={() => void copyText(`rotakey-codex install --url ${rootURL}`).then(() => notify("Codex setup command copied.")).catch(() => notify(clipboardBlocked, "danger"))}><Clipboard size={14} aria-hidden="true" /> Copy setup</Button>
          </div>
        </div>
        <pre>{`rotakey-codex install --url ${rootURL}
rotakey-codex doctor
codex --profile rotakey`}</pre>
        <p>The installer prompts for the gateway key, protects it in the OS credential store, and writes only a managed Rotakey profile. Run <code>rotakey-codex sync</code> after changing model routes.</p>
      </section>
      <section className="section-block code-example">
        <div className="section-heading"><div><p className="eyebrow">Anthropic SDKs</p><h2>Messages SDK and Claude Code</h2></div><div className="button-row"><a className="button button--quiet" href="https://github.com/jisunahamed/rotakey/blob/main/docs/CLAUDE-CODE.md" target="_blank" rel="noreferrer"><BookOpen size={14} aria-hidden="true" /> Full guide</a><Button variant="quiet" onClick={() => void copyText(rootURL).then(() => notify("Anthropic base URL copied.")).catch(() => notify(clipboardBlocked, "danger"))}><Clipboard size={14} aria-hidden="true" /> Copy base URL</Button></div></div>
        <pre>{`export ANTHROPIC_BASE_URL="${rootURL}"
export ANTHROPIC_API_KEY="$ROTAKEY_KEY"

curl "${rootURL}/v1/messages" \\
  -H "x-api-key: $ROTAKEY_KEY" \\
  -H "anthropic-version: 2023-06-01" \\
  -H "content-type: application/json" \\
  -d '{"model":"provider/model-alias","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}'`}</pre>
      </section>
      <div className="page-actions console-actionbar">
        <span>Rotating revokes the current gateway key immediately and cannot be undone.</span>
        <Button variant="danger" disabled={rotating} onClick={rotateKey}><RefreshCw size={15} aria-hidden="true" /> {rotating ? "Rotating…" : "Rotate gateway key"}</Button>
      </div>
    </>
  );
}

function SettingsPage({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const ask = useConfirm();
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loadedMode, setLoadedMode] = useState<RoutingMode | null>(null);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [error, setError] = useState("");
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    // Try again refires this while the previous pair may still be open; the guard
    // keeps the abandoned reply from landing on top of the newer one.
    let ignore = false;
    void Promise.all([api<Settings>("/api/admin/settings"), api<{ providers: Provider[] }>("/api/admin/providers")])
      .then(([nextSettings, result]) => {
        if (ignore) return;
        // Providers only feed the two dropdown lists, so they always refresh. The
        // settings themselves are the form: once a value is on screen the operator
        // may be part-way through changing it, and a late reply must not type over
        // them. Try again only appears while the form is still empty, so it is
        // unaffected.
        setSettings((current) => current ?? nextSettings);
        setLoadedMode((current) => current ?? nextSettings.routing_mode);
        setProviders(normalizeProviders(result.providers));
        setError("");
      })
      .catch((caught) => { if (!ignore) setError(errorMessage(caught)); });
    return () => { ignore = true; };
  }, [attempt]);
  // A failed load used to leave an animating skeleton with no explanation, so the
  // page looked permanently busy rather than broken.
  if (!settings) {
    return error ? (
      <>
        <PageHeader eyebrow="Control plane policy" title="Settings" description="Bound waiting and retention so the gateway stays predictable on a small VPS." />
        <EmptyState
          level={2}
          title="Settings could not be loaded"
          description={error}
          action={<Button onClick={() => setAttempt((n) => n + 1)}><RefreshCw size={14} aria-hidden="true" /> Try again</Button>}
        />
      </>
    ) : (
      <PageSkeleton />
    );
  }
  const modeChanged = loadedMode !== null && settings.routing_mode !== loadedMode;
  const aliasesAtRisk = providers.reduce((total, provider) => total + provider.models.length, 0);
  // The number inputs carry min and max, but nothing on this page is a <form>, so
  // the browser never runs constraint validation on them: clearing the timeout field
  // made Number("") zero and posted a zero-second timeout that then became the
  // default for every new provider. Each field is checked here instead, against the
  // same bounds the inputs advertise.
  const settingsBounds: Array<[keyof Settings, string, number, number]> = [
    ["max_wait_ms", "Capacity wait ceiling", 0, 30_000],
    ["default_provider_timeout_seconds", "Global provider timeout", 1, 900],
    ["metadata_retention_days", "Metadata retention", 1, 3650],
    ["body_retention_days", "Captured body retention", 1, 365]
  ];
  const outOfBounds = settingsBounds.find(([key, , min, max]) => {
    const value = settings[key];
    return typeof value !== "number" || !Number.isFinite(value) || value < min || value > max;
  });
  const saveSettings = () => {
    if (outOfBounds) {
      const [, label, min, max] = outOfBounds;
      notify(`${label} must be between ${formatNumber(min)} and ${formatNumber(max)}.`, "danger");
      return;
    }
    void (async () => {
      // Switching mode rewrites every public alias in the same transaction, which
      // changes the name live callers ask for. That deserves a question first.
      if (modeChanged) {
        const confirmed = await ask({
          title: `Switch to ${settings.routing_mode}-wise routing?`,
          body: `Up to ${aliasesAtRisk} model alias${aliasesAtRisk === 1 ? "" : "es"} will be renamed, and clients calling the old names stop working until they are updated.`,
          confirmLabel: "Switch routing mode"
        });
        if (!confirmed) return;
      }
      setBusy(true);
      try {
        const result = await api<SettingsUpdateResult>("/api/admin/settings", { method: "PUT", json: settings });
        publishRoutingMode(result.routing_mode);
        setLoadedMode(result.routing_mode);
        // A mode switch renames aliases in the same transaction, so the save
        // confirmation reports what changed and what could not.
        const parts = ["Settings saved."];
        if (result.aliases_rewritten > 0) parts.push(`${result.aliases_rewritten} model alias${result.aliases_rewritten === 1 ? "" : "es"} renamed for ${result.routing_mode === "model" ? "model" : "provider"}-wise routing.`);
        if (result.alias_conflicts.length > 0) parts.push(`Kept unchanged to avoid a collision: ${result.alias_conflicts.join(", ")}.`);
        notify(parts.join(" "), result.alias_conflicts.length > 0 ? "danger" : "success");
      } catch (caught) {
        notify(errorMessage(caught), "danger");
      } finally {
        setBusy(false);
      }
    })();
  };
  return (
    <>
      <PageHeader eyebrow="Control plane policy" title="Settings" description="Bound waiting and retention so the gateway stays predictable on a small VPS." />
      <section className="settings-list">
        <label className="settings-row"><span><strong>Routing mode</strong><small>{settings.routing_mode === "model" ? "Model-wise: one alias pools every provider publishing that name, rotating across providers and keys. Saving strips the provider prefix from aliases that carry one." : "Provider-wise: each alias belongs to one provider. Saving prepends the provider slug to aliases that lack one."}</small></span><div><select value={settings.routing_mode} onChange={(event) => setSettings({ ...settings, routing_mode: event.target.value as RoutingMode })}><option value="provider">Provider-wise</option><option value="model">Model-wise (pooled)</option></select></div></label>
        <label className="settings-row"><span><strong>Default Anthropic resource provider</strong><small>Files have no model field, so uploads need one native Anthropic provider. Batches remain model-routed.</small></span><div><select value={settings.default_anthropic_provider_id || ""} onChange={(event) => setSettings({ ...settings, default_anthropic_provider_id: event.target.value })}><option value="">Not configured</option>{providers.filter((provider) => provider.api_format === "anthropic" && provider.enabled).map((provider) => {
          const noResourceAPIs = foundryHasNoResourceAPIs(provider);
          return <option key={provider.id} value={provider.id} disabled={noResourceAPIs}>{provider.name}{noResourceAPIs ? " — no Files or Batches" : ""}</option>;
        })}</select></div></label>
        <label className="settings-row"><span><strong>Capacity wait ceiling</strong><small>Requests wait only when capacity can return within this deadline.</small></span><div><input type="number" min={0} max={30000} step={100} value={settings.max_wait_ms} onChange={(e) => setSettings({ ...settings, max_wait_ms: Number(e.target.value) })} /><code>ms</code></div></label>
        <label className="settings-row"><span><strong>Global provider timeout</strong><small>Applies this request timeout to every provider and becomes the default for new providers.</small></span><div><input type="number" min={1} max={900} value={settings.default_provider_timeout_seconds} onChange={(e) => setSettings({ ...settings, default_provider_timeout_seconds: Number(e.target.value) })} /><code>seconds</code></div></label>
        <label className="settings-row"><span><strong>Metadata retention</strong><small>Request IDs, routing attempts, status, latency and usage.</small></span><div><input type="number" min={1} max={3650} value={settings.metadata_retention_days} onChange={(e) => setSettings({ ...settings, metadata_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
        <label className="settings-row"><span><strong>Captured body retention</strong><small>Only applies to model routes where encrypted body capture is enabled.</small></span><div><input type="number" min={1} max={365} value={settings.body_retention_days} onChange={(e) => setSettings({ ...settings, body_retention_days: Number(e.target.value) })} /><code>days</code></div></label>
      </section>
      <div className="page-actions console-actionbar">
        <span>{modeChanged ? "Saving a new routing mode renames existing model aliases." : "Changes apply to new requests without restarting the gateway."}</span>
        <Button disabled={busy} onClick={saveSettings}>{busy ? "Saving…" : "Save settings"}</Button>
      </div>
      <ConfigTransfer notify={notify} />
      <section className="security-baseline">
        <ShieldCheck size={19} aria-hidden="true" />
        <div><strong>Protections that are always on</strong><p>Provider API keys are stored encrypted, admin sessions use HttpOnly cookies with CSRF checks, and the gateway refuses to call private network addresses.</p></div>
      </section>
    </>
  );
}

// ConfigTransfer writes the whole provider, model, key and limit setup to one
// file and replays it. Keys are included by default so an import is a complete
// setup rather than a shell that still needs every secret pasted back in.
function ConfigTransfer({ notify }: { notify: (message: string, tone?: "success" | "danger") => void }) {
  const ask = useConfirm();
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

  // The file picker used to start the import the moment a file was chosen, so a
  // misclick could overwrite a working setup with no way back. The bundle is read
  // first, and the operator confirms against what it actually contains.
  const reviewThenImport = async (file: File) => {
    let summary = "";
    try {
      const bundle = JSON.parse(await file.text()) as {
        providers?: unknown[];
        models?: unknown[];
        credentials?: unknown[];
        routing_mode?: string;
      };
      const counts = [
        `${bundle.providers?.length ?? 0} provider${bundle.providers?.length === 1 ? "" : "s"}`,
        `${bundle.models?.length ?? 0} model route${bundle.models?.length === 1 ? "" : "s"}`,
        `${bundle.credentials?.length ?? 0} API key${bundle.credentials?.length === 1 ? "" : "s"}`
      ];
      summary = `${counts.join("\n")}${bundle.routing_mode ? `\nSets ${bundle.routing_mode}-wise routing` : ""}`;
    } catch {
      notify("That file is not valid JSON.", "danger");
      return;
    }
    const confirmed = await ask({
      title: `Import ${file.name}?`,
      body: "The bundle is replayed in one transaction: matching providers, model routes and API keys are overwritten with the values in the file. Existing items not in the file are left alone.",
      confirmLabel: "Import configuration",
      detail: summary
    });
    if (!confirmed) return;
    await importConfig(file);
  };

  return (
    <section className="settings-list config-transfer">
      {/* These rows hold buttons rather than a single control, so they are plain
          divs: a wrapping label would steal clicks and nest interactive labels. */}
      <div className="settings-row">
        <span><strong>Export configuration</strong><small>Writes every provider, model route, API key and rate limit to one JSON file, sorted A-to-Z. Import it on another install to reproduce this setup.</small></span>
        <div>
          <label className="config-transfer__secrets"><input type="checkbox" checked={includeSecrets} onChange={(event) => setIncludeSecrets(event.target.checked)} /><span>Include API keys</span></label>
          <Button variant="quiet" disabled={busy !== null} onClick={() => void exportConfig()}><Download size={14} aria-hidden="true" /> {busy === "export" ? "Exporting…" : "Export"}</Button>
        </div>
      </div>
      <div className="settings-row">
        <span><strong>Import configuration</strong><small>Replays a bundle in one transaction. Providers match on identifier and models on alias, so re-importing updates instead of duplicating. You see what the file contains before anything is written.</small></span>
        <div>
          <input id="config-import-file" type="file" accept="application/json,.json" style={{ display: "none" }} onChange={(event) => { const file = event.target.files?.[0]; event.target.value = ""; if (file) void reviewThenImport(file); }} />
          <Button variant="quiet" disabled={busy !== null} onClick={() => document.getElementById("config-import-file")?.click()}><Upload size={14} aria-hidden="true" /> {busy === "import" ? "Importing…" : "Import"}</Button>
        </div>
      </div>
      {result && (
        <div className="config-transfer__result">
          <strong>Import summary</strong>
          <ul>
            <li>Routing mode set to {result.routing_mode === "model" ? "model-wise (pooled)" : "provider-wise"}</li>
            <li>Providers: {result.providers_created} created · {result.providers_updated} updated</li>
            <li>Model routes: {result.models_created} created · {result.models_updated} updated</li>
            <li>API keys: {result.credentials_created} created · {result.credentials_updated} updated{result.credentials_skipped > 0 ? ` · ${result.credentials_skipped} skipped` : ""}</li>
          </ul>
          {result.credentials_unverified > 0 && <InlineNotice tone="danger">{result.credentials_unverified} imported API key{result.credentials_unverified === 1 ? " was" : "s were"} saved without contacting the provider. Use Test on each provider to confirm they work.</InlineNotice>}
          {/* Two providers can raise the same warning text, and using the text as
              the key made React drop the duplicate. The index is stable here: the
              list is rendered whole from one import result and never reordered. */}
          {result.warnings.map((warning, index) => <InlineNotice key={index} tone="danger">{warning}</InlineNotice>)}
        </div>
      )}
    </section>
  );
}

function SecretReveal({ title, keyValue, message, onClose, notify }: { title: string; keyValue: string; message: string; onClose: () => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <Sheet
      title={title}
      eyebrow="One-time secret"
      onClose={onClose}
      dirty={!confirmed}
      discardMessage="Close without confirming that the key is saved? This key cannot be shown again."
    >
      <InlineNotice tone="danger">{message}</InlineNotice>
      <div className="secret-value"><code>{keyValue}</code><Button variant="quiet" onClick={() => void copyText(keyValue).then(() => notify("Gateway key copied.")).catch(() => notify(clipboardBlocked, "danger"))}><Clipboard size={14} aria-hidden="true" /> Copy</Button></div>
      <label className="confirmation-check"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /><span>I stored this key securely.</span></label>
      {/* The button dismisses the panel and nothing else — the key already exists.
          "Finish" implied a remaining step, and the checkbox above it is what the
          operator actually finishes. */}
      <div className="sheet-actions"><span /><Button disabled={!confirmed} onClick={onClose}>Close</Button></div>
    </Sheet>
  );
}

function PageSkeleton() {
  return (
    <div className="page-skeleton" role="status" aria-live="polite">
      <span className="sr-only">Loading…</span>
      <span aria-hidden="true" />
      <span aria-hidden="true" />
      <span aria-hidden="true" />
      <span aria-hidden="true" />
    </div>
  );
}

type CredentialDraft = {
  id: string;
  label: string;
  secret: string;
  is_primary: boolean;
};

function uniqueKeyLines(value: string) {
  const seen = new Set<string>();
  const keys: string[] = [];
  for (const line of value.split(/\r?\n/)) {
    const key = line.trim();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    keys.push(key);
  }
  return keys;
}

function automaticCredentialEntries(provider: Provider, secrets: string[], preferred = new Map<string, string>()) {
  const used = new Set(provider.credentials.map((credential) => credential.label.toLocaleLowerCase()));
  let sequence = provider.credentials.length + 1;
  return secrets.map((secret) => {
    let label = preferred.get(secret) ?? "";
    if (!label || used.has(label.toLocaleLowerCase())) {
      do {
        label = `Key ${sequence}`;
        sequence += 1;
      } while (used.has(label.toLocaleLowerCase()));
    }
    used.add(label.toLocaleLowerCase());
    return { label, secret };
  });
}

function maskedSecret(secret: string) {
  return secret.length <= 4 ? "••••" : `•••• ${secret.slice(-4)}`;
}

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
        <Button type="button" variant="quiet" disabled={value.length >= 100} onClick={() => onChange([...value, newCredentialDraft()])}><Plus size={14} aria-hidden="true" /> Add another API key</Button>
      </div>
      {value.map((credential, index) => (
        // Every entry repeats the same three field labels, so the group is named
        // and each field carries its number: "Label" three times over tells a
        // screen reader operator nothing about which key they are filling in.
        <section className="credential-entry" key={credential.id} aria-label={`API key ${index + 1}${credential.label.trim() ? `, ${credential.label.trim()}` : ""}`}>
          <header>
            <strong>API key {index + 1}</strong>
            {value.length > 1 && <Button type="button" variant="quiet" onClick={() => onChange(value.filter((item) => item.id !== credential.id))} aria-label={`Remove API key ${index + 1}${credential.label.trim() ? `, ${credential.label.trim()}` : ""}`}><Trash2 size={13} aria-hidden="true" /> Remove</Button>}
          </header>
          <div className="field-pair">
            <label className="field"><span>Label</span><input placeholder="Production key" value={credential.label} onChange={(event) => update(credential.id, { label: event.target.value })} aria-label={`Label for API key ${index + 1}`} /></label>
            {/* autoComplete="off" is widely ignored for password fields; the
                new-password token is what actually stops a manager offering the
                operator's own saved credentials for a provider's API key. */}
            <label className="field"><span>API key</span><input type="password" autoComplete="new-password" spellCheck={false} placeholder="Paste provider API key" value={credential.secret} onChange={(event) => update(credential.id, { secret: event.target.value })} aria-label={`Secret for API key ${index + 1}`} /></label>
          </div>
          <label className="primary-choice">
            <input type="checkbox" checked={credential.is_primary} onChange={(event) => update(credential.id, { is_primary: event.target.checked })} aria-label={`Try API key ${index + 1} first while it has capacity`} />
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
    // An absent balance is "not tracked", which is deliberately not the same as
    // zero: zero is what stops a key receiving traffic. Only the spend figures are
    // defaulted to a number.
    default_key_balance_usd: provider.default_key_balance_usd ?? null,
    balance_spent_usd: safeNumber(provider.balance_spent_usd),
    models: (provider.models ?? []).map((model) => ({ ...model, supports_messages: model.supports_messages ?? true, strip_parameters: model.strip_parameters ?? [], capability_status: model.capability_status ?? "unverified", capability_profile: model.capability_profile ?? {}, input_cost_per_million_usd: model.input_cost_per_million_usd ?? 0, output_cost_per_million_usd: model.output_cost_per_million_usd ?? 0, request_cost_usd: model.request_cost_usd })),
    credentials: (provider.credentials ?? []).map((credential) => ({
      ...credential,
      validation_error: credential.validation_error ?? "",
      balance_usd: credential.balance_usd ?? null,
      balance_spent_usd: safeNumber(credential.balance_spent_usd),
      // Go serialises an empty map as null, so both of these arrive as null from
      // a key that has never had a limit set. Every render path indexes them
      // directly, and without an error boundary a null map takes the console
      // to a blank page rather than to a missing number.
      limits: credential.limits ?? emptyPolicy(),
      model_limits: credential.model_limits ?? {},
    })),
  }));
}

/** Absent numbers arrive from a partially-populated payload; `Intl` turns them
 *  into the literal string "NaN", which is worse than a zero in a readout. */
function safeNumber(value: number | null | undefined) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

/** The overview is the one payload every widget on the landing page indexes into
 *  without checking, so a missing array or a missing credit block used to take
 *  the whole console down. Defaulted here once instead of at forty call sites. */
function normalizeOverview(overview: Overview): Overview {
  const summary = overview.summary ?? ({} as Overview["summary"]);
  return {
    ...overview,
    summary: {
      ...summary,
      providers_total: safeNumber(summary.providers_total),
      providers_ready: safeNumber(summary.providers_ready),
      routes_total: safeNumber(summary.routes_total),
      routes_ready: safeNumber(summary.routes_ready),
      keys_total: safeNumber(summary.keys_total),
      keys_ready: safeNumber(summary.keys_ready),
      keys_warning: safeNumber(summary.keys_warning),
      requests: safeNumber(summary.requests),
      tokens: safeNumber(summary.tokens),
      estimated_cost_usd: safeNumber(summary.estimated_cost_usd),
      errors: safeNumber(summary.errors),
      error_rate: safeNumber(summary.error_rate),
      latency_p50_ms: safeNumber(summary.latency_p50_ms),
      latency_p95_ms: safeNumber(summary.latency_p95_ms),
      max_wait_ms: safeNumber(summary.max_wait_ms),
      gateway_key_ready: summary.gateway_key_ready ?? false,
      credit: summary.credit ?? emptyCreditTotals()
    },
    series: overview.series ?? [],
    providers: (overview.providers ?? []).map((provider) => ({
      ...provider,
      models_total: safeNumber(provider.models_total),
      models_ready: safeNumber(provider.models_ready),
      keys_total: safeNumber(provider.keys_total),
      keys_ready: safeNumber(provider.keys_ready),
      keys_warning: safeNumber(provider.keys_warning),
      validation_warnings: safeNumber(provider.validation_warnings),
      capacity: provider.capacity ?? {},
      credit: provider.credit ?? emptyCreditTotals()
    })),
    // The route rows render these figures straight into text, so a payload that
    // omits one produced "NaN% err" and "undefined ms" in the table rather than a
    // zero. Only the arrays used to be defaulted here.
    routes: (overview.routes ?? []).map((route) => ({
      ...route,
      requests: safeNumber(route.requests),
      errors: safeNumber(route.errors),
      tokens: safeNumber(route.tokens),
      estimated_cost_usd: safeNumber(route.estimated_cost_usd),
      error_rate: safeNumber(route.error_rate),
      latency_p95_ms: safeNumber(route.latency_p95_ms),
      healthy_credentials: safeNumber(route.healthy_credentials),
      unavailable_credentials: safeNumber(route.unavailable_credentials),
      total_credentials: safeNumber(route.total_credentials),
      default_max_output_tokens: safeNumber(route.default_max_output_tokens),
      strip_parameters: route.strip_parameters ?? [],
      segments: route.segments ?? []
    })),
    alerts: overview.alerts ?? [],
    recent_failures: overview.recent_failures ?? []
  };
}

function formatNumber(value: number) {
  const safe = safeNumber(value);
  return new Intl.NumberFormat("en", { notation: safe > 9999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(safe);
}

function formatCompact(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(safeNumber(value));
}

function formatUSD(value: number) {
  const safe = safeNumber(value);
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: safe < 1 ? 4 : 2 }).format(safe);
}

function formatLatency(value: number) {
  const safe = safeNumber(value);
  return safe >= 1000 ? `${(safe / 1000).toFixed(safe >= 10_000 ? 0 : 1)}s` : `${Math.round(safe)}ms`;
}

function formatChartDate(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  // Intl throws a RangeError on an invalid date rather than returning a string,
  // and this runs inside the overview's render path, so one malformed timestamp
  // in the series would take the whole page down.
  if (Number.isNaN(parsed.getTime())) return "—";
  return new Intl.DateTimeFormat("en", { month: "short", day: "numeric" }).format(parsed);
}

/** The wall-clock time a request landed. Every row in the log list renders one,
 *  so an unparseable stamp has to degrade to a dash rather than to "Invalid
 *  Date" in the row and in its accessible name. */
function formatClockTime(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "—";
  return parsed.toLocaleTimeString();
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
  // Everything the console throws is an Error, so this fallback only fires when a
  // rejection carries no message at all — a dropped connection, or a request the
  // browser cancelled. There is nothing to report except the one thing that helps:
  // the request never landed, so trying it again is safe.
  return "The gateway did not answer. Try again.";
}

export default App;
