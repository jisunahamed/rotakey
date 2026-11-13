import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import { Button, InlineNotice } from "./components";
import { useConfirm } from "./ConfirmDialog";
import { errorMessage } from "./lib/format";

const path = "/api/admin/drive-backup";
type Settings = {
  connected: boolean; account?: string; client_id?: string; folder_id?: string; folder_name?: string;
  daily_time: string; scheduled: boolean; delete_after_upload: boolean; include_bodies: boolean;
  include_metadata: boolean; first_backup_pending: boolean; next_run_at: string | null; revision: number;
};
type Run = {
  id: string; started_at: string; finished_at: string | null; state: "queued" | "running" | "completed" | "failed";
  uploaded: number; deleted: number; files: number; error?: string; last_file_id?: string;
  delete_source: boolean; include_bodies: boolean; include_metadata: boolean;
};
type Snapshot = { settings: Settings; runs: Run[]; redirect_uri: string; preserve_local_logs: boolean };
type Folder = { id: string; name: string };
type Action = "upload" | "upload_delete" | "cancel";
type Notify = (text: string, tone?: "success" | "danger") => void;
const date = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short", timeZone: "Asia/Dhaka" });

function selectedData(s: {include_bodies: boolean; include_metadata: boolean}) {
  return s.include_bodies ? (s.include_metadata ? "Input/output and metadata" : "Input/output") : "Metadata";
}

