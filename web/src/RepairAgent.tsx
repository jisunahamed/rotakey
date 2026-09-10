import { useEffect, useMemo, useState } from "react";
import { Activity, AlertTriangle, Bot, CheckCircle2, ChevronDown, CircleOff, Eye, RefreshCw, Save, ShieldCheck, Wrench } from "lucide-react";
import { api } from "./api";
import { Button } from "./Button";
import { formatRelativeTime } from "./lib/format";
import type { Provider } from "./types";

type Policy = {
  enabled: boolean;
  model_id: string;
  mode: "observe" | "auto" | "full" | "custom";
  permissions: string[];
  daily_tokens: number;
  diagnosis_seconds: number;
  output_tokens: number;
  route_ids: string[];
  version: number;
};

type RepairAttempt = {
  proposal: { diagnosis: string; action: string; parameter?: string; value?: unknown; expected_result?: string };
  before?: unknown;
  status: string;
  reason?: string;
  agent_tokens: number;
  duration_ms: number;
  test_tokens?: number;
  test_duration_ms?: number;
};

type Incident = {
  id: string;
  request_id: string;
  route_id: string;
  route_name?: string;
  provider_name?: string;
  category: string;
  error: string;
  status: string;
  attempts: RepairAttempt[];
  created_at?: string;
};

const modes: Array<{ id: Policy["mode"]; title: string; description: string; icon: typeof Eye }> = [
  { id: "observe", title: "Watch only", description: "Explain errors, but change nothing.", icon: Eye },
  { id: "auto", title: "Fix request errors", description: "Safely adjust request settings and retry.", icon: Wrench },
  { id: "full", title: "Full administration", description: "Use every available API, model and provider repair tool.", icon: ShieldCheck },
  { id: "custom", title: "Choose permissions", description: "Select exactly what Rotakey may change.", icon: Bot }
];

const toolLabels: Record<string, { title: string; description: string }> = {
  set_parameter: { title: "Adjust request values", description: "Correct token limits and supported generation values." },
  remove_parameter: { title: "Remove unsupported options", description: "Drop optional settings rejected by a provider." },
  switch_endpoint: { title: "Switch API endpoint", description: "Move between Chat and Responses when required." },
  set_timeout: { title: "Adjust provider timeout", description: "Save a tested timeout after a successful response." },
  reset_cooldown: { title: "Clear a key cooldown", description: "Only after the key passes validation." },
  refresh_connection: { title: "Refresh connection", description: "Close stale connections and reconnect." },
  select_credential: { title: "Choose another API key", description: "Use an existing healthy key for the same provider." },
  validate_credential: { title: "Validate API keys", description: "Check an existing key before changing its health." },
  set_route_enabled: { title: "Disable a broken route", description: "Only after a provider outage and after the request ends." }
};

function messageFrom(error: unknown) {
  return error instanceof Error ? error.message : String(error);
}

function modeSummary(policy: Policy) {
  if (!policy.enabled) return "Rotakey is using its built-in retry rules only.";
  if (policy.mode === "observe") return "Rotakey explains new errors, but it will not change requests or settings.";
  if (policy.mode === "auto") return "Rotakey can correct safe request settings, retry, and remember verified fixes.";
  if (policy.mode === "full") return "Rotakey can use every published repair tool for API, model, key, route and connection failures.";
  return `Rotakey can use ${policy.permissions.length} selected permission${policy.permissions.length === 1 ? "" : "s"}.`;
}

