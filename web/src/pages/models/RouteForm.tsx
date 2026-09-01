/** The one place a model route is written.
 *
 *  There used to be three: this sheet, opened from a provider; a full editor in
 *  the playground's third pane; and a create step inside the provider wizard.
 *  They drifted, as three copies do — the playground's power button PUT the whole
 *  unsaved draft alongside the flag it meant to change, so switching a route off
 *  committed whatever half-typed alias was in the form beside it.
 *
 *  Models owns the route object now. The Providers page still opens this sheet,
 *  because adding a route while looking at the provider that serves it is the
 *  right moment to do it — it just opens the same one.
 */

import { useEffect, useRef, useState } from "react";
import { Trash2 } from "lucide-react";
import { api } from "../../api";
import { deleteRouteQuestion } from "../../lib/copy";
import { errorMessage } from "../../lib/format";
import { useRoutingMode } from "../../lib/routing-mode";
import type { ModelRoute, Provider } from "../../types";
import {
  Button,
  Field,
  FieldPair,
  FieldStack,
  Sheet,
  Toggle,
  useConfirm
} from "../../ui";

/** Everything about a route that the operator sets. The columns left out are the
 *  ones the gateway writes for itself — what a check found, when it ran — and a
 *  form that offered them would be offering to lie about them. */
export type RouteDraft = Omit<
  ModelRoute,
  "id" | "provider_id" | "created_at" | "updated_at" | "capability_status" | "capability_profile" | "capabilities_checked_at" | "capability_error"
> & { manual?: boolean };

export function routeDraftFrom(route: ModelRoute): RouteDraft {
  return {
    public_alias: route.public_alias,
    upstream_model: route.upstream_model,
    supports_chat: route.supports_chat,
    supports_responses: route.supports_responses,
    supports_messages: route.supports_messages,
    default_max_output_tokens: route.default_max_output_tokens,
    tokenizer: route.tokenizer,
    input_cost_per_million_usd: route.input_cost_per_million_usd,
    output_cost_per_million_usd: route.output_cost_per_million_usd,
    request_cost_usd: route.request_cost_usd,
    capture_bodies: route.capture_bodies,
    strip_parameters: route.strip_parameters ?? [],
    enabled: route.enabled
  };
}

/** A price the operator types. Cleared means "no charge", which `Number("")`
 *  turns into 0 — the same value — so these three can commit on every keystroke.
 *  The reply limit below cannot, which is why it is not in here. */
function money(raw: string) {
  const parsed = Number(raw);
  return raw.trim() === "" || !Number.isFinite(parsed) || parsed < 0 ? 0 : parsed;
}

