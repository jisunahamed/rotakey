import { useEffect, useState } from "react";
import { api } from "./api";
import { Button } from "./Button";
import type { Provider } from "./types";

type Policy = {
  enabled: boolean; model_id: string; mode: string; permissions: string[];
  daily_tokens: number; diagnosis_seconds: number; output_tokens: number;
  route_ids: string[]; version: number;
};
type Incident = {
  id: string; request_id: string; route_id: string; category: string; error: string; status: string;
  attempts: Array<{ proposal: { diagnosis: string; action: string; parameter?: string; value?: unknown }; before?: unknown; status: string; reason?: string; agent_tokens: number; duration_ms: number }>;
};

export function RepairAgentSettings({ providers }: { providers: Provider[] }) {
  const [policy, setPolicy] = useState<Policy | null>(null);
  const [tools, setTools] = useState<string[]>([]);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState(false);
  const [reload, setReload] = useState(0);
  useEffect(() => {
    let active = true;
    void api<{ policy: Policy; tools: string[] }>("/api/admin/repair/policy").then(result => {
      if (active) { setPolicy(result.policy); setTools(result.tools); setMessage(""); }
    }).catch(error => { if (active) setMessage(String(error)); });
    return () => { active = false; };
  }, [reload]);
  const save = async () => {
    setBusy(true);
    try { setPolicy(await api<Policy>("/api/admin/repair/policy", { method: "PUT", json: policy })); setMessage("Repair policy saved."); }
    catch (error) { setMessage(String(error)); }
    finally { setBusy(false); }
  };
  return <section className="settings-list" aria-labelledby="repair-title">
    <h2 id="repair-title">AI repair agent</h2>
    <p>Diagnose provider failures and verify repairs using a separate coding model. Caller responses keep their selected model.</p>
    {message && <p role="status">{message}</p>}
    <Button variant="quiet" disabled={busy} onClick={() => setReload(n => n + 1)}>Reload repair policy</Button>
    {policy && <form onSubmit={event => { event.preventDefault(); void save(); }}>
      <fieldset disabled={busy}>
        <label className="settings-row"><span><strong>Enable agent</strong><small>Starts in observe mode. Disable to stop new agent actions.</small></span><input type="checkbox" checked={policy.enabled} onChange={event => setPolicy({ ...policy, enabled: event.target.checked })} /></label>
        <label className="settings-row"><span><strong>Repair model and connection</strong></span><select required={policy.enabled} value={policy.model_id} onChange={event => setPolicy({ ...policy, model_id: event.target.value })}><option value="">Select a coding model</option>{providers.filter(p => p.enabled).map(provider => <optgroup key={provider.id} label={provider.name}>{provider.models.filter(m => m.enabled).map(model => <option key={model.id} value={model.id}>{model.public_alias} · {model.upstream_model}</option>)}</optgroup>)}</select></label>
        <label className="settings-row"><span><strong>Permission mode</strong></span><select value={policy.mode} onChange={event => setPolicy({ ...policy, mode: event.target.value })}><option value="observe">Observe — diagnosis only</option><option value="auto">Auto repair — request compatibility</option><option value="full">Full administration — published tools</option><option value="custom">Custom permissions</option></select></label>
        {policy.mode === "custom" && tools.map(tool => <label className="settings-row" key={tool}><span>{tool.replaceAll("_", " ")}</span><input type="checkbox" checked={policy.permissions.includes(tool)} onChange={event => setPolicy({ ...policy, permissions: event.target.checked ? [...policy.permissions, tool] : policy.permissions.filter(item => item !== tool) })} /></label>)}
        <label className="settings-row"><span><strong>Daily diagnosis token budget</strong></span><input type="number" required min={1} max={100000000} value={policy.daily_tokens} onChange={event => setPolicy({ ...policy, daily_tokens: Number(event.target.value) })} /></label>
        <label className="settings-row"><span><strong>Diagnosis time budget</strong><small>Total seconds per caller request.</small></span><input type="number" required min={1} max={10} value={policy.diagnosis_seconds} onChange={event => setPolicy({ ...policy, diagnosis_seconds: Number(event.target.value) })} /></label>
        <label className="settings-row"><span><strong>Diagnosis output token cap</strong></span><input type="number" required min={128} max={2048} value={policy.output_tokens} onChange={event => setPolicy({ ...policy, output_tokens: Number(event.target.value) })} /></label>
        <details><summary>Rollout routes ({policy.route_ids.length || "all enabled"})</summary>{providers.map(provider => provider.models.filter(model => model.enabled).map(model => <label className="settings-row" key={model.id}><span>{provider.name} · {model.public_alias}</span><input type="checkbox" checked={policy.route_ids.includes(model.id)} onChange={event => setPolicy({ ...policy, route_ids: event.target.checked ? [...policy.route_ids, model.id] : policy.route_ids.filter(id => id !== model.id) })} /></label>))}</details>
        <p>Settings, API and connection operations only. No source edits, shell access or deployment. Process restart is unavailable on this host.</p>
        <Button type="submit" disabled={busy}>{busy ? "Saving…" : "Save repair policy"}</Button>
      </fieldset>
    </form>}
    <RepairHistory />
  </section>;
}