export function RepairAgentSettings({ providers }: { providers: Provider[] }) {
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [savedPolicy, setSavedPolicy] = useState("");
  const [tools, setTools] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    let active = true;
    void api<{ policy: Policy; tools: string[] }>("/api/admin/repair/policy")
      .then((result) => {
        if (!active) return;
        setPolicy(result.policy);
        setSavedPolicy(JSON.stringify(result.policy));
        setTools(result.tools);
        setMessage("");
      })
      .catch((error) => { if (active) setMessage(messageFrom(error)); });
    return () => { active = false; };
  }, [reload]);

  const selectedModel = useMemo(() => {
    if (!policy?.model_id) return null;
    for (const provider of providers) {
      const model = provider.models.find((candidate) => candidate.id === policy.model_id);
      if (model) return { provider, model };
    }
    return null;
  }, [policy?.model_id, providers]);

  const save = async () => {
    if (!policy) return;
    setBusy(true);
    try {
      const next = await api<Policy>("/api/admin/repair/policy", { method: "PUT", json: policy });
      setPolicy(next);
      setSavedPolicy(JSON.stringify(next));
      setMessage("AI repair settings saved.");
    } catch (error) {
      setMessage(messageFrom(error));
    } finally {
      setBusy(false);
    }
  };

  const dirty = policy !== null && JSON.stringify(policy) !== savedPolicy;
  const selectedMode = policy ? modes.find((mode) => mode.id === policy.mode) : undefined;

  return (
    <section className="repair-center" aria-labelledby="repair-title">
      <header className="repair-hero">
        <div className={`repair-hero__icon ${policy?.enabled ? "is-on" : ""}`}><Bot size={25} aria-hidden="true" /></div>
        <div>
          <div className="repair-hero__title-row">
            <h2 id="repair-title">Automatic error repair</h2>
            {policy && <span className={`repair-state ${policy.enabled ? "is-on" : "is-off"}`}><span />{policy.enabled ? "On" : "Off"}</span>}
          </div>
          <p>{policy ? modeSummary(policy) : "Loading repair status…"}</p>
        </div>
        {policy && (
          <label className="repair-switch">
            <input type="checkbox" checked={policy.enabled} onChange={(event) => setPolicy({ ...policy, enabled: event.target.checked })} />
            <span aria-hidden="true" />
            <b>{policy.enabled ? "Turn off" : "Turn on"}</b>
          </label>
        )}
      </header>

      {message && <div className="repair-message" role="status">{message}</div>}

      {!policy ? (
        <div className="repair-loading"><Activity size={18} aria-hidden="true" /> Reading repair settings… <Button variant="quiet" onClick={() => setReload((value) => value + 1)}><RefreshCw size={14} aria-hidden="true" /> Retry</Button></div>
      ) : (
        <form className="repair-form" onSubmit={(event) => { event.preventDefault(); void save(); }}>
          <fieldset disabled={busy}>
            <div className="repair-step">
              <div className="repair-step__number">1</div>
              <div className="repair-step__body">
                <label htmlFor="repair-model"><strong>Choose the model that investigates errors</strong><small>This model diagnoses failures only. The user still receives an answer from the model they requested.</small></label>
                <select id="repair-model" required={policy.enabled} value={policy.model_id} onChange={(event) => setPolicy({ ...policy, model_id: event.target.value })}>
                  <option value="">Select a coding model…</option>
                  {providers.filter((provider) => provider.enabled).map((provider) => (
                    <optgroup key={provider.id} label={provider.name}>
                      {provider.models.filter((model) => model.enabled).map((model) => <option key={model.id} value={model.id}>{model.public_alias}</option>)}
                    </optgroup>
                  ))}
                </select>
                {selectedModel && <p className="repair-selection"><CheckCircle2 size={15} aria-hidden="true" /> {selectedModel.model.public_alias} through {selectedModel.provider.name}</p>}
              </div>
            </div>

            <div className="repair-step">
              <div className="repair-step__number">2</div>
              <div className="repair-step__body">
                <strong>Choose what Rotakey may do</strong>
                <small>Every change is tested. Rotakey remembers it only when the original request succeeds.</small>
                <div className="repair-mode-grid">
                  {modes.map((mode) => {
                    const Icon = mode.icon;
                    return <label className={`repair-mode ${policy.mode === mode.id ? "is-selected" : ""}`} key={mode.id}>
                      <input type="radio" name="repair-mode" value={mode.id} checked={policy.mode === mode.id} onChange={() => setPolicy({ ...policy, mode: mode.id })} />
                      <Icon size={19} aria-hidden="true" />
                      <span><b>{mode.title}</b><small>{mode.description}</small></span>
                      {mode.id === "auto" && <em>Recommended</em>}
                    </label>;
                  })}
                </div>

                {policy.mode === "full" && (
                  <p className="repair-full-access"><ShieldCheck size={16} aria-hidden="true" /> All {tools.length} available repair tools are enabled. Rotakey tests changes, rolls back failures and saves only verified fixes.</p>
                )}

                {policy.mode === "custom" && (
                  <div className="repair-permissions">
                    {tools.map((tool) => {
                      const copy = toolLabels[tool] ?? { title: tool.replaceAll("_", " "), description: "Allow this repair operation." };
                      return <label key={tool}>
                        <input type="checkbox" checked={policy.permissions.includes(tool)} onChange={(event) => setPolicy({ ...policy, permissions: event.target.checked ? [...policy.permissions, tool] : policy.permissions.filter((item) => item !== tool) })} />
                        <span><b>{copy.title}</b><small>{copy.description}</small></span>
                      </label>;
                    })}
                  </div>
                )}
              </div>
            </div>

            <div className="repair-step">
              <div className="repair-step__number">3</div>
              <div className="repair-step__body">
                <strong>Choose where it runs</strong>
                <small>{policy.route_ids.length === 0 ? "It will watch every enabled model route." : `It will run on ${policy.route_ids.length} selected route${policy.route_ids.length === 1 ? "" : "s"}.`}</small>
                <details className="repair-disclosure">
                  <summary><ChevronDown size={16} aria-hidden="true" /> {policy.route_ids.length === 0 ? "All enabled routes" : `${policy.route_ids.length} selected routes`}</summary>
                  <div className="repair-route-list">
                    {providers.flatMap((provider) => provider.models.filter((model) => model.enabled).map((model) => (
                      <label key={model.id}>
                        <input type="checkbox" checked={policy.route_ids.includes(model.id)} onChange={(event) => setPolicy({ ...policy, route_ids: event.target.checked ? [...policy.route_ids, model.id] : policy.route_ids.filter((id) => id !== model.id) })} />
                        <span><b>{model.public_alias}</b><small>{provider.name}</small></span>
                      </label>
                    )))}
                  </div>
                  {policy.route_ids.length > 0 && <button type="button" className="repair-text-button" onClick={() => setPolicy({ ...policy, route_ids: [] })}>Use all enabled routes</button>}
                </details>
              </div>
            </div>

            <details className="repair-advanced">
              <summary><ChevronDown size={16} aria-hidden="true" /> Advanced limits</summary>
              <p>These limits control repair cost and delay. The defaults suit most installations.</p>
              <div className="repair-limit-grid">
                <label><span><b>Daily diagnosis budget</b><small>Maximum tokens used to investigate errors each UTC day.</small></span><div><input type="number" required min={1} max={100000000} value={policy.daily_tokens} onChange={(event) => setPolicy({ ...policy, daily_tokens: Number(event.target.value) })} /><code>tokens</code></div></label>
                <label><span><b>Time per request</b><small>Maximum extra diagnosis time before normal fallback continues.</small></span><div><input type="number" required min={1} max={10} value={policy.diagnosis_seconds} onChange={(event) => setPolicy({ ...policy, diagnosis_seconds: Number(event.target.value) })} /><code>seconds</code></div></label>
                <label><span><b>Diagnosis answer size</b><small>Maximum tokens the repair model may return per diagnosis.</small></span><div><input type="number" required min={128} max={2048} value={policy.output_tokens} onChange={(event) => setPolicy({ ...policy, output_tokens: Number(event.target.value) })} /><code>tokens</code></div></label>
              </div>
              <p className="repair-boundary"><ShieldCheck size={16} aria-hidden="true" /> No source-code changes, shell access, credential disclosure or deployment. Service restart is unavailable on this host.</p>
            </details>

            <footer className="repair-savebar">
              <div><b>{dirty ? "You have unsaved changes" : "Settings are saved"}</b><small>{selectedMode?.title}. Changes apply to new requests immediately.</small></div>
              <Button type="submit" disabled={busy || !dirty}><Save size={15} aria-hidden="true" /> {busy ? "Saving…" : "Save repair settings"}</Button>
            </footer>
          </fieldset>
        </form>
      )}

      <RepairHistory providers={providers} />
    </section>
  );
}