function RouteFields({
  value,
  onChange
}: {
  value: RouteDraft;
  onChange: (next: RouteDraft) => void;
}) {
  // Held as text so that clearing the box leaves it cleared. `Number("")` is 0,
  // and a reply limit of 0 is a route that answers with nothing.
  const [limit, setLimit] = useState(String(value.default_max_output_tokens));
  useEffect(() => setLimit(String(value.default_max_output_tokens)), [value.default_max_output_tokens]);
  const limitValue = Number(limit);
  const limitBad = limit.trim() === "" || !Number.isInteger(limitValue) || limitValue < 1;

  return (
    <FieldStack>
      <Field label="Public name" hint="what callers put in the model field">
        <input
          required
          placeholder="groq/llama-3.3-70b"
          value={value.public_alias}
          onChange={(event) => onChange({ ...value, public_alias: event.target.value })}
        />
      </Field>
      <Field label="Model name at the provider" hint="the provider's own id">
        <input
          required
          placeholder="llama-3.3-70b-versatile"
          value={value.upstream_model}
          onChange={(event) => onChange({ ...value, upstream_model: event.target.value })}
        />
      </Field>
      <p className="mdl-form__note">
        On Azure and Azure AI Foundry this is the deployment name you created, which is often not the vendor's name for
        the model.
      </p>

      <FieldPair>
        <Field
          label="Reply limit"
          hint="tokens"
          error={limitBad ? "Enter a whole number of tokens, 1 or more." : ""}
        >
          {(control) => (
            <input
              {...control}
              type="number"
              min={1}
              value={limit}
              onChange={(event) => {
                setLimit(event.target.value);
                const next = Number(event.target.value);
                if (Number.isInteger(next) && next >= 1) onChange({ ...value, default_max_output_tokens: next });
              }}
            />
          )}
        </Field>
        <Field label="How tokens are counted" hint="used for limits and cost">
          <select value={value.tokenizer} onChange={(event) => onChange({ ...value, tokenizer: event.target.value })}>
            <option value="heuristic">Estimate — works for any model</option>
            <option value="cl100k_base">cl100k_base — GPT-4 and GPT-3.5</option>
            <option value="o200k_base">o200k_base — GPT-4o and newer</option>
          </select>
        </Field>
      </FieldPair>
      <p className="mdl-form__note">
        The reply limit is what Rotakey sends when a caller does not ask for one, and what it reserves against a token
        limit before the request goes out.
      </p>

      <FieldPair columns={3}>
        <Field label="Input price" hint="USD per million tokens">
          <input
            type="number"
            min={0}
            step="0.000001"
            value={value.input_cost_per_million_usd}
            onChange={(event) => onChange({ ...value, input_cost_per_million_usd: money(event.target.value) })}
          />
        </Field>
        <Field label="Output price" hint="USD per million tokens">
          <input
            type="number"
            min={0}
            step="0.000001"
            value={value.output_cost_per_million_usd}
            onChange={(event) => onChange({ ...value, output_cost_per_million_usd: money(event.target.value) })}
          />
        </Field>
        <Field label="Price per request" hint="USD, optional">
          <input
            type="number"
            min={0}
            step="0.000001"
            placeholder="None"
            value={value.request_cost_usd ?? ""}
            onChange={(event) =>
              onChange({
                ...value,
                request_cost_usd: event.target.value.trim() === "" ? undefined : money(event.target.value)
              })
            }
          />
        </Field>
      </FieldPair>
      <p className="mdl-form__note">
        Prices are what the spend figures on Overview are worked out from. Leave them at 0 and Rotakey counts tokens
        without costing them.
      </p>

      <Toggle
        checked={value.supports_chat}
        onChange={(supports_chat) => onChange({ ...value, supports_chat })}
        label="The provider serves this model at Chat Completions"
        description="Almost every provider does. Turn it off only for one that answers at Responses and nowhere else."
      />
      <Toggle
        checked={value.supports_responses}
        onChange={(supports_responses) => onChange({ ...value, supports_responses })}
        label="The provider serves this model at Responses"
        description="Off means Rotakey answers a Responses call by translating it to Chat Completions. Callers see no difference; it is one extra step."
      />
      <Toggle
        checked={value.supports_messages}
        onChange={(supports_messages) => onChange({ ...value, supports_messages })}
        label="Offer this model on the Anthropic Messages API"
        description="Lets callers reach this name at /v1/messages. Text, images and client-side tools translate without loss; anything else does not."
      />

      <Field label="Fields to remove before sending" hint="comma separated, this route only">
        <input
          placeholder="thinking"
          value={value.strip_parameters.join(", ")}
          onChange={(event) =>
            onChange({
              ...value,
              strip_parameters: event.target.value
                .split(",")
                .map((item) => item.trim())
                .filter(Boolean)
            })
          }
        />
      </Field>
      <p className="mdl-form__note">
        Top-level names only, for a provider that rejects a field the caller keeps sending. Rotakey also removes fields a
        provider has rejected by name on its own — the panel for this route lists those separately.
      </p>

      <Toggle
        checked={value.capture_bodies}
        onChange={(capture_bodies) => onChange({ ...value, capture_bodies })}
        label="Keep the request and reply text"
        description="Off by default. Stored encrypted and deleted on the same schedule as the rest of the request history."
      />
      <Toggle
        checked={value.enabled}
        onChange={(enabled) => onChange({ ...value, enabled })}
        label="Serve this route"
        description="Off takes the name out of /v1/models, and a caller asking for it gets an error."
      />
    </FieldStack>
  );
}

