/** The one editor for a route's per-key limits.
 *
 *  There were two, and they disagreed about the most consequential thing either of
 *  them did. The key panel's editor set a limit on the one key it was opened from;
 *  this one defaulted its scope select to "All API keys" — so the same-looking
 *  Save, in the same-looking box, either changed one key or rewrote the whole
 *  pool, and the only way to know which was to notice which page you were on.
 *
 *  So the default here is nothing. The operator names the scope before there is
 *  anything to save, a line above the button says in words what the save will
 *  write and where, and the fan-out still asks. A control this destructive should
 *  cost one deliberate choice, not zero.
 */

import { useEffect, useRef, useState } from "react";
import { api } from "../../api";
import { rateSummaryFull } from "../../lib/copy";
import { countOf, errorMessage } from "../../lib/format";
import { emptyPolicy, type Credential, type RatePolicy } from "../../types";
import { Button, Field, Notice, RateFields, SectionHeader, useConfirm } from "../../ui";
import type { Route } from "./state";

/** Two policies are the same limit when all seven dimensions agree. Written out
 *  rather than compared as JSON, because `emptyPolicy()` writes nulls and the
 *  server sends absent fields for the same thing. */
function samePolicy(left: RatePolicy, right: RatePolicy) {
  return (["rps", "rpm", "rpd", "tps", "tpm", "tpd", "tpr"] as const).every(
    (dimension) => (left[dimension] ?? null) === (right[dimension] ?? null)
  );
}

function hasLimit(policy: RatePolicy) {
  return Object.values(policy).some((value) => value !== null && value !== undefined);
}

export function LimitEditor({
  route,
  notify,
  onSaved
}: {
  route: Route;
  notify: (message: string, tone?: "success" | "danger") => void;
  onSaved: () => Promise<void>;
}) {
  const ask = useConfirm();
  const credentials = route.credentials;
  // "" is not a key and not "all" — it is the operator not having chosen yet,
  // which is the state this used to skip straight past.
  const [target, setTarget] = useState("");
  const [draft, setDraft] = useState<RatePolicy>(emptyPolicy());
  const [busy, setBusy] = useState(false);

  // The page reloads in the background, which hands this a fresh credentials
  // array every few seconds. Reading it through a ref keeps a reload the operator
  // did not ask for from resetting a half-typed limit.
  const credentialsRef = useRef(credentials);
  credentialsRef.current = credentials;

  useEffect(() => {
    setTarget("");
    setDraft(emptyPolicy());
  }, [route.id]);

  useEffect(() => {
    const pool = credentialsRef.current;
    if (target === "") {
      setDraft(emptyPolicy());
      return;
    }
    if (target === "all") {
      // Load the pool's limit only when there is one shared answer to load. Where
      // the keys disagree, showing any one of their limits would invite the
      // operator to save it onto the others without ever being told they differed.
      const first = pool[0]?.model_limits[route.id] ?? emptyPolicy();
      const agree = pool.every((credential) => samePolicy(credential.model_limits[route.id] ?? emptyPolicy(), first));
      setDraft(agree ? first : emptyPolicy());
      return;
    }
    setDraft(pool.find((credential) => credential.id === target)?.model_limits[route.id] ?? emptyPolicy());
  }, [target, route.id]);

  const chosen: Credential | undefined = credentials.find((credential) => credential.id === target);
  const targets = target === "all" ? credentials : chosen ? [chosen] : [];
  const scope = target === "all" ? `all ${countOf(credentials.length, "API key")} on ${route.provider.name}` : chosen?.label ?? "";
  const mixed =
    target === "all" &&
    credentials.length > 1 &&
    !credentials.every((credential) =>
      samePolicy(credential.model_limits[route.id] ?? emptyPolicy(), credentials[0].model_limits[route.id] ?? emptyPolicy())
    );

  /** Fanning out to the pool asks; changing one key does not. The blast radius is
   *  the difference, and it is the only difference. */
  const confirmFanOut = async (question: { title: string; body: string; confirmLabel: string }) =>
    target !== "all" || (await ask({ ...question, tone: "primary" }));

  const save = async () => {
    if (targets.length === 0) return;
    if (
      !(await confirmFanOut({
        title: `Set this limit on ${scope}?`,
        body: `Every one of them gets ${rateSummaryFull(draft)} for ${route.public_alias}, replacing whatever it has now. Each key's shared limit still applies on top.`,
        confirmLabel: "Set the limit"
      }))
    )
      return;
    setBusy(true);
    try {
      await Promise.all(
        targets.map((credential) =>
          api(`/api/admin/credentials/${credential.id}/model-limits/${route.id}`, { method: "PUT", json: draft })
        )
      );
      notify(`${route.public_alias} limited on ${countOf(targets.length, "API key")}.`);
      await onSaved();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  const clear = async () => {
    if (targets.length === 0) return;
    if (
      !(await confirmFanOut({
        title: `Use the shared limit on ${scope}?`,
        body: `${route.public_alias}'s own limit is removed from every one of them, and it falls back to each key's shared limit.`,
        confirmLabel: "Use the shared limit"
      }))
    )
      return;
    setBusy(true);
    try {
      await Promise.all(
        targets.map((credential) =>
          api(`/api/admin/credentials/${credential.id}/model-limits/${route.id}`, { method: "DELETE" })
        )
      );
      setDraft(emptyPolicy());
      notify(`${route.public_alias} back on the shared limit for ${countOf(targets.length, "API key")}.`);
      await onSaved();
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  if (credentials.length === 0) {
    return (
      <section className="mdl-limits">
        <SectionHeader
          level={3}
          title="Limit this route"
          description={`${route.provider.name} has no API key yet, so there is nothing to set a limit on.`}
        />
      </section>
    );
  }

  return (
    <section className="mdl-limits">
      <SectionHeader
        level={3}
        title="Limit this route"
        description="A cap that applies only to this model, on top of whatever the key's shared limit already allows."
      />
      <Field label="Which API keys" hint="nothing is saved until you choose">
        <select value={target} onChange={(event) => setTarget(event.target.value)}>
          <option value="">Choose which API keys this applies to…</option>
          <option value="all">All {countOf(credentials.length, "API key")} on {route.provider.name}</option>
          {credentials.map((credential) => (
            <option key={credential.id} value={credential.id}>
              {credential.label}
            </option>
          ))}
        </select>
      </Field>
      {mixed && (
        <Notice tone="warning">
          These keys do not all have the same limit for {route.public_alias} right now. Saving here gives every one of
          them the same one.
        </Notice>
      )}
      {target !== "" && (
        <>
          <RateFields value={draft} onChange={setDraft} compact />
          <p className="mdl-limits__preview">
            {hasLimit(draft)
              ? `Save writes ${rateSummaryFull(draft)} for ${route.public_alias} on ${scope}.`
              : `Nothing is set yet. Fill in at least one limit above, or use the shared limit on ${scope}.`}
          </p>
          <div className="button-row">
            <Button disabled={busy || !hasLimit(draft)} onClick={() => void save()}>
              {busy ? "Saving…" : "Save this limit"}
            </Button>
            <Button variant="quiet" disabled={busy} onClick={() => void clear()}>
              Use the shared limit
            </Button>
          </div>
        </>
      )}
    </section>
  );
}
