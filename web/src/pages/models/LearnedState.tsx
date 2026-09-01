/** What the gateway has taught itself about this route.
 *
 *  This panel exists because of a specific day. v3.0.0 shipped a repair that
 *  switched a route to Responses when the provider asked for it, and cached the
 *  switch for 24 hours — correctly — while sending that endpoint a body it could
 *  not accept. Every screen was truthful about its own half: the route said Chat
 *  Completions, because that is what the route says, and the requests said 400.
 *  Nothing anywhere said the gateway had stopped doing what the route says. The
 *  only way to find out was to read the attempt rows of a failed request and
 *  notice the word "switched".
 *
 *  So the facts are on screen, in sentences, with the expiry that makes them
 *  facts rather than settings — and with one control that drops them, for the
 *  repair that was right last night and is wrong this morning.
 *
 *  It is loaded per route rather than carried on the route row: each fact is a
 *  separate Redis read that cannot be batched with the others, and the providers
 *  list polls. One route open costs one request; every route on every poll would
 *  cost hundreds.
 */

import { useEffect, useState } from "react";
import { api } from "../../api";
import { learnedFactSentence } from "../../lib/copy";
import { errorMessage, formatDuration } from "../../lib/format";
import type { LearnedFact } from "../../types";
import { Button, SectionHeader, useConfirm } from "../../ui";

export function LearnedState({
  routeID,
  alias,
  notify
}: {
  routeID: string;
  alias: string;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const [facts, setFacts] = useState<LearnedFact[] | null>(null);
  const [failed, setFailed] = useState("");
  const [busy, setBusy] = useState(false);
  // Bumped by a successful forget, which is the only thing that changes the
  // answer from this side.
  const [reloadKey, setReloadKey] = useState(0);

  useEffect(() => {
    // The same generation guard the pages use: switching routes quickly can land
    // two replies out of order, and the older one would overwrite the newer.
    let live = true;
    setFacts(null);
    setFailed("");
    api<{ facts: LearnedFact[] }>(`/api/admin/models/${routeID}/learned`)
      .then((payload) => {
        if (live) setFacts(payload.facts ?? []);
      })
      .catch((caught) => {
        if (live) setFailed(errorMessage(caught));
      });
    return () => {
      live = false;
    };
  }, [routeID, reloadKey]);

  const forget = async () => {
    if (
      !(await ask({
        title: "Forget what Rotakey learned about this route?",
        body: `The next request for ${alias} is built from this route's own settings. If the provider still wants the change, Rotakey learns it again from the provider's next answer — at the cost of one failed request.`,
        confirmLabel: "Forget it",
        tone: "primary"
      }))
    )
      return;
    setBusy(true);
    try {
      await api(`/api/admin/models/${routeID}/learned`, { method: "DELETE" });
      notify(`Rotakey forgot what it learned about ${alias}.`);
      setReloadKey((key) => key + 1);
    } catch (caught) {
      notify(errorMessage(caught), "danger");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="mdl-learned">
      <SectionHeader
        level={3}
        title="What Rotakey learned"
        description="Changes Rotakey makes to every request for this route, worked out from the provider's own answers. None of it is saved settings — all of it expires."
        actions={
          facts && facts.length > 0 ? (
            <Button variant="quiet" disabled={busy} onClick={() => void forget()}>
              {busy ? "Forgetting…" : "Forget it"}
            </Button>
          ) : undefined
        }
      />
      {failed !== "" ? (
        <p className="mdl-learned__empty">Rotakey could not read this: {failed}</p>
      ) : facts === null ? (
        <p className="mdl-learned__empty">Reading…</p>
      ) : facts.length === 0 ? (
        <p className="mdl-learned__empty">Nothing. Every request goes out exactly as this route is set up above.</p>
      ) : (
        <ul className="mdl-learned__list">
          {facts.map((fact) => (
            <li key={`${fact.kind}:${fact.endpoint ?? ""}`}>
              <p>{learnedFactSentence(fact)}</p>
              <span>{expiryLine(fact.expires_at)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** How long the fact has left, from an absolute stamp, so the console needs no
 *  clock agreement with the server. A stamp already in the past is a fact Redis
 *  is about to drop, not an error. */
function expiryLine(expiresAt: string) {
  const remaining = new Date(expiresAt).getTime() - Date.now();
  if (Number.isNaN(remaining)) return "Rotakey forgets this within a day.";
  if (remaining <= 0) return "Rotakey is about to forget this.";
  return `Rotakey forgets this in ${formatDuration(remaining)}.`;
}
