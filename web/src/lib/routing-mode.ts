/** Which mode the gateway routes in, and what a new route is therefore called.
 *
 *  These two belong together because the second is entirely decided by the first:
 *  under provider-wise routing an alias carries the provider slug that keeps two
 *  providers' copies of the same model apart, and under model-wise routing it
 *  must not, because that is exactly what pools them.
 */

import { useEffect, useState } from "react";
import { api } from "../api";
import { type ModelRoute, type RoutingMode, type Settings } from "../types";

// The routing mode decides whether a new public alias carries the provider slug,
// so it is fetched once and shared by every form that proposes an alias. The
// subscriber set is what makes it shared rather than merely cached: when Settings
// saves a new mode, every mounted form re-labels itself instead of waiting for a
// remount, and concurrent mounts await one request instead of each firing their own.
let routingModeCache: RoutingMode | null = null;
let routingModeRequest: Promise<RoutingMode> | null = null;
const routingModeSubscribers = new Set<(mode: RoutingMode) => void>();

export function publishRoutingMode(mode: RoutingMode) {
  routingModeCache = mode;
  routingModeSubscribers.forEach((notifySubscriber) => notifySubscriber(mode));
}

export function useRoutingMode(): RoutingMode {
  const [mode, setMode] = useState<RoutingMode>(routingModeCache ?? "provider");
  useEffect(() => {
    routingModeSubscribers.add(setMode);
    if (routingModeCache) {
      setMode(routingModeCache);
    } else {
      routingModeRequest ??= api<Settings>("/api/admin/settings")
        .then((settings) => (settings.routing_mode === "model" ? "model" : "provider"))
        .finally(() => { routingModeRequest = null; });
      void routingModeRequest.then(publishRoutingMode).catch(() => undefined);
    }
    return () => { routingModeSubscribers.delete(setMode); };
  }, []);
  return mode;
}

export function defaultPublicAlias(providerSlug: string, upstreamModel: string, mode: RoutingMode = "provider") {
  // Model-wise routing pools every provider publishing the same alias, so a new
  // route must not carry the provider slug that would keep them separate.
  const raw = mode === "model" || upstreamModel.startsWith(`${providerSlug}/`)
    ? upstreamModel
    : `${providerSlug}/${upstreamModel}`;
  const safe = raw.replace(/[^A-Za-z0-9._:/-]+/g, "-").replace(/^-+|-+$/g, "");
  return safe.slice(0, 128);
}

// Providers often publish the same model with different casing, namespaces, or
// a dated deployment suffix (for example DeepSeek-V4-Flash and
// deepseek-v4-flash-0731). Model-wise routing should put those routes in one
// pool. Modality words stay in the key, so a vision or embedding model is never
// folded into its text-only relative merely because the brand/version matches.
export function modelFamilyKey(modelID: string) {
  const segments = modelID.trim().toLowerCase().split("/").filter(Boolean);
  let value = segments.at(-1) ?? "model";
  value = value
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/-(?:19|20)\d{2}(?:-?\d{2}){1,2}$/g, "")
    .replace(/-(?:0?[1-9]|1[0-2])(?:0?[1-9]|[12]\d|3[01])$/g, "")
    .replace(/-(?:latest|stable)$/g, "")
    .replace(/^-+|-+$/g, "");
  return value || "model";
}

export function automaticPublicAlias(
  providerSlug: string,
  upstreamModel: string,
  mode: RoutingMode,
  knownRoutes: Pick<ModelRoute, "public_alias" | "upstream_model">[] = []
) {
  if (mode === "provider") return defaultPublicAlias(providerSlug, upstreamModel, mode);
  const family = modelFamilyKey(upstreamModel);
  const existing = knownRoutes.find((route) => modelFamilyKey(route.upstream_model) === family);
  return existing?.public_alias || family;
}

export function providerSlugForUI(name: string) {
  const slug = name.toLowerCase().trim().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
  return slug.length >= 2 ? slug : "provider";
}