export function RouteSheet({
  providers,
  providerID,
  route,
  onClose,
  onComplete,
  notify
}: {
  /** What can be chosen from. One entry means there is no choice to make, which
   *  is the Providers page opening this from inside a provider. */
  providers: Provider[];
  providerID: string;
  /** Absent when creating. */
  route?: ModelRoute;
  onClose: () => void;
  onComplete: (message: string) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const routingMode = useRoutingMode();
  const [chosenID, setChosenID] = useState(providerID);
  // A route cannot move between providers — there is no endpoint for it — so an
  // open route keeps the provider it was opened on whatever the list says.
  const provider = providers.find((item) => item.id === (route ? providerID : chosenID)) ?? providers[0];
  const [draft, setDraft] = useState<RouteDraft>(
    route
      ? routeDraftFrom(route)
      : {
          public_alias: `${provider?.slug ?? ""}/`,
          upstream_model: "",
          supports_chat: true,
          supports_responses: false,
          supports_messages: true,
          default_max_output_tokens: 1024,
          tokenizer: "heuristic",
          input_cost_per_million_usd: 0,
          output_cost_per_million_usd: 0,
          request_cost_usd: undefined,
          capture_bodies: false,
          strip_parameters: [],
          enabled: true
        }
  );
  const [busy, setBusy] = useState(false);
  const [deleting, setDeleting] = useState(false);
  // Dirty is "the operator changed something", not "the draft differs from what
  // it was on the first render". The two are not the same here: the default name
  // below is corrected after mount, once the routing mode and the chosen provider
  // are both known, and a comparison against the first render would call that
  // correction the operator's work and refuse to close without asking.
  const [touched, setTouched] = useState(false);
  const edit = (next: RouteDraft) => {
    setTouched(true);
    setDraft(next);
  };

  /** The name a new route starts with, which depends on two things that are not
   *  settled at the first render: the routing mode, which arrives from the
   *  server, and which provider is selected, which the operator can change.
   *
   *  Model-wise routing pools every provider publishing the same name, so the
   *  provider slug that keeps two copies of a model apart is exactly the thing
   *  that must not be there. Only the untouched default is replaced — anything
   *  typed is the operator's. */
  const suggested = useRef(`${provider?.slug ?? ""}/`);
  useEffect(() => {
    if (route) return;
    const next = routingMode === "model" ? "" : `${provider?.slug ?? ""}/`;
    const previous = suggested.current;
    if (next === previous) return;
    suggested.current = next;
    setDraft((current) => (current.public_alias === previous ? { ...current, public_alias: next } : current));
  }, [route, provider?.slug, routingMode]);

  const save = async () => {
    if (!provider) return;
    setBusy(true);
    try {
      if (route) await api(`/api/admin/models/${route.id}`, { method: "PUT", json: draft });
      else await api(`/api/admin/providers/${provider.id}/models`, { method: "POST", json: draft });
      onComplete(route ? `${draft.public_alias} saved.` : `${draft.public_alias} added on ${provider.name}.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (!route) return;
    if (!(await ask(deleteRouteQuestion(route.public_alias)))) return;
    setDeleting(true);
    try {
      await api(`/api/admin/models/${route.id}`, { method: "DELETE" });
      onComplete(`${route.public_alias} deleted.`);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setDeleting(false);
    }
  };

  return (
    <Sheet
      title={route ? "Edit model route" : "Add a model route"}
      eyebrow={provider?.name}
      onClose={onClose}
      dirty={touched}
      discardMessage="Close this panel? What you have typed here is not saved yet."
      actions={
        route ? (
          <Button variant="danger" disabled={deleting} onClick={() => void remove()}>
            <Trash2 size={14} aria-hidden="true" /> {deleting ? "Deleting…" : "Delete route"}
          </Button>
        ) : undefined
      }
    >
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        <FieldStack>
          {!route && providers.length > 1 && (
            <Field label="Provider" hint="which upstream serves this model">
              <select value={chosenID} onChange={(event) => setChosenID(event.target.value)}>
                {providers.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </select>
            </Field>
          )}
        </FieldStack>
        <RouteFields value={draft} onChange={edit} />
        <div className="sheet-actions">
          <span />
          <Button type="submit" disabled={busy || !provider}>
            {busy ? "Saving…" : route ? "Save route" : "Add route"}
          </Button>
        </div>
      </form>
    </Sheet>
  );
}
