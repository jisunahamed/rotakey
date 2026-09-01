/** Words the console says in more than one place.
 *
 *  A string typed twice is a string that will be edited once. Both halves of
 *  every pair below were already on screen at the same time saying different
 *  things — "API key validation failed" in the wizard and something else in the
 *  key panel, two wordings of the same failed catalog load — which reads as two
 *  different problems and sends the operator looking for the second one.
 *
 *  The other job here is that no raw database enum reaches a screen. Values like
 *  `probe_verified` and `stream_interrupted` are how a column is stored; they are
 *  not what happened, and the operator has no way to look them up.
 */

import { type Overview, type RequestLog } from "../types";

/** What the console says when a provider turns a key away. Three call sites showed
 *  this — the credential panel, the provider wizard and the overview inspector —
 *  and all three used to say only "API key validation failed", which names the
 *  step that failed and no way out of it. */
export const keyCheckFailed =
  "The provider rejected this key. Replace it, or check it again if you have just created it.";

/** Loading a provider's catalog fails for one of two reasons and the operator can
 *  act on both, so both are named. The two call sites — the load-models panel's
 *  toast and its notice — used to word this differently, which read as two
 *  different failures. */
export const discoveryFailed =
  "The provider did not return a model list. Check the API key and the base URL, then try again.";

/** The same question is asked from the edit panel and from the models inspector. It
 *  was written out twice, so a change to one wording silently disagreed with the
 *  other about what deleting a route does. */
export function deleteRouteQuestion(alias: string) {
  return {
    title: `Delete route ${alias}?`,
    body: "Requests using this alias stop immediately, and the route cannot be restored — you would add it again from scratch.",
    confirmLabel: "Delete route"
  } as const;
}

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

export function capabilityLabelFor(value: string | undefined) {
  return capabilityPhrase[value ?? "unverified"] ?? capabilityPhrase.unverified;
}

export function protocolLabelFor(value: string | undefined) {
  return protocolPhrase[value ?? "unknown"] ?? protocolPhrase.unknown;
}

/** One attempt's outcome as a single line. An attempt that never reached a status
 *  code carries only an error, and the old expression printed it on both sides of
 *  the separator — "connection_error · connection_error". replaced_parameters was
 *  truthy-checked as an object, so an empty map left a dangling "· replaced". */
export function attemptSummary(attempt: RequestLog["attempts"][number]) {
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
export function routingStageLabel(log: RequestLog) {
  if (log.credential_label) return log.credential_label;
  if (log.provider_name === "gateway") return "Gateway validation";
  if (log.attempts?.length) return "No key was reached";
  return "Rejected before routing";
}

export function humanizeErrorCode(code: string) {
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

/** The seven buckets a provider's capacity is measured in, in the order they are
 *  always shown: requests before tokens, and shortest window first. */
export const capacityDimensions = ["rps", "rpm", "rpd", "tps", "tpm", "tpd", "tpr"] as const;

/** Spoken names for the seven buckets. "RPS" read aloud is three letters, so each
 *  cell of the capacity grid carries the words instead. */
export const dimensionNames: Record<(typeof capacityDimensions)[number], string> = {
  rps: "requests per second",
  rpm: "requests per minute",
  rpd: "requests per day",
  tps: "tokens per second",
  tpm: "tokens per minute",
  tpd: "tokens per day",
  tpr: "tokens per request"
};

/** The range is a URL enum, and reading it out raw produced labels like "all
 *  gateway status" and "Traffic · all". These are the same words the range
 *  switcher's own buttons announce, so a heading and its control agree. */
const rangeNames: Record<Overview["range"], string> = {
  "1h": "Last hour",
  "24h": "Last 24 hours",
  "7d": "Last 7 days",
  all: "All time"
};

export function rangeLabel(range: Overview["range"]) {
  return rangeNames[range] ?? String(range);
}

/** A limit is either the provider's shared key limit or one set for a single model.
 *  The enum reaching the screen said "shared" and "model" with no noun, which reads
 *  as a category rather than as the limit doing the limiting. */
export function limitScopeLabel(scope: string | undefined) {
  return scope === "model" ? "model limit" : "shared key limit";
}
