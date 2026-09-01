/** Whether the router would reach for a key, and why not.
 *
 *  This is one question with one answer, and the console used to give several.
 *  A key can be skipped because the operator switched it off, because its
 *  balance is spent, or because the provider put it in cooldown or quarantine —
 *  and a count, a dot, a rotor segment and a banner that each decide that for
 *  themselves will disagree the first time two reasons apply at once. Everything
 *  below is built from `credentialPoolState`, so they cannot.
 */

import { type Credential } from "../types";
import { formatUSD } from "./format";

/** credentialBalanceNote appends the credit left to a key's row, and returns an
 * empty string for untracked keys so the pool list looks unchanged on installs
 * that do not use balances. */
export function credentialBalanceNote(credential: Credential) {
  if (credential.balance_usd === null || credential.balance_usd === undefined) return "";
  const remaining = Math.max(0, credential.balance_usd - credential.balance_spent_usd);
  return remaining <= 0 ? " · out of balance" : ` · ${formatUSD(remaining)} left`;
}

/** The one state name that describes whether the router will reach for this key,
 *  folding together the three separate reasons it might not: the operator turned it
 *  off, its balance is spent, or the upstream put it in cooldown or quarantine.
 *  The rotor and the status dot both need that single answer — a key that is
 *  `healthy` but out of balance is skipped, and drawing it green would be a lie. */
export function credentialPoolState(credential: Credential) {
  if (!credential.enabled) return "disabled";
  if (credential.balance_usd != null && credential.balance_usd - credential.balance_spent_usd <= 0) return "exhausted";
  return credential.status;
}

/** How many keys the router would actually reach for on this route — the one
 *  definition of "ready", used by every count, dot and label that claims it.
 *  Two contradictory ones used to sit a hundred lines apart on the same page, and
 *  neither asked whether the provider itself was switched on, so a route on a
 *  turned-off provider was drawn green with "3/3 keys ready" beside it. */
export function readyKeyCount(route: { provider: { enabled: boolean }; credentials: Credential[] }) {
  if (!route.provider.enabled) return 0;
  return route.credentials.filter((credential) => credentialPoolState(credential) === "healthy").length;
}

/** The single sentence explaining why a route cannot serve, or "" when it can.
 *  Kept beside `readyKeyCount` because a count of zero always needs the reason:
 *  "waiting for a healthy API key" is wrong when the provider is off, and wrong
 *  again when no key was ever added. */
export function routeBlockReason(route: { provider: { enabled: boolean; name: string }; credentials: Credential[]; enabled: boolean }) {
  if (!route.enabled) return "route turned off";
  if (!route.provider.enabled) return `${route.provider.name} is turned off`;
  if (route.credentials.length === 0) return `no API key on ${route.provider.name}`;
  if (readyKeyCount(route) === 0) return "no API key is ready";
  return "";
}

/** A key that cannot serve and will not recover on its own, which is the exact set
 *  the delete button acts on. It reads `credentialPoolState` rather than the raw
 *  fields so the banner's count, the rotor and the status dot can never disagree.
 *
 *  Deliberately not included: a key whose only signal is `validation_error`, which
 *  is also written for a key saved without a successful check or imported from a
 *  config bundle and still routes; and a key in cooldown, which clears itself. */
export function isUnusableKey(credential: Credential) {
  const state = credentialPoolState(credential);
  return state === "quarantined" || state === "exhausted";
}

/** unusableKeyReason is the one-line "why" shown beside each key in the confirm
 *  dialog, so the operator reads what is going before agreeing to it. */
export function unusableKeyReason(credential: Credential) {
  return credentialPoolState(credential) === "quarantined"
    ? "rejected by the provider"
    : "out of balance";
}
