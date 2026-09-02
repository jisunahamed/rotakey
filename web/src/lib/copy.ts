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

import { type LearnedFact, type Overview, type RatePolicy, type RequestLog } from "../types";
import { formatNumber } from "./format";

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
 *  not say who translates or that the call still works.
 *
 *  Every value `modelCapabilityProfile` writes is here — all eight, not the four
 *  the three protocol rows happened to use. The profile is one map with rows for
 *  streaming, tools and thinking too, and those three carry their own spellings;
 *  with only four names the inspector printed "Not checked yet" over a route that
 *  had been checked, which is the exact failure the phrase table exists to stop. */
const protocolPhrase: Record<string, string> = {
  native: "Native",
  translated: "Translated by the gateway",
  off: "Off",
  /** The provider's own shape supports it and no request has proved it — the
   *  catalog says so, or the probe did not exercise it. */
  native_unverified: "Native, not yet proved",
  /** Streaming: whatever the provider sends is re-shaped into the protocol the
   *  caller asked in, so it is neither native nor off. */
  gateway_normalized: "Normalized by the gateway",
  /** Thinking, on an OpenAI-shaped route reached by an Anthropic caller. The
   *  reasoning does not survive the translation, and nothing is going to make it. */
  unsupported_cross_protocol: "Not carried between protocols",
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

/** The three shapes a request can be put on the wire in, named the way the
 *  documentation an operator would go and read names them. `chat` and
 *  `responses` are two different OpenAI endpoints and `messages` is Anthropic's,
 *  so the bare enum says nothing about which one an SDK will end up using. */
const endpointNames: Record<string, string> = {
  auto: "Chosen by Rotakey",
  chat: "Chat Completions",
  responses: "Responses",
  messages: "Messages"
};

export function endpointLabel(value: string | undefined) {
  return endpointNames[value ?? ""] ?? "";
}

/** Whose API a request was finally sent as. The log stores this as `openai` or
 *  `anthropic`, which names a company rather than the wire format being
 *  debugged, and it is only worth saying at all when it differs from what the
 *  caller asked for. */
export function apiFamilyLabel(value: string | undefined) {
  if (value === "anthropic") return "the Anthropic Messages API";
  if (value === "openai") return "the OpenAI API";
  return "";
}

/** What the gateway taught itself about one route, in a sentence that says what
 *  it is doing now and what made it start.
 *
 *  Both halves are needed. "Sending to Responses" on its own reads as a setting
 *  someone chose and cannot find, which is exactly how the v3.0.0 endpoint-switch
 *  defect stayed hidden for a day: the route said Chat Completions, the gateway
 *  sent Responses, and neither screen was lying about its own half. */
export function learnedFactSentence(fact: LearnedFact) {
  switch (fact.kind) {
    case "prefer_responses":
      return `Sending to ${endpointLabel("responses")}. The provider turned down a ${endpointLabel("chat")} request for this model and named ${endpointLabel("responses")} instead.`;
    case "no_responses":
      return `Sending as ${endpointLabel("chat")}. The provider answered "not found" at ${endpointLabel("responses")}, so Rotakey translates the request rather than failing it.`;
    case "strip_parameters": {
      const names = fact.parameters ?? [];
      return `Removing ${names.join(", ")} from every request. The provider rejected ${names.length === 1 ? "it" : "them"} by name.`;
    }
    case "rename_parameters": {
      const pairs = (fact.renames ?? []).map(([from, to]) => `${from} to ${to}`);
      return `Renaming ${pairs.join(", ")} on ${endpointLabel(fact.endpoint)} requests. The provider named the spelling it accepts.`;
    }
    case "raise_reply_budget": {
      // The number is the fact. Without it the sentence reads as a policy;
      // with it the operator can see the budget a request will actually get,
      // and judge it against what the model costs.
      const floor = formatNumber(Number(fact.parameters?.[0] ?? 0));
      return `Asking for at least ${floor} reply tokens when the caller sets no limit of their own. A smaller budget came back spent entirely on the model's own reasoning, with no visible reply in it.`;
    }
    case "detach_replayed_ids":
      return "Sending the conversation's earlier messages and tool calls without their ids. The provider demanded each id's original reasoning record, which the app that sent the request does not have to give.";
    case "strip_item_fields": {
      // The path, not the bare name. These fields are put there by the caller's
      // own client rather than by Rotakey or by the operator, so the sentence has
      // to say which part of the request lost something or the operator has
      // nowhere to go and look for it.
      const paths = fact.parameters ?? [];
      return `Removing ${paths.join(", ")} from every message in the request. The provider rejected ${paths.length === 1 ? "it" : "them"} by name, and the app that sent the request adds ${paths.length === 1 ? "it" : "them"} on its own.`;
    }
  }
}

/** The limits on a policy, short enough to sit at the end of a key's row.
 *
 *  Two of the seven, because a row that lists all seven is unreadable and no
 *  install sets all seven. The acronyms stay short here and are expanded in the
 *  title through `rateSummaryFull` — the row is a dense grid, and "requests per
 *  minute 60 · tokens per minute 400,000" is a paragraph. */
export function rateSummary(policy: RatePolicy) {
  const set = capacityDimensions.filter((dimension) => policy[dimension] !== null && policy[dimension] !== undefined);
  if (set.length === 0) return "";
  const shown = set.slice(0, 2).map((dimension) => `${dimension.toUpperCase()} ${formatNumber(policy[dimension] as number)}`);
  return `${shown.join(" · ")}${set.length > 2 ? ` +${set.length - 2}` : ""}`;
}

/** The same limits with every acronym spelled out. This is what a preview line
 *  before a save says, and what a dense row carries in its title. */
export function rateSummaryFull(policy: RatePolicy) {
  return capacityDimensions
    .filter((dimension) => policy[dimension] !== null && policy[dimension] !== undefined)
    .map((dimension) => `${formatNumber(policy[dimension] as number)} ${dimensionNames[dimension]}`)
    .join(", ");
}