export function DriveBackup({ notify }: { notify: Notify }) {
  const ask = useConfirm();
  const [data, setData] = useState<Snapshot>();
  const [draft, setDraft] = useState<Settings>();
  const [clientID, setClientID] = useState("");
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [folders, setFolders] = useState<Folder[]>([]);
  const [search, setSearch] = useState("");
  const [activeSearch, setActiveSearch] = useState("");
  const [nextPage, setNextPage] = useState("");
  const [loadingFolders, setLoadingFolders] = useState(false);
  const [showConnect, setShowConnect] = useState(false);
  const [showChoice, setShowChoice] = useState(false);
  const dialog = useRef<HTMLDialogElement>(null);
  const cancelChoice = useRef<HTMLButtonElement>(null);
  const active = data?.runs.some((r) => r.state === "queued" || r.state === "running") ?? false;
  const saved = data?.settings;
  const dirty = saved && draft && ["folder_id", "daily_time", "scheduled", "delete_after_upload", "include_bodies", "include_metadata"].some((k) => saved[k as keyof Settings] !== draft[k as keyof Settings]);

  const reload = useCallback(async (resetDraft = false) => {
    const fresh = await api<Snapshot>(path);
    setData(fresh);
    setDraft((current) => resetDraft || !current ? fresh.settings : current);
    return fresh;
  }, []);

  useEffect(() => {
    void reload(true).catch((e) => setError(errorMessage(e)));
    const here = new URL(window.location.href);
    const result = here.searchParams.get("drive");
    if (!result) return;
    here.searchParams.delete("drive");
    window.history.replaceState(window.history.state, "", here);
    const messages: Record<string, string> = {
      denied: "Google authorization was cancelled.", expired: "Connection expired. Sign in and connect again.",
      changed: "Backup settings changed during connection. Try again when no backup is running.",
      permission: "Enable Google Drive API and grant Drive access, then reconnect.", failed: "Google connection failed. Check the OAuth credentials and authorized redirect URI."
    };
    if (result === "connected") notify("Google Drive connected. Select a folder to set up your first backup.");
    else setError(messages[result] ?? "Google connection failed.");
  }, [reload, notify]);

  useEffect(() => {
    // Slow polling also picks up scheduled runs that begin while Settings is open.
    const timer = window.setInterval(() => { if (!busy && !showChoice) void reload().catch(() => {}); }, active ? 2000 : 15000);
    return () => window.clearInterval(timer);
  }, [active, busy, showChoice, reload]);

  useEffect(() => {
    if (!showChoice || !dialog.current) return;
    const node = dialog.current;
    node.showModal();
    cancelChoice.current?.focus();
    return () => { if (node.open) node.close(); };
  }, [showChoice]);

  async function loadFolders(term = "", page = "") {
    setLoadingFolders(true);
    setError("");
    try {
      const params = new URLSearchParams({ search: term, page_token: page });
      const result = await api<{folders: Folder[]; next_page_token: string}>(`${path}/folders?${params}`);
      setFolders((previous) => page ? [...previous, ...result.folders.filter((f) => !previous.some((x) => x.id === f.id))] : result.folders);
      setActiveSearch(term);
      setNextPage(result.next_page_token);
    } catch (e) { setError(errorMessage(e)); }
    finally { setLoadingFolders(false); }
  }

  async function connect() {
    setBusy(true); setError("");
    try {
      const result = await api<{authorization_url: string}>(`${path}/connect`, { method: "POST", json: { client_id: clientID, client_secret: secret } });
      setSecret("");
      window.location.assign(result.authorization_url);
    } catch (e) { setError(errorMessage(e)); setBusy(false); }
  }

  async function save() {
    if (!draft || !saved) return;
    if (draft.scheduled && draft.delete_after_upload && dirty) {
      if (!await ask({ title: "Delete VPS data after each daily backup?", body: `Rotakey will remove the covered ${selectedData(draft).toLowerCase()} from the VPS only after verifying the Drive copies.`, confirmLabel: "Save and enable deletion" })) return;
    }
    setBusy(true); setError("");
    try {
      const { folder_id, daily_time, scheduled, delete_after_upload, include_bodies, include_metadata } = draft;
      await api(path, { method: "PUT", json: { folder_id, daily_time, scheduled, delete_after_upload, include_bodies, include_metadata, revision: draft.revision } });
      const fresh = await reload(true);
      notify("Drive backup settings saved.");
      if (fresh.settings.first_backup_pending) setShowChoice(true);
    } catch (e) { setError(errorMessage(e)); }
    finally { setBusy(false); }
  }

  async function choose(action: Action) {
    if (!saved) return;
    if (action === "cancel" && !saved.first_backup_pending) { setShowChoice(false); return; }
    setBusy(true); setError("");
    try {
      await api(`${path}/run`, { method: "POST", json: { action, revision: saved.revision } });
      setShowChoice(false);
      await reload(true);
      if (action !== "cancel") notify("Backup queued. You can leave this page while it runs.");
    } catch (e) { setShowChoice(false); setError(errorMessage(e)); }
    finally { setBusy(false); }
  }

  async function copyRedirect() {
    if (!data?.redirect_uri) return;
    try { await navigator.clipboard.writeText(data.redirect_uri); notify("Redirect URI copied."); }
    catch { setError("Copy the redirect URI shown below into Google Cloud."); }
  }

  async function disconnect() {
    if (!saved || !await ask({ title: "Disconnect Google Drive?", body: "Daily backups will stop. Files already in Google Drive will stay there.", confirmLabel: "Disconnect" })) return;
    setBusy(true); setError("");
    try {
      await api(`${path}/disconnect`, { method: "POST", json: { revision: saved.revision } });
      await reload(true);
      setFolders([]); setNextPage(""); setClientID(""); setShowConnect(false);
    } catch (e) { setError(errorMessage(e)); }
    finally { setBusy(false); }
  }

  return <section className="settings-list drive-backup" aria-label="Google Drive backup">
    <header className="drive-backup__header">
      <div><h2>Google Drive backup</h2><p>Save request data to your Drive and manage how long it stays on this VPS.</p></div>
      <div className="drive-backup__actions">
        {saved && <span className="drive-backup__pill">{saved.connected ? "Connected" : "Not connected"}</span>}
        <Button variant="quiet" disabled={busy} onClick={() => { setError(""); void reload(!dirty).catch((e) => setError(errorMessage(e))); }}>Refresh</Button>
      </div>
    </header>
    {error && <InlineNotice tone="danger">{error}</InlineNotice>}
    {!data && !error && <p>Loading backup settings…</p>}
    {data?.preserve_local_logs && <p>Automatic age-based cleanup is paused on this VPS. Use verified backup cleanup to clear saved requests.</p>}
    {data && saved && (!saved.connected || showConnect) && <form className="drive-backup__form" onSubmit={(e) => { e.preventDefault(); void connect(); }}>
      <div><h3>1. Connect Google Drive</h3><p>Enable the Drive API and create a <a href="https://console.cloud.google.com/auth/clients" target="_blank" rel="noreferrer">Web application OAuth client</a>. Add this authorized redirect URI:</p></div>
      <div className="drive-backup__actions"><code>{data.redirect_uri || "Configure the public HTTPS URL to connect Google Drive."}</code><Button type="button" variant="quiet" disabled={!data.redirect_uri} onClick={() => void copyRedirect()}>Copy URI</Button></div>
      <div className="drive-backup__grid">
        <label>OAuth client ID<input required value={clientID} autoComplete="off" onChange={(e) => setClientID(e.target.value)} /></label>
        <label>OAuth client secret<input required type="password" autoComplete="off" onChange={(e) => setSecret(e.target.value)} value={secret} /></label>
      </div>
      <div className="drive-backup__actions"><Button disabled={busy || active || !data.redirect_uri}>Connect Google account</Button>{showConnect && <Button type="button" variant="quiet" onClick={() => { setShowConnect(false); setSecret(""); }}>Cancel</Button>}</div>
    </form>}
    {saved?.connected && draft && <div className="drive-backup__form">
      <div className="drive-backup__header"><div><h3>Drive account</h3><p>{saved.account}</p></div><div className="drive-backup__actions">
        <Button variant="quiet" disabled={busy || active} onClick={() => { setClientID(saved.client_id ?? ""); setShowConnect(true); }}>Reconnect</Button>
        <Button variant="quiet" disabled={busy || active} onClick={() => void disconnect()}>Disconnect</Button>
      </div></div>
      <div><h3>2. Choose a folder</h3><p>Use a private folder if the backups contain user conversations.</p></div>
      <form className="drive-backup__actions" onSubmit={(e) => { e.preventDefault(); void loadFolders(search); }}>
        <input type="search" aria-label="Search Drive folders" placeholder="Search Drive folders" value={search} onChange={(e) => setSearch(e.target.value)} />
        <Button variant="quiet" disabled={busy || loadingFolders}>{loadingFolders ? "Loading…" : search ? "Search folders" : "Browse folders"}</Button>
      </form>
      <label className="drive-backup__folder">Backup folder<select disabled={busy || active} value={draft.folder_id ?? ""} onChange={(e) => setDraft({ ...draft, folder_id: e.target.value, folder_name: folders.find((f) => f.id === e.target.value)?.name })}>
        <option value="">Choose a folder</option>
        {draft.folder_id && !folders.some((f) => f.id === draft.folder_id) && <option value={draft.folder_id}>{draft.folder_name}</option>}
        {folders.map((f) => <option key={f.id} value={f.id}>{f.name}{f.id === "root" ? "" : ` · ${f.id.slice(-6)}`}</option>)}
      </select></label>
      {nextPage && <div><Button variant="quiet" disabled={loadingFolders} onClick={() => void loadFolders(activeSearch, nextPage)}>Load more folders</Button></div>}
      <div>{active && <InlineNotice>Backup is running. Changes apply to the next backup; account and folder stay locked until this run finishes.</InlineNotice>}<h3>3. Choose what to save</h3><p>Metadata includes model, time, status and usage, even for requests with no saved body.</p></div>
      <div className="drive-backup__actions">
        <label><input type="checkbox" checked={draft.include_bodies} disabled={busy} onChange={(e) => setDraft({ ...draft, include_bodies: e.target.checked })} /> Input/output</label>
        <label><input type="checkbox" checked={draft.include_metadata} disabled={busy} onChange={(e) => setDraft({ ...draft, include_metadata: e.target.checked })} /> Metadata</label>
      </div>
      <div><h3>4. Schedule daily backups</h3><p>{saved.next_run_at && !saved.first_backup_pending ? `Next backup: ${date.format(new Date(saved.next_run_at))} (Dhaka)` : "Run on demand or set a daily time (Dhaka)."}</p></div>
      <div className="drive-backup__actions">
        <label><input type="checkbox" checked={draft.scheduled} disabled={busy} onChange={(e) => setDraft({ ...draft, scheduled: e.target.checked })} /> Daily backup</label>
        <input type="time" aria-label="Daily backup time (Dhaka)" value={draft.daily_time} disabled={busy || !draft.scheduled} onChange={(e) => setDraft({ ...draft, daily_time: e.target.value })} />
        <label><input type="checkbox" checked={draft.delete_after_upload} disabled={busy || !draft.scheduled} onChange={(e) => setDraft({ ...draft, delete_after_upload: e.target.checked })} /> Delete after verified upload</label>
      </div>
      <div className="drive-backup__actions">
        <Button disabled={busy || !dirty || !draft.daily_time || !draft.folder_id || (!draft.include_bodies && !draft.include_metadata)} onClick={() => void save()}>Save backup settings</Button>
        <Button variant="quiet" disabled={busy || active || Boolean(dirty) || !saved.folder_id} onClick={() => setShowChoice(true)}>{saved.first_backup_pending ? "Choose first backup" : "Back up now"}</Button>
      </div>
    </div>}
    {data && data.runs.length > 0 && <div className="drive-backup__form"><h3>Backup activity</h3><ul className="drive-backup__runs">
      {data.runs.map((run) => <li key={run.id}><div><strong>{run.state[0].toUpperCase() + run.state.slice(1)}</strong><span>{date.format(new Date(run.started_at))} · {selectedData(run)}</span></div>
        <div><span>{run.uploaded.toLocaleString()} uploaded · {run.files} files{run.delete_source ? ` · ${run.deleted.toLocaleString()} cleaned` : ""}</span>
          {run.last_file_id && <a href={`https://drive.google.com/open?id=${encodeURIComponent(run.last_file_id)}`} target="_blank" rel="noreferrer">Open latest file</a>}
          {run.error && <span role="alert">{run.error}</span>}
        </div></li>)}
    </ul></div>}
    {showChoice && saved && <dialog ref={dialog} className="confirm-dialog drive-backup__dialog" aria-labelledby="drive-backup-choice" onCancel={(e) => { e.preventDefault(); if (!busy) void choose("cancel"); }}>
      <h2 id="drive-backup-choice">{saved.first_backup_pending ? "Back up existing requests?" : "Start a Google Drive backup?"}</h2>
      <p>{selectedData(saved)} will be uploaded to <strong>{saved.folder_name}</strong> in the background. Choose whether to keep uploaded input/output on the VPS.</p>
      <p>Request metadata and daily/lifetime usage meters are always kept. Only selected, verified input/output is cleaned up.</p>
      {saved.first_backup_pending && saved.scheduled && <p>Cancel skips this one-time backup. The saved daily schedule will still apply.</p>}
      <div className="drive-backup__actions">
        <Button variant="quiet" ref={cancelChoice} disabled={busy} onClick={() => void choose("cancel")}>Cancel</Button>
        <Button disabled={busy} onClick={() => void choose("upload")}>Upload only</Button>
        <Button variant="danger" disabled={busy} onClick={() => void choose("upload_delete")}>Upload and delete</Button>
      </div>
    </dialog>}
  </section>;
}
