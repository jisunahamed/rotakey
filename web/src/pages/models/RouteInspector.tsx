/** One route, in full.
 *
 *  What it is, whether it can serve and why not, what Rotakey has taught itself
 *  about it, which key is next, and what limits it. Everything that can be done to
 *  a route is here or in the menu at the top of it — this is the page that owns
 *  the route object, so nothing on it says "go somewhere else to change this".
 *
 *  The two buttons at the bottom of the old panel did exactly that. "Manage model
 *  limits" navigated to the Providers page, away from a limits editor eighty
 *  pixels above it, and "View request logs" left the page entirely. Both are menu
 *  items now, where going somewhere else belongs.
 */

import { useState } from "react";
import { Activity, MessageSquare, Pencil, Power, ScrollText, Trash2 } from "lucide-react";
import { api } from "../../api";
import { capabilityLabelFor, deleteRouteQuestion, protocolLabelFor, rateSummary, rateSummaryFull } from "../../lib/copy";
import { countOf, errorMessage, formatNumber, formatRelativeTime } from "../../lib/format";
import { credentialPoolState } from "../../lib/keys";
import type { Page } from "../../routes";
import {
  Button,
  Disclosure,
  Dot,
  Inspector,
  Menu,
  MenuItem,
  Notice,
  Rotor,
  states,
  Tag,
  useConfirm
} from "../../ui";
import { LearnedState } from "./LearnedState";
import { LimitEditor } from "./LimitEditor";
import { routeDraftFrom } from "./RouteForm";
import { routeState, routeStateNote, type CheckResult, type Route } from "./state";

/** One row of the facts list. Two of these sit side by side in a narrow panel and
 *  the value ellipsises rather than widening the pane, so the full text stays on
 *  the title — a provider name is the usual casualty, and it is the one value the
 *  operator named themselves. */
function Fact({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="mdl-facts__item">
      <dt>{label}</dt>
      <dd className={mono ? "is-mono" : ""} title={value}>
        {value}
      </dd>
    </div>
  );
}