type StatusView = { title: string; detail: string; tone: "fixed" | "watching" | "failed" | "fallback"; icon: typeof CheckCircle2 };

function statusView(status: string): StatusView {
  switch (status) {
    case "verified": return { title: "Fixed automatically", detail: "The repaired request succeeded and Rotakey remembered the fix.", tone: "fixed", icon: CheckCircle2 };
    case "verified_configuration_conflict": return { title: "Request fixed", detail: "The request succeeded, but a newer setting prevented Rotakey from saving the change.", tone: "fallback", icon: AlertTriangle };
    case "recovered_elsewhere": return { title: "Answered by another route", detail: "This route failed, but Rotakey found another route for the same model.", tone: "fallback", icon: RefreshCw };
    case "observed": return { title: "Watched only", detail: "Rotakey recorded the error and made no change.", tone: "watching", icon: Eye };
    case "invalid_response": return { title: "Fix rejected", detail: "The test returned no usable answer, so Rotakey did not remember the change.", tone: "failed", icon: CircleOff };
    case "failed": return { title: "Couldn’t fix", detail: "The tested changes failed and were rolled back.", tone: "failed", icon: AlertTriangle };
    default: return { title: status.replaceAll("_", " "), detail: "Open this item to see what happened.", tone: "watching", icon: Activity };
  }
}