export function RepairHistory({ requestID }: { requestID?: string }) {
  const [items, setItems] = useState<Incident[]>([]);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    const read = () => { void api<Incident[]>(`/api/admin/repair/incidents${requestID ? `?request_id=${encodeURIComponent(requestID)}` : ""}`).then(result => { if (active) { setItems(result); setError(""); } }).catch(caught => { if (active) setError(String(caught)); }); };
    read(); const timer = window.setInterval(read, 5000);
    return () => { active = false; window.clearInterval(timer); };
  }, [requestID]);
  return <section aria-label="Repair history"><h3>Repair history</h3>{error && <p role="status">{error}</p>}{items.length === 0 && !error && <p>No repair incidents recorded.</p>}{items.map(item => <details key={item.id}><summary>{item.status} · {item.category.replaceAll("_", " ")} · {item.request_id}</summary><p>{item.error}</p>{item.attempts.map((attempt, index) => <div key={index}><strong>{attempt.status} · {attempt.proposal.action}</strong><p>{attempt.proposal.diagnosis}</p>{attempt.proposal.parameter && <p>{attempt.proposal.parameter}: {JSON.stringify(attempt.before)} → {JSON.stringify(attempt.proposal.value)}</p>}<p>{attempt.reason}</p><small>{attempt.agent_tokens} diagnosis tokens · {attempt.duration_ms} ms</small></div>)}</details>)}</section>;
}

type Metrics = { requests: number; successful_requests: number; incidents: number; recovered_incidents: number; agent_tokens: number; diagnosis_ms: number; daily_budget_used: number; test_tokens: number; test_ms: number };
export function RepairMetrics() {
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    const read = () => { void api<Metrics>("/api/admin/repair/metrics").then(result => { if (active) { setMetrics(result); setError(""); } }).catch(caught => { if (active) setError(String(caught)); }); };
    read(); const timer = window.setInterval(read, 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, []);
  return <section aria-label="Recovery metrics"><h2>Recovery · last 24 hours</h2>{error && <p role="status">{error}</p>}{metrics && <p>Final success: {metrics.requests ? `${(100 * metrics.successful_requests / metrics.requests).toFixed(1)}%` : "No requests"} · Recovered incidents: {metrics.recovered_incidents}/{metrics.incidents} · Diagnosis tokens: {metrics.agent_tokens.toLocaleString()} · Diagnosis time: {(metrics.diagnosis_ms / 1000).toFixed(1)}s total · Repair test tokens: {metrics.test_tokens.toLocaleString()} · Repair test time: {(metrics.test_ms / 1000).toFixed(1)}s total · Today’s budget used: {metrics.daily_budget_used.toLocaleString()} tokens</p>}</section>;
}
