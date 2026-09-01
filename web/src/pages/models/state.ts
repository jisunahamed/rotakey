/** What state a model route is in, said once.
 *
 *  The old page decided this inline, in a nine-way conditional that mixed three
 *  sources — what the last check said, what the `capability_status` column says,
 *  and whether any key can actually serve — and invented its own words for each
 *  combination: "cannot serve · no API key is ready", "listed by provider · check
 *  inconclusive", "waiting · healthy API key required", "unverified". None of
 *  those phrases existed anywhere else in the console, and two of them described
 *  the same situation differently depending on whether a sweep had run.
 *
 *  So there is one function that answers "what state", drawing from the kit's one
 *  vocabulary, and one that answers "why", in a whole sentence. Everything on the
 *  page — the row, the dot, the inspector's header, its notice — reads both from
 *  here, which is what stops a row saying "Ready" beside a panel saying it cannot
 *  serve.
 */

import { countOf } from "../../lib/format";
import { readyKeyCount } from "../../lib/keys";
import { endpointLabel } from "../../lib/copy";
import type { Credential, ModelRoute, Provider } from "../../types";
import type { ConsoleState } from "../../ui";

/** A route with the two things every judgement about it needs: the provider it
 *  sits on, and the keys that would serve it. */
export type Route = ModelRoute & { provider: Provider; credentials: Credential[] };

/** What a check just said, held only while the page is open.
 *
 *  It outranks the stored `capability_status` because it is newer — but only
 *  where the route could serve at all, which is why `routeState` consults the
 *  keys first. A route on a provider switched off half a minute ago is not
 *  "Ready" because it answered a probe before that. */
export type CheckResult = {
  state: "checking" | "passed" | "listed" | "blocked" | "failed";
  /** The provider's own words, when it gave any. */
  note?: string;
};

export function routeState(route: Route, check?: CheckResult): ConsoleState {
  if (check?.state === "checking") return "checking";
  if (!route.enabled || !route.provider.enabled) return "disabled";
  if (readyKeyCount(route) === 0) return "unavailable";
  if (check?.state === "failed") return "failed";
  if (check?.state === "blocked") return "unavailable";
  if (check?.state === "passed" || check?.state === "listed") return "healthy";
  if (route.capability_status === "failed") return "failed";
  if (route.capability_status === "probe_verified" || route.capability_status === "catalog_verified") return "healthy";
  return "unverified";
}

/** Why it is in that state, in one sentence, or "" when the state's own phrase
 *  has already said everything there is to say.
 *
 *  Written out rather than assembled from `routeBlockReason`, whose fragments are
 *  built to sit inside a longer sentence — "no API key is ready" under a dot
 *  reading "Cannot be used" is the same fact twice and neither says what to do. */
export function routeStateNote(route: Route, check?: CheckResult): string {
  if (check?.state === "checking") return "";
  if (!route.enabled) return "This route is switched off, so it is not listed and callers asking for it get an error.";
  if (!route.provider.enabled) return `${route.provider.name} is switched off, so nothing on it can serve a request.`;
  if (route.credentials.length === 0) return `${route.provider.name} has no API key, so there is nothing to send this to.`;
  if (readyKeyCount(route) === 0) return `None of ${route.provider.name}'s ${countOf(route.credentials.length, "API key")} can serve a request right now.`;
  if (check?.state === "blocked") return check.note || "Rotakey had nothing to run the check with.";
  if (check?.state === "failed") return check.note || "The provider refused the check.";
  if (check?.state === "listed") return check.note || "The provider lists this model, and the live check came back neither way. The route stays available.";
  if (check?.state === "passed") return "";
  if (route.capability_status === "failed") return route.capability_error || "The provider refused the last check.";
  if (route.capability_status === "catalog_verified") return "The provider lists this model. Nothing has been sent to it yet.";
  return "";
}

/** Which endpoints the provider serves this model at natively.
 *
 *  Only the two the route claims about the *provider*. Whether the alias is also
 *  offered on the Anthropic Messages API is a decision the operator made about
 *  this gateway, not a fact about the upstream, so it belongs in the panel and
 *  not in a row whose other three cells are all facts about the provider. */
export function upstreamEndpoints(route: ModelRoute): string {
  const parts = [
    route.supports_chat ? endpointLabel("chat") : "",
    route.supports_responses ? endpointLabel("responses") : ""
  ].filter((part) => part !== "");
  if (parts.length === 2) return `${endpointLabel("chat")} + ${endpointLabel("responses")}`;
  // Neither claimed is a real configuration and a real problem: Rotakey has no
  // endpoint it can send this to, and the row should say so rather than leave a
  // blank where the other rows carry a fact.
  return parts[0] ?? "no endpoint set";
}