export function RouteInspector({
  route,
  pooled,
  check,
  onClose,
  onEdit,
  onChecked,
  reload,
  navigate,
  notify,
  ref
}: {
  route: Route;
  /** How many provider routes publish this alias, under model-wise routing. 1 or
   *  0 means it is not pooled and the row is not drawn. */
  pooled: number;
  check?: CheckResult;
  onClose: () => void;
  onEdit: () => void;
  /** Records a single route's check result in the page's own map, so the row and
   *  the panel agree about what just happened. */
  onChecked: (result: CheckResult) => void;
  reload: () => Promise<void>;
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
  ref?: React.Ref<HTMLElement>;
}) {
  const ask = useConfirm();
  const [busy, setBusy] = useState(false);
  const state = routeState(route, check);
  const note = routeStateNote(route, check);
  const credentials = route.credentials;

  const probe = async () => {
    setBusy(true);
    onChecked({ state: "checking" });
    try {
      const result = await api<{ warning?: string }>(`/api/admin/models/${route.id}/probe`, { method: "POST" });
      if (result.warning) {
        onChecked({ state: "listed", note: result.warning });
        notify("The provider lists this model. The live check came back neither way, so the route stays available.");
      } else {
        onChecked({ state: "passed" });
        notify(`${route.public_alias} answered.`);
      }
    } catch (caught) {
      onChecked({ state: "failed", note: errorMessage(caught) });
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
      await reload();
    }
  };

  /** The route exactly as it is stored, with one flag flipped. The old power
   *  button on the playground sent the *form's* draft beside the flag, so
   *  switching a route off also committed whatever was half-typed in the editor
   *  next to it. The PUT rewrites the whole row and the admin API refuses unknown
   *  fields, so it has to be a full draft — it just has to be this one. */
  const setEnabled = async (enabled: boolean) => {
    if (
      !enabled &&
      !(await ask({
        title: `Turn off ${route.public_alias}?`,
        body: "It comes out of the model list immediately, and a caller asking for it gets an error until you turn it back on.",
        confirmLabel: "Turn it off",
        tone: "primary"
      }))
    )
      return;
    setBusy(true);
    try {
      await api(`/api/admin/models/${route.id}`, { method: "PUT", json: { ...routeDraftFrom(route), enabled } });
      notify(`${route.public_alias} is ${enabled ? "on" : "off"}.`);
      await reload();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!(await ask(deleteRouteQuestion(route.public_alias)))) return;
    setBusy(true);
    try {
      await api(`/api/admin/models/${route.id}`, { method: "DELETE" });
      notify(`${route.public_alias} deleted.`);
      await reload();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  const [keysOpen, setKeysOpen] = useState(false);

  return (
    <Inspector
      ref={ref}
      level={2}
      title={route.public_alias}
      subtitle={route.upstream_model}
      onClose={onClose}
      meta={
        <>
          <Dot state={state} label="" />
          <span>{states[state].phrase}</span>
          {route.supports_messages && <Tag>Messages API</Tag>}
          {pooled > 1 && <Tag tone="accent">Pooled ×{pooled}</Tag>}
        </>
      }
      actions={
        <>
          <Button variant="quiet" disabled={busy} onClick={() => void probe()}>
            <Activity size={13} aria-hidden="true" /> {busy ? "Working…" : "Check it"}
          </Button>
          <Menu label={`More actions for ${route.public_alias}`}>
            <MenuItem icon={<Pencil size={14} aria-hidden="true" />} onSelect={onEdit}>
              Edit this route
            </MenuItem>
            <MenuItem icon={<Power size={14} aria-hidden="true" />} onSelect={() => setEnabled(!route.enabled)}>
              {route.enabled ? "Turn this route off" : "Turn this route on"}
            </MenuItem>
            <MenuItem
              icon={<MessageSquare size={14} aria-hidden="true" />}
              onSelect={() => navigate("playground", { model: route.id })}
            >
              Send it a prompt
            </MenuItem>
            <MenuItem
              icon={<ScrollText size={14} aria-hidden="true" />}
              onSelect={() => navigate("requests", { q: route.public_alias })}
            >
              See its requests
            </MenuItem>
            <MenuItem tone="danger" icon={<Trash2 size={14} aria-hidden="true" />} onSelect={remove}>
              Delete this route
            </MenuItem>
          </Menu>
        </>
      }
    >
      {note !== "" && (
        <div className="mdl-inspector__notice">
          <Notice tone={states[state].tone === "fault" ? "danger" : "warning"}>{note}</Notice>
        </div>
      )}

      <dl className="mdl-facts">
        <Fact label="Provider" value={route.provider.name} />
        {pooled > 1 && (
          <Fact label="Pooled with" value={`${countOf(pooled - 1, "other provider route")} on the same name`} />
        )}
        <Fact label="Chat Completions" value={protocolLabelFor(route.capability_profile?.chat || (route.supports_chat ? "native" : "off"))} />
        <Fact label="Responses" value={protocolLabelFor(route.capability_profile?.responses || (route.supports_responses ? "native" : "translated"))} />
        <Fact label="Messages" value={protocolLabelFor(route.capability_profile?.messages || (route.supports_messages ? "native" : "off"))} />
        <Fact label="Streaming" value={protocolLabelFor(route.capability_profile?.streaming)} />
        <Fact label="Tools" value={protocolLabelFor(route.capability_profile?.tools)} />
        <Fact label="Thinking" value={protocolLabelFor(route.capability_profile?.thinking)} />
        <Fact label="Reply limit" value={`${formatNumber(route.default_max_output_tokens)} tokens`} />
        <Fact label="Token counting" value={route.tokenizer === "heuristic" ? "Estimate" : route.tokenizer} mono={route.tokenizer !== "heuristic"} />
        <Fact
          label="Last check"
          value={
            route.capabilities_checked_at
              ? `${capabilityLabelFor(route.capability_status)} · ${formatRelativeTime(route.capabilities_checked_at)}`
              : capabilityLabelFor(route.capability_status)
          }
        />
        <Fact label="Request text kept" value={route.capture_bodies ? "Yes" : "No"} />
      </dl>

      {route.strip_parameters.length > 0 && (
        <div className="mdl-inspector__notice">
          <Notice title="Fields you asked Rotakey to remove">
            <code>{route.strip_parameters.join(", ")}</code> is taken out of every request for this route before it is
            sent.
          </Notice>
        </div>
      )}

      <LearnedState routeID={route.id} alias={route.public_alias} notify={notify} />

      <section className="mdl-pool">
        <Rotor
          keys={credentials.map((credential) => ({ id: credential.id, status: credentialPoolState(credential) }))}
          stalled={credentials.length > 0 && !credentials.some((credential) => credentialPoolState(credential) === "healthy")}
          stalledNote="No key can serve this route. Open its provider to check the keys."
        />
        {credentials.length > 0 && (
          <Disclosure
            level={3}
            title="Key order"
            subtitle="The order Rotakey tries them in"
            meta={countOf(credentials.length, "API key")}
            open={keysOpen}
            onToggle={() => setKeysOpen((current) => !current)}
          >
            <ul className="mdl-keys">
              {credentials.map((credential) => {
                const policy = credential.model_limits[route.id] || credential.limits;
                const own = Boolean(credential.model_limits[route.id]);
                const summary = rateSummary(policy);
                return (
                  <li key={credential.id}>
                    <Dot state={credentialPoolState(credential)} label="" />
                    <strong title={credential.label}>{credential.label}</strong>
                    <small>{credential.is_primary ? "First in line" : states[credentialPoolState(credential)].phrase}</small>
                    <span title={summary ? rateSummaryFull(policy) : undefined}>
                      <small>{own ? "its own limit" : "the shared limit"}</small>
                      {summary || "No limit"}
                    </span>
                  </li>
                );
              })}
            </ul>
          </Disclosure>
        )}
      </section>

      <LimitEditor route={route} notify={notify} onSaved={reload} />
    </Inspector>
  );
}
