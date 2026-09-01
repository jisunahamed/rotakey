/** Models — the page that owns the route object.
 *
 *  A model route used to be created on Providers, edited in the Playground's
 *  third pane, and only read here. Three surfaces, three drafts, three ideas of
 *  what a route is: the Playground's editor and this page's list could not even
 *  agree on which routes had failed, and their two bulk-delete buttons deleted
 *  different sets of live public aliases.
 *
 *  Now there is one. Add, edit, check, turn off, limit and delete all happen
 *  here. Providers keeps a read-only list of the routes on a provider and a link
 *  to this page; the Playground sends prompts and changes nothing.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Activity, Plus, RefreshCw, Trash2 } from "lucide-react";
import { api, APIError } from "../../api";
import { errorMessage } from "../../lib/format";
import { useMediaQuery } from "../../lib/hooks";
import { readyKeyCount, routeBlockReason } from "../../lib/keys";
import { normalizeProviders, poolSizeByAlias } from "../../lib/normalize";
import { useRoutingMode } from "../../lib/routing-mode";
import { useTitleDetail, useURLState, type Page } from "../../routes";
import type { Provider } from "../../types";
import {
  Button,
  Cell,
  DataTable,
  Dot,
  Empty,
  Menu,
  MenuItem,
  Notice,
  Row,
  SearchInput,
  SectionHeader,
  states,
  Toolbar,
  useConfirm,
  useDrawerOverlay,
  useFilterHotkey,
  useListKeys,
  Workbench,
  WorkbenchFrame
} from "../../ui";
import { RouteInspector } from "./RouteInspector";
import { RouteSheet } from "./RouteForm";
import { routeState, routeStateNote, upstreamEndpoints, type CheckResult, type Route } from "./state";

export { RouteSheet, routeDraftFrom, type RouteDraft } from "./RouteForm";

export function ModelsPage({
  navigate,
  notify
}: {
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const listKeys = useListKeys();
  const filterField = useRef<HTMLInputElement | null>(null);
  useFilterHotkey(filterField);
  const routingMode = useRoutingMode();

  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [providerFilter, setProviderFilter] = useState("");
  const [selectedID, setSelectedID] = useURLState("model");
  const [sheet, setSheet] = useState<"none" | "add" | "edit">("none");

  // Below 900px the inspector covers the list, so it takes focus, keeps Tab
  // inside itself and closes on Escape like any other overlay.
  const [inspectorOpen, setInspectorOpen] = useState(false);
  const inspectorFloating = useMediaQuery("(max-width: 900px)");
  const drawer = useDrawerOverlay({
    open: inspectorOpen,
    active: inspectorFloating,
    onClose: () => setInspectorOpen(false)
  });

  const [checks, setChecks] = useState<Record<string, CheckResult>>({});
  const [sweep, setSweep] = useState({ done: 0, total: 0 });
  const [sweeping, setSweeping] = useState(false);
  const [deletingFailed, setDeletingFailed] = useState(false);
  // What the live region says, as opposed to the counter the eye reads. A sweep of
  // two hundred routes announced every single one; this speaks at quarters.
  const [sweepNote, setSweepNote] = useState("");
  // A sweep issues one request per route and there is no way to abort them all, so
  // it is stopped between routes: the loop checks this before taking the next one.
  const stopSweep = useRef(false);
  // Set false on unmount so the sweep stops writing to state and raising toasts for
  // a page the operator has already left.
  const mounted = useRef(true);
  useEffect(
    () => () => {
      mounted.current = false;
      stopSweep.current = true;
    },
    []
  );

  // Loads are versioned so a slow reply cannot overwrite a newer one. A route
  // deleted from the inspector triggers an immediate reload while a background one
  // is still in flight; without this the older list wins and the deleted route is
  // back on screen and re-selected.
  const generation = useRef(0);
  // Only the first load blanks the page. A reload after a check or a delete keeps
  // the list and the open inspector in place, because replacing the workbench with
  // a skeleton throws away the operator's position mid-task.
  const load = useCallback(
    async (background = false) => {
      const mine = ++generation.current;
      if (!background) setLoading(true);
      try {
        const result = await api<{ providers: Provider[] }>("/api/admin/providers");
        if (mine !== generation.current) return;
        const normalized = normalizeProviders(result.providers);
        setProviders(normalized);
        setError("");
        const available = normalized.flatMap((provider) => provider.models);
        setSelectedID(
          (current) => (available.some((route) => route.id === current) ? current : available[0]?.id || ""),
          "replace"
        );
      } catch (caught) {
        if (mine !== generation.current) return;
        setError(errorMessage(caught));
        if (!background) notify(errorMessage(caught), "danger");
      } finally {
        if (mine === generation.current) setLoading(false);
      }
    },
    [notify, setSelectedID]
  );
  const reload = useCallback(() => load(true), [load]);
  useEffect(() => {
    void load();
  }, [load]);

  const routes: Route[] = useMemo(
    () =>
      providers.flatMap((provider) =>
        provider.models.map((route) => ({ ...route, provider, credentials: provider.credentials }))
      ),
    [providers]
  );
  const poolSizes = poolSizeByAlias(providers);
  const needle = query.trim().toLowerCase();
  const filtered = routes.filter((route) => {
    const matchesProvider = !providerFilter || route.provider.id === providerFilter;
    const matchesQuery =
      !needle ||
      route.public_alias.toLowerCase().includes(needle) ||
      route.upstream_model.toLowerCase().includes(needle) ||
      route.provider.name.toLowerCase().includes(needle);
    return matchesProvider && matchesQuery;
  });
  const selected = routes.find((route) => route.id === selectedID);
  useTitleDetail(selected?.public_alias ?? "");

  const ready = routes.filter((route) => readyKeyCount(route) > 0).length;
  const wrong = routes.filter((route) => routeState(route, checks[route.id]) === "failed").length;

  /** The routes a delete would take: ones that could serve and did not. A route
   *  with no ready key failed for a reason that has nothing to do with the route,
   *  and deleting it would be deleting the wrong thing. */
  const failedIDs = routes
    .filter(
      (route) =>
        readyKeyCount(route) > 0 &&
        (checks[route.id]?.state === "failed" || (!checks[route.id] && route.capability_status === "failed"))
    )
    .map((route) => route.id);

  const checkEverything = async () => {
    if (sweeping || routes.length === 0) return;
    const targets = routes.filter((route) => readyKeyCount(route) > 0);
    const skipped = routes.filter((route) => readyKeyCount(route) === 0);
    // A route that cannot serve states its own reason. "Waiting for a healthy API
    // key" is a lie when the provider is switched off or has no keys at all.
    setChecks(
      Object.fromEntries(
        routes.map((route) => [
          route.id,
          readyKeyCount(route) > 0
            ? ({ state: "checking" } as CheckResult)
            : ({ state: "blocked", note: routeBlockReason(route) } as CheckResult)
        ])
      )
    );
    setSweep({ done: skipped.length, total: routes.length });
    setSweeping(true);
    stopSweep.current = false;
    setSweepNote(`Checking ${targets.length} route${targets.length === 1 ? "" : "s"}.`);

    let cursor = 0;
    let done = 0;
    let passed = 0;
    let listed = 0;
    let failed = 0;
    // One announcement per route is two hundred announcements on a real install,
    // which is a screen reader talking over itself for a minute. The bar keeps
    // ticking for the eye; the live region speaks at quarters and at the end.
    const milestone = Math.max(1, Math.ceil(targets.length / 4));
    while (cursor < targets.length) {
      if (stopSweep.current || !mounted.current) break;
      const route = targets[cursor++];
      try {
        const result = await api<{ warning?: string }>(`/api/admin/models/${route.id}/probe`, { method: "POST" });
        if (result.warning) {
          listed++;
          if (mounted.current) setChecks((current) => ({ ...current, [route.id]: { state: "listed", note: result.warning } }));
        } else {
          passed++;
          if (mounted.current) setChecks((current) => ({ ...current, [route.id]: { state: "passed" } }));
        }
      } catch (caught) {
        const blocked = caught instanceof APIError && caught.code === "model_probe_blocked";
        if (!blocked) failed++;
        if (mounted.current) {
          setChecks((current) => ({
            ...current,
            [route.id]: { state: blocked ? "blocked" : "failed", note: errorMessage(caught) }
          }));
        }
      } finally {
        done++;
        if (mounted.current) {
          setSweep({ done: done + skipped.length, total: routes.length });
          if (done % milestone === 0 && done < targets.length) setSweepNote(`${done} of ${targets.length} checked.`);
        }
      }
    }
    // The page can be left while a sweep of two hundred routes is running, and
    // every write after that point is against a component that is gone.
    if (!mounted.current) return;
    setSweeping(false);
    const stopped = stopSweep.current;
    const summary = `${passed} answered${listed ? `, ${listed} listed by the provider` : ""}${
      failed ? `, ${failed} refused` : ""
    }${skipped.length ? `, ${skipped.length} could not be checked` : ""}.`;
    setSweepNote(stopped ? `Stopped after ${done} of ${targets.length}. ${summary}` : summary);
    notify(stopped ? `Stopped after ${done} of ${targets.length}. ${summary}` : summary, failed ? "danger" : "success");
    await load(true);
  };

  const deleteFailed = async () => {
    if (deletingFailed || failedIDs.length === 0) return;
    const aliases = routes.filter((route) => failedIDs.includes(route.id)).map((route) => route.public_alias);
    // The dialog gets the whole list in a scrolling block rather than the twelve a
    // window.confirm() could fit: this is the one action that deletes many routes
    // at once, so the operator has to be able to read what goes.
    if (
      !(await ask({
        title: `Delete ${aliases.length} route${aliases.length === 1 ? "" : "s"} the provider refused?`,
        body: "Requests using these names stop immediately, and the routes cannot be restored.",
        confirmLabel: `Delete ${aliases.length} route${aliases.length === 1 ? "" : "s"}`,
        detail: aliases.join("\n")
      }))
    )
      return;
    setDeletingFailed(true);
    let deleted = 0;
    let refused = 0;
    for (const id of failedIDs) {
      try {
        await api(`/api/admin/models/${id}`, { method: "DELETE" });
        deleted++;
        setChecks((current) => {
          const next = { ...current };
          delete next[id];
          return next;
        });
      } catch (caught) {
        refused++;
        setChecks((current) => ({ ...current, [id]: { state: "failed", note: `Could not delete: ${errorMessage(caught)}` } }));
      }
    }
    setDeletingFailed(false);
    notify(
      `${deleted} route${deleted === 1 ? "" : "s"} deleted${refused ? `, ${refused} could not be deleted` : ""}.`,
      refused ? "danger" : "success"
    );
    await load(true);
  };

  const open = (route: Route) => {
    setSelectedID(route.id);
    setInspectorOpen(true);
  };

  const addRoute = (
    <Button onClick={() => setSheet("add")} disabled={providers.length === 0}>
      <Plus size={14} aria-hidden="true" /> Add a route
    </Button>
  );

  return (
    <div className="mdl-page">
      <SectionHeader
        level={1}
        title="Models"
        meta={
          routes.length > 0
            ? `${routes.length} route${routes.length === 1 ? "" : "s"} · ${ready} ready${wrong ? ` · ${wrong} need attention` : ""}`
            : undefined
        }
        actions={addRoute}
      />
      <p className="sr-only" role="status" aria-live="polite">
        {sweepNote}
      </p>

      {loading ? (
        <Empty level={2} size="page" title="Loading routes…" description="Reading every provider and the keys on it." />
      ) : error !== "" && routes.length === 0 ? (
        <Empty
          level={2}
          size="page"
          title="The routes could not be loaded"
          description={error}
          action={
            <Button onClick={() => void load()}>
              <RefreshCw size={14} aria-hidden="true" /> Try again
            </Button>
          }
        />
      ) : providers.length === 0 ? (
        <Empty
          level={2}
          size="page"
          title="There are no providers yet"
          description="A route sends one model name to one provider, so a provider has to exist first."
          action={<Button onClick={() => navigate("providers")}>Add a provider</Button>}
        />
      ) : routes.length === 0 ? (
        <Empty
          level={2}
          size="page"
          title="No model routes yet"
          description="A route gives one model a public name your callers ask for, and decides which provider serves it."
          action={addRoute}
        />
      ) : (
        <WorkbenchFrame>
          {error !== "" && <Notice tone="warning">{error} This is the last list that loaded.</Notice>}
          <Workbench
            inspectorOpen={inspectorOpen && Boolean(selected)}
            list={
              <>
                <Toolbar label="Filter routes">
                  <SearchInput
                    ref={filterField}
                    value={query}
                    onChange={setQuery}
                    label="Filter routes"
                    placeholder="A name, a model id or a provider"
                  />
                  <label className="mdl-filter">
                    <span className="sr-only">Provider</span>
                    <select value={providerFilter} onChange={(event) => setProviderFilter(event.target.value)}>
                      <option value="">Every provider</option>
                      {providers.map((provider) => (
                        <option key={provider.id} value={provider.id}>
                          {provider.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <Menu label="Actions for every route shown" align="end">
                    {sweeping ? (
                      <MenuItem
                        icon={<Activity size={14} aria-hidden="true" />}
                        onSelect={() => {
                          stopSweep.current = true;
                        }}
                      >
                        Stop checking
                      </MenuItem>
                    ) : (
                      <MenuItem icon={<Activity size={14} aria-hidden="true" />} onSelect={checkEverything}>
                        Check every route
                      </MenuItem>
                    )}
                    <MenuItem
                      tone="danger"
                      icon={<Trash2 size={14} aria-hidden="true" />}
                      disabled={sweeping || deletingFailed || failedIDs.length === 0}
                      onSelect={deleteFailed}
                    >
                      {failedIDs.length === 0
                        ? "No refused routes to delete"
                        : `Delete the ${failedIDs.length} route${failedIDs.length === 1 ? "" : "s"} the provider refused`}
                    </MenuItem>
                  </Menu>
                </Toolbar>

                {/* Only while a sweep is running, or the page carries a bar at 0%
                    that reads as something stuck rather than as nothing happening. */}
                {sweeping && (
                  <div
                    className="mdl-sweep"
                    role="progressbar"
                    aria-label="Checking routes"
                    aria-valuemin={0}
                    aria-valuemax={sweep.total}
                    aria-valuenow={sweep.done}
                  >
                    <span style={{ width: `${sweep.total ? (sweep.done / sweep.total) * 100 : 0}%` }} />
                  </div>
                )}

                <DataTable
                  label="Model routes"
                  columns="minmax(0, 1.6fr) minmax(0, 1.2fr) minmax(0, 0.9fr) 5.5rem"
                  onKeyDown={listKeys}
                  actions
                  head={
                    // A filter that matches nothing used to leave a blank
                    // rectangle under a row of column captions, which reads as a
                    // failed load rather than as an answer.
                    filtered.length > 0 ? (
                      <>
                        <Cell>Name callers ask for</Cell>
                        <Cell>Model at the provider</Cell>
                        <Cell>Provider</Cell>
                        <Cell align="end">Keys ready</Cell>
                      </>
                    ) : undefined
                  }
                >
                  {filtered.length === 0 ? (
                    <Empty
                      level={3}
                      size="pane"
                      title="No route matches"
                      description={`${routes.length} route${routes.length === 1 ? "" : "s"} exist and none of them match what is in the filters.`}
                      action={
                        <Button
                          variant="quiet"
                          onClick={() => {
                            setQuery("");
                            setProviderFilter("");
                          }}
                        >
                          Clear the filters
                        </Button>
                      }
                    />
                  ) : (
                    filtered.map((route) => {
                      const check = checks[route.id];
                      const state = routeState(route, check);
                      const note = routeStateNote(route, check);
                      const pooled = routingMode === "model" ? poolSizes[route.public_alias] ?? 1 : 1;
                      const keys = `${readyKeyCount(route)}/${route.credentials.length}`;
                      return (
                        <Row
                          key={route.id}
                          selected={selectedID === route.id}
                          onClick={() => open(route)}
                          title={`${route.public_alias} — ${states[state].phrase}`}
                        >
                          <Cell
                            icon={<Dot state={state} label="" />}
                            sub={note || states[state].phrase}
                            title={route.public_alias}
                          >
                            {route.public_alias}
                          </Cell>
                          <Cell sub={upstreamEndpoints(route)} title={route.upstream_model}>
                            {route.upstream_model}
                          </Cell>
                          <Cell
                            sub={pooled > 1 ? `one of ${pooled} serving this name` : undefined}
                            title={route.provider.name}
                          >
                            {route.provider.name}
                          </Cell>
                          <Cell align="end" figure title={`${keys} keys can serve this route`}>
                            {keys}
                          </Cell>
                        </Row>
                      );
                    })
                  )}
                </DataTable>
              </>
            }
            inspector={
              selected ? (
                <RouteInspector
                  ref={drawer as React.Ref<HTMLElement>}
                  route={selected}
                  pooled={routingMode === "model" ? poolSizes[selected.public_alias] ?? 1 : 1}
                  check={checks[selected.id]}
                  onClose={() => setInspectorOpen(false)}
                  onEdit={() => setSheet("edit")}
                  onChecked={(result) => setChecks((current) => ({ ...current, [selected.id]: result }))}
                  reload={reload}
                  navigate={navigate}
                  notify={notify}
                />
              ) : (
                <Empty
                  level={2}
                  size="pane"
                  title="Nothing open"
                  description="Choose a route to see what it sends, which key serves it next, and what Rotakey has learned about it."
                />
              )
            }
          />
        </WorkbenchFrame>
      )}

      {sheet !== "none" && (
        <RouteSheet
          providers={providers}
          providerID={sheet === "edit" && selected ? selected.provider.id : providerFilter || providers[0]?.id || ""}
          route={sheet === "edit" ? selected : undefined}
          onClose={() => setSheet("none")}
          onComplete={(message) => {
            setSheet("none");
            notify(message);
            void reload();
          }}
          notify={notify}
        />
      )}
    </div>
  );
}