function actionText(attempt: RepairAttempt) {
  const action = attempt.proposal.action;
  if (!action || action === "none") return "No safe change was suggested";
  if (action === "set_parameter") return `Changed ${attempt.proposal.parameter ?? "a request value"} from ${JSON.stringify(attempt.before)} to ${JSON.stringify(attempt.proposal.value)}`;
  if (action === "remove_parameter") return `Removed unsupported ${attempt.proposal.parameter ?? "request option"}`;
  return toolLabels[action]?.title ?? action.replaceAll("_", " ");
}

function attemptResult(status: string) {
  if (status === "verified") return "Worked";
  if (status === "testing") return "Testing";
  if (status === "rolled_back") return "Didn’t work — rolled back";
  if (status === "permission_denied") return "Not allowed by your settings";
  if (status === "background_observed" || status === "observed") return "Suggestion only";
  if (status === "diagnosis_failed") return "Could not diagnose";
  if (status === "rejected") return "Rejected as unsafe or invalid";
  return status.replaceAll("_", " ");
}

export function RepairHistory({ requestID, providers }: { requestID?: string; providers?: Provider[] }) {
  const [items, setItems] = useState<Incident[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    const read = () => {
      void api<Incident[]>(`/api/admin/repair/incidents${requestID ? `?request_id=${encodeURIComponent(requestID)}` : ""}`)
        .then((result) => { if (active) { setItems(result); setError(""); } })
        .catch((caught) => { if (active) setError(messageFrom(caught)); });
    };
    read();
    const timer = window.setInterval(read, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, [requestID]);

  const routeNames = useMemo(() => new Map(providers?.flatMap((provider) => provider.models.map((model) => [model.id, `${model.public_alias} through ${provider.name}`] as const)) ?? []), [providers]);
  const visible = requestID ? items : items.slice(0, 12);
  const fixed = visible.filter((item) => item.status === "verified" || item.status === "recovered_elsewhere" || item.status === "verified_configuration_conflict").length;
  const failed = visible.filter((item) => item.status === "failed" || item.status === "invalid_response").length;

  return (
    <section className={`repair-history ${requestID ? "is-request" : ""}`} aria-label="Repair activity">
      <header>
        <div><h3>{requestID ? "Repair result" : "Recent repair activity"}</h3><p>{requestID ? "What Rotakey tried for this request." : "See which errors were fixed and which still need attention."}</p></div>
        {!requestID && visible.length > 0 && <div className="repair-history__counts"><span className="is-fixed"><CheckCircle2 size={14} />{fixed} fixed</span><span className="is-failed"><AlertTriangle size={14} />{failed} unresolved</span></div>}
      </header>
      {error && <div className="repair-message is-error" role="status">Could not load repair activity: {error}</div>}
      {visible.length === 0 && !error && <div className="repair-empty"><CheckCircle2 size={22} aria-hidden="true" /><div><b>No repair activity yet</b><p>When Rotakey investigates an error, the result will appear here in plain language.</p></div></div>}
      <div className="repair-incident-list">
        {visible.map((item) => {
          const view = statusView(item.status);
          const Icon = view.icon;
          const route = item.route_name && item.provider_name ? `${item.route_name} through ${item.provider_name}` : routeNames.get(item.route_id) ?? "Model route";
          return <details className={`repair-incident is-${view.tone}`} key={item.id} open={requestID ? true : undefined}>
            <summary>
              <span className="repair-incident__icon"><Icon size={18} aria-hidden="true" /></span>
              <span className="repair-incident__headline"><b>{view.title}</b><small>{view.detail}</small></span>
              <span className="repair-incident__meta"><b>{route}</b>{item.created_at && <time dateTime={item.created_at}>{formatRelativeTime(item.created_at)}</time>}</span>
              <ChevronDown className="repair-incident__chevron" size={17} aria-hidden="true" />
            </summary>
            <div className="repair-incident__body">
              <div className="repair-fact"><span>What happened</span><p>{item.error || "The provider rejected the request."}</p></div>
              <div className="repair-fact"><span>Request</span><p><code>{item.request_id}</code></p></div>
              {item.attempts.length === 0 ? <div className="repair-fact"><span>What Rotakey did</span><p>No AI repair was attempted.</p></div> : (
                <div className="repair-attempts"><span>What Rotakey did</span>{item.attempts.map((attempt, attemptIndex) => <div key={`${item.id}-${attemptIndex}`}>
                  <span className={`repair-attempt__mark is-${attempt.status}`} />
                  <div><b>{actionText(attempt)}</b>{attempt.proposal.diagnosis && <p>{attempt.proposal.diagnosis}</p>}{attempt.reason && <p>{attempt.reason}</p>}</div>
                  <small>{attemptResult(attempt.status)}</small>
                </div>)}</div>
              )}
            </div>
          </details>;
        })}
      </div>
    </section>
  );
}

type Metrics = { requests: number; successful_requests: number; incidents: number; recovered_incidents: number; agent_tokens: number; diagnosis_ms: number; daily_budget_used: number; test_tokens: number; test_ms: number };

export function RepairMetrics() {
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    const read = () => { void api<Metrics>("/api/admin/repair/metrics").then((result) => { if (active) { setMetrics(result); setError(""); } }).catch((caught) => { if (active) setError(messageFrom(caught)); }); };
    read();
    const timer = window.setInterval(read, 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);

  const rate = metrics?.incidents ? Math.round((100 * metrics.recovered_incidents) / metrics.incidents) : 0;
  return <section className="repair-metrics" aria-label="Automatic repair performance">
    <header><div><h2>Automatic repair</h2><p>Results from the last 24 hours.</p></div>{metrics && <span className={metrics.incidents === 0 ? "is-quiet" : rate >= 70 ? "is-good" : "is-warning"}>{metrics.incidents === 0 ? "No errors investigated" : `${rate}% recovered`}</span>}</header>
    {error && <p role="status">Could not load repair results: {error}</p>}
    {metrics && <div className="repair-metric-grid"><div><strong>{metrics.recovered_incidents}</strong><span>Errors fixed</span></div><div><strong>{Math.max(0, metrics.incidents - metrics.recovered_incidents)}</strong><span>Still unresolved</span></div><div><strong>{metrics.requests ? `${Math.round((100 * metrics.successful_requests) / metrics.requests)}%` : "—"}</strong><span>Final request success</span></div><div><strong>{metrics.daily_budget_used.toLocaleString()}</strong><span>Diagnosis tokens today</span></div></div>}
  </section>;
}
