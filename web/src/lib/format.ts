/** Numbers and times, turned into the strings the console prints.
 *
 *  Every function here has the same two obligations. It never returns "NaN",
 *  "Invalid Date" or "undefined" — a malformed field renders as an em dash and
 *  the page stays up — and it returns a string that is read the same way in a
 *  sentence and in a column, because the console sets figures in tabular
 *  figures and a row that formats its own number differently from the row above
 *  it is the thing the type scale exists to prevent.
 */

import { APIError } from "../api";
import { safeNumber } from "./normalize";

export function formatNumber(value: number) {
  const safe = safeNumber(value);
  return new Intl.NumberFormat("en", { notation: safe > 9999 ? "compact" : "standard", maximumFractionDigits: 1 }).format(safe);
}

export function formatCompact(value: number) {
  return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(safeNumber(value));
}

export function formatUSD(value: number) {
  const safe = safeNumber(value);
  return new Intl.NumberFormat("en-US", { style: "currency", currency: "USD", maximumFractionDigits: safe < 1 ? 4 : 2 }).format(safe);
}

export function formatLatency(value: number) {
  const safe = safeNumber(value);
  return safe >= 1000 ? `${(safe / 1000).toFixed(safe >= 10_000 ? 0 : 1)}s` : `${Math.round(safe)}ms`;
}

export function formatChartDate(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  // Intl throws a RangeError on an invalid date rather than returning a string,
  // and this runs inside the overview's render path, so one malformed timestamp
  // in the series would take the whole page down.
  if (Number.isNaN(parsed.getTime())) return "—";
  return new Intl.DateTimeFormat("en", { month: "short", day: "numeric" }).format(parsed);
}

/** The wall-clock time a request landed. Every row in the log list renders one,
 *  so an unparseable stamp has to degrade to a dash rather than to "Invalid
 *  Date" in the row and in its accessible name. */
export function formatClockTime(value?: string) {
  if (!value) return "—";
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "—";
  return parsed.toLocaleTimeString();
}

/** A span the operator chose — a retention window, a wait ceiling. Rounded up,
 *  because "0s" for a limit that is really 900ms would read as "no limit". */
export function formatDuration(milliseconds: number) {
  if (milliseconds < 60_000) return `${Math.max(1, Math.ceil(milliseconds / 1_000))}s`;
  if (milliseconds < 3_600_000) return `${Math.ceil(milliseconds / 60_000)}m`;
  return `${Math.ceil(milliseconds / 3_600_000)}h`;
}

/** A span the console measured, which is a different job from `formatDuration`:
 *  a request that took 1.2s and one that took 1.9s are not both "2s" to someone
 *  deciding whether a provider is slow. */
export function formatElapsed(milliseconds: number) {
  return `${(milliseconds / 1000).toFixed(1)}s`;
}

/** When a rate limit frees up. `now` is a parameter rather than a call so the
 *  ticking readout can re-render from one clock instead of each row reading its
 *  own, which is also what makes this testable without freezing time. */
export function formatResetTime(value: string, now = Date.now()) {
  const reset = new Date(value).getTime();
  if (Number.isNaN(reset)) return "unknown";
  const milliseconds = reset - now;
  if (milliseconds <= 0) return "now";
  if (milliseconds < 60_000) return `in ${Math.ceil(milliseconds / 1000)}s`;
  if (milliseconds < 3_600_000) return `in ${Math.ceil(milliseconds / 60_000)}m`;
  return `at ${new Date(reset).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`;
}

export function formatRelativeTime(value: string) {
  const stamp = new Date(value).getTime();
  // Every comparison against NaN is false, so an unparseable timestamp used to
  // fall through this ladder and render the literal text "NaNd ago".
  if (Number.isNaN(stamp)) return "at an unknown time";
  const seconds = Math.max(0, Math.floor((Date.now() - stamp) / 1000));
  if (seconds < 10) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

/** Enough of a secret to recognise it, and not enough to use it. The gateway
 *  never sends a key back, so this only ever runs on one the operator has just
 *  typed or pasted into the form in front of them. */
export function maskedSecret(secret: string) {
  return secret.length <= 4 ? "••••" : `•••• ${secret.slice(-4)}`;
}

export function errorMessage(caught: unknown) {
  if (caught instanceof APIError || caught instanceof Error) return caught.message;
  // Everything the console throws is an Error, so this fallback only fires when a
  // rejection carries no message at all — a dropped connection, or a request the
  // browser cancelled. There is nothing to report except the one thing that helps:
  // the request never landed, so trying it again is safe.
  return "The gateway did not answer. Try again.";
}
