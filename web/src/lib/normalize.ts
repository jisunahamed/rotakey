/** What the console does to a payload before any component indexes into it.
 *
 *  Go serialises an empty map and an empty slice as `null`, and the admin API
 *  omits a field it has nothing to say about. Every widget on the overview reads
 *  four levels deep without checking, so one absent array used to take the page
 *  to the error boundary. Defaulting happens once, here, at the edge — not at the
 *  forty places that read the result.
 */

import { emptyCreditTotals, emptyPolicy, type DiscoveredModel, type Overview, type Provider } from "../types";

/** Absent numbers arrive from a partially-populated payload; `Intl` turns them
 *  into the literal string "NaN", which is worse than a zero in a readout. */
export function safeNumber(value: number | null | undefined) {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function normalizeProviders(providers: Provider[] | null | undefined): Provider[] {
  return (providers ?? []).map((provider) => ({
    ...provider,
    api_format: provider.api_format ?? "openai",
    anthropic_version: provider.anthropic_version ?? "2023-06-01",
    extra_headers: provider.extra_headers ?? {},
    // An absent balance is "not tracked", which is deliberately not the same as
    // zero: zero is what stops a key receiving traffic. Only the spend figures are
    // defaulted to a number.
    default_key_balance_usd: provider.default_key_balance_usd ?? null,
    balance_spent_usd: safeNumber(provider.balance_spent_usd),
    models: (provider.models ?? []).map((model) => ({ ...model, supports_messages: model.supports_messages ?? true, strip_parameters: model.strip_parameters ?? [], capability_status: model.capability_status ?? "unverified", capability_profile: model.capability_profile ?? {}, input_cost_per_million_usd: model.input_cost_per_million_usd ?? 0, output_cost_per_million_usd: model.output_cost_per_million_usd ?? 0, request_cost_usd: model.request_cost_usd })),
    credentials: (provider.credentials ?? []).map((credential) => ({
      ...credential,
      validation_error: credential.validation_error ?? "",
      balance_usd: credential.balance_usd ?? null,
      balance_spent_usd: safeNumber(credential.balance_spent_usd),
      // Go serialises an empty map as null, so both of these arrive as null from
      // a key that has never had a limit set. Every render path indexes them
      // directly, and without an error boundary a null map takes the console
      // to a blank page rather than to a missing number.
      limits: credential.limits ?? emptyPolicy(),
      model_limits: credential.model_limits ?? {},
    })),
  }));
}

/** The overview is the one payload every widget on the landing page indexes into
 *  without checking, so a missing array or a missing credit block used to take
 *  the whole console down. Defaulted here once instead of at forty call sites. */
export function normalizeOverview(overview: Overview): Overview {
  const summary = overview.summary ?? ({} as Overview["summary"]);
  return {
    ...overview,
    summary: {
      ...summary,
      providers_total: safeNumber(summary.providers_total),
      providers_ready: safeNumber(summary.providers_ready),
      routes_total: safeNumber(summary.routes_total),
      routes_ready: safeNumber(summary.routes_ready),
      keys_total: safeNumber(summary.keys_total),
      keys_ready: safeNumber(summary.keys_ready),
      keys_warning: safeNumber(summary.keys_warning),
      requests: safeNumber(summary.requests),
      tokens: safeNumber(summary.tokens),
      estimated_cost_usd: safeNumber(summary.estimated_cost_usd),
      errors: safeNumber(summary.errors),
      error_rate: safeNumber(summary.error_rate),
      latency_p50_ms: safeNumber(summary.latency_p50_ms),
      latency_p95_ms: safeNumber(summary.latency_p95_ms),
      max_wait_ms: safeNumber(summary.max_wait_ms),
      gateway_key_ready: summary.gateway_key_ready ?? false,
      credit: summary.credit ?? emptyCreditTotals()
    },
    series: overview.series ?? [],
    providers: (overview.providers ?? []).map((provider) => ({
      ...provider,
      models_total: safeNumber(provider.models_total),
      models_ready: safeNumber(provider.models_ready),
      keys_total: safeNumber(provider.keys_total),
      keys_ready: safeNumber(provider.keys_ready),
      keys_warning: safeNumber(provider.keys_warning),
      validation_warnings: safeNumber(provider.validation_warnings),
      capacity: provider.capacity ?? {},
      credit: provider.credit ?? emptyCreditTotals()
    })),
    // The route rows render these figures straight into text, so a payload that
    // omits one produced "NaN% err" and "undefined ms" in the table rather than a
    // zero. Only the arrays used to be defaulted here.
    routes: (overview.routes ?? []).map((route) => ({
      ...route,
      requests: safeNumber(route.requests),
      errors: safeNumber(route.errors),
      tokens: safeNumber(route.tokens),
      estimated_cost_usd: safeNumber(route.estimated_cost_usd),
      error_rate: safeNumber(route.error_rate),
      latency_p95_ms: safeNumber(route.latency_p95_ms),
      healthy_credentials: safeNumber(route.healthy_credentials),
      unavailable_credentials: safeNumber(route.unavailable_credentials),
      total_credentials: safeNumber(route.total_credentials),
      default_max_output_tokens: safeNumber(route.default_max_output_tokens),
      strip_parameters: route.strip_parameters ?? [],
      segments: route.segments ?? []
    })),
    alerts: overview.alerts ?? [],
    recent_failures: overview.recent_failures ?? []
  };
}

// poolSizeByAlias counts how many provider routes publish each public alias, so
// model-wise mode can show that one name is backed by several providers.
export function poolSizeByAlias(providers: Provider[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const provider of providers) {
    for (const model of provider.models) {
      counts[model.public_alias] = (counts[model.public_alias] ?? 0) + 1;
    }
  }
  return counts;
}

export function mergeModelCatalogs(catalogs: DiscoveredModel[][]): DiscoveredModel[] {
  const byID = new Map<string, DiscoveredModel>();
  for (const catalog of catalogs) {
    for (const model of catalog ?? []) {
      if (!byID.has(model.id)) byID.set(model.id, model);
    }
  }
  return [...byID.values()].sort((left, right) => left.id.localeCompare(right.id));
}
