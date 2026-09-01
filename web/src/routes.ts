import { useCallback, useEffect, useState } from "react";
import { CircleGauge, Database, FileClock, FlaskConical, KeyRound, Route, Settings } from "lucide-react";

/** Every destination the console serves. The slug in the address bar and the word
 *  in the rail are the same word — three of the seven used to disagree, so
 *  "Request logs" lived at /admin/logs and "Gateway key" at /admin/access, and an
 *  operator reading one had no way to get back to the other. */
export type Page =
  | "overview"
  | "requests"
  | "providers"
  | "models"
  | "playground"
  | "connect"
  | "settings";

/** Which band of the rail a page sits in. The groups answer "what am I here to
 *  do" — watch it, set it up, or use it — rather than listing seven flat items. */
export type PageGroup = "operate" | "configure" | "use" | "system";

export type PageMeta = {
  id: Page;
  /** The rail's word, the URL's word, and the browser tab's word. */
  label: string;
  /** One line naming what this page owns, for the rail's tooltip and the palette.
   *  It states the object, not the mechanism. */
  summary: string;
  group: PageGroup;
  /** The rail's picture, reused by the search palette so a result is recognised as
   *  the row the operator already knows. Typed off a value because lucide-react
   *  publishes no name for it. */
  icon: typeof CircleGauge;
  /** Words an operator might search for that are not in the label or the summary —
   *  what they called this screen before, and what it holds. Only the palette
   *  reads these. */
  aliases: string;
};

export const pages: readonly PageMeta[] = [
  { id: "overview", label: "Overview", summary: "What is ready now, and what it cost", group: "operate", icon: CircleGauge, aliases: "dashboard home status health spend cost usage" },
  { id: "requests", label: "Requests", summary: "Every request and why it went the way it did", group: "operate", icon: FileClock, aliases: "logs history traffic errors latency" },
  { id: "providers", label: "Providers", summary: "Upstream accounts and their API keys", group: "configure", icon: Database, aliases: "upstream accounts credentials keys base url" },
  { id: "models", label: "Models", summary: "The model names your callers ask for", group: "configure", icon: Route, aliases: "routes aliases limits capabilities" },
  { id: "playground", label: "Playground", summary: "Send a prompt through the gateway", group: "use", icon: FlaskConical, aliases: "chat test prompt try" },
  { id: "connect", label: "Connect", summary: "Base URLs, SDK snippets and the gateway key", group: "use", icon: KeyRound, aliases: "access gateway key sdk snippet curl python endpoint" },
  { id: "settings", label: "Settings", summary: "Gateway-wide policy, backup and restore", group: "system", icon: Settings, aliases: "config options preferences export import backup" }
];

/** The rail's bands, in the order they are shown. Seven flat rows gave an operator
 *  no way to guess where a thing lived; three verbs do, because they are the three
 *  reasons anyone opens this console. Settings carries no caption — it is one row,
 *  and a caption over one row is decoration. It gets a hairline instead. */
export const pageGroups: readonly { group: PageGroup; caption: string | null }[] = [
  { group: "operate", caption: "Operate" },
  { group: "configure", caption: "Configure" },
  { group: "use", caption: "Use" },
  { group: "system", caption: null }
];

export function pagesIn(group: PageGroup): readonly PageMeta[] {
  return pages.filter((page) => page.group === group);
}

const pageIDs = new Set<string>(pages.map((page) => page.id));

/** URLs that already shipped. A bookmark, a link in a chat, or a browser history
 *  entry is not something the console gets to rename out from under the operator,
 *  so both old slugs still resolve — and then quietly correct themselves to the
 *  canonical path so the address bar and the rail agree from then on. */
const legacyPaths: Record<string, Page> = {
  logs: "requests",
  access: "connect"
};

export function isPage(value: string): value is Page {
  return pageIDs.has(value);
}

export function pageMeta(page: Page): PageMeta {
  // The union and the list are declared together, so this cannot miss.
  return pages.find((item) => item.id === page) as PageMeta;
}

export type Route = {
  page: Page;
  /** True when the path named nothing this console serves. /admin/typo used to
   *  render the Overview, which told the operator their link had worked. */
  notFound: boolean;
  /** Where the address bar should be corrected to, or null when it is already
   *  right. Set for /admin, for the two old slugs, and for a trailing segment. */
  canonicalPath: string | null;
  /** The development-only page that draws every primitive. It is not a `Page`
   *  because it is not a destination: nothing links to it, the rail does not list
   *  it and the search palette does not index it. In a production build the path
   *  is a typo like any other. */
  kitchenSink: boolean;
};

/** The one path outside the seven. Kept next to `readRoute` so the string is
 *  written once and the guard below cannot drift from the address it answers. */
export const kitchenSinkPath = "/admin/__ui";

export function readRoute(): Route {
  const segments = location.pathname.replace(/^\/admin\/?/, "").split("/").filter(Boolean);
  const first = segments[0] ?? "";
  if (import.meta.env.DEV && first === "__ui") {
    return { page: "overview", notFound: false, canonicalPath: null, kitchenSink: true };
  }
  if (first === "") {
    return { page: "overview", notFound: false, canonicalPath: `/admin/overview${location.search}`, kitchenSink: false };
  }
  if (isPage(first)) {
    // A page owns one path segment. Anything deeper was never a route, so it is
    // trimmed rather than treated as a section that does not exist.
    return { page: first, notFound: false, canonicalPath: segments.length > 1 ? `/admin/${first}${location.search}` : null, kitchenSink: false };
  }
  const legacy = legacyPaths[first];
  if (legacy) {
    return { page: legacy, notFound: false, canonicalPath: `/admin/${legacy}${location.search}`, kitchenSink: false };
  }
  return { page: "overview", notFound: true, canonicalPath: null, kitchenSink: false };
}

export function pathFor(page: Page, query?: Record<string, string>) {
  const search = query && Object.keys(query).length > 0 ? `?${new URLSearchParams(query)}` : "";
  return `/admin/${page}${search}`;
}

/** Every tab read "Rotakey" — nothing anywhere set the title — so a window with
 *  four of them open named none of them. The detail is what the page has open:
 *  a provider, a route, a request id. */
export function documentTitle(page: Page, detail?: string) {
  const label = pageMeta(page).label;
  return detail ? `${detail} · ${label} · Rotakey` : `${label} · Rotakey`;
}

let currentTitleDetail = "";
const titleDetailSubscribers = new Set<(detail: string) => void>();

/** What the open page currently has selected, for the browser tab. Published
 *  rather than passed down: the component that knows the provider's name is four
 *  levels below the one that owns the title, and the routing mode already travels
 *  the console this way. */
function publishTitleDetail(detail: string) {
  currentTitleDetail = detail;
  titleDetailSubscribers.forEach((notifySubscriber) => notifySubscriber(detail));
}

/** Called by a page with whatever it has open, and with nothing when it closes. */
export function useTitleDetail(detail: string) {
  useEffect(() => {
    publishTitleDetail(detail);
    return () => publishTitleDetail("");
  }, [detail]);
}

export function useCurrentTitleDetail() {
  const [detail, setDetail] = useState(currentTitleDetail);
  useEffect(() => {
    titleDetailSubscribers.add(setDetail);
    setDetail(currentTitleDetail);
    return () => { titleDetailSubscribers.delete(setDetail); };
  }, []);
  return detail;
}

/** Fired after the console writes the address bar itself. `popstate` covers Back
 *  and Forward and nothing covers pushState, so a page that stays mounted while a
 *  link inside it changes the query would otherwise never hear about the change. */
const URL_CHANGED = "rotakey:url";

/** Writes the address bar and tells the rest of the console. `push` is for
 *  anything the operator chose, so Back undoes it; `replace` is for a correction
 *  the console made on their behalf, which is not a place they can return to.
 *  Every in-page write used to be a replace, so Back inside a page did nothing
 *  and five provider selections in a row left one history entry. */
export function writeURL(url: string | URL, mode: "push" | "replace") {
  const next = String(url);
  if (next === location.href || next === `${location.pathname}${location.search}${location.hash}`) return;
  if (mode === "push") history.pushState(history.state, "", next);
  else history.replaceState(history.state, "", next);
  window.dispatchEvent(new Event(URL_CHANGED));
}

/** Runs the handler whenever the address bar changes for any reason. */
export function useURLChange(handler: () => void) {
  useEffect(() => {
    window.addEventListener("popstate", handler);
    window.addEventListener(URL_CHANGED, handler);
    return () => {
      window.removeEventListener("popstate", handler);
      window.removeEventListener(URL_CHANGED, handler);
    };
  }, [handler]);
}

/** One search parameter, held in the address bar rather than beside it. Four
 *  copies of this pattern were written out across App.tsx with three different
 *  rules about when they wrote, and none of them read the parameter back — so
 *  Back moved the URL and left the page showing the previous selection.
 *
 *  An empty string means the parameter is absent, which is how "nothing selected"
 *  is spelled in a URL. */
export function useURLState(key: string, mode: "push" | "replace" = "push") {
  const read = useCallback(() => new URLSearchParams(location.search).get(key) ?? "", [key]);
  const [value, setValue] = useState(read);
  const sync = useCallback(() => setValue(read()), [read]);
  useURLChange(sync);
  const commit = useCallback(
    // The updater form is here because callers use it: a list that has just
    // reloaded keeps the current selection if it survived and falls back to the
    // first row if it did not. The current value is read back out of the address
    // bar rather than out of a closure, so it cannot go stale.
    //
    // `at` overrides the hook's mode for one call. The same parameter is both a
    // choice and a correction depending on who set it — the operator clicking a
    // row is a push, the loader replacing a selection that no longer exists is a
    // replace, and Back must not walk them through selections they never made.
    (next: string | ((current: string) => string), at: "push" | "replace" = mode) => {
      const resolved = typeof next === "function" ? next(read()) : next;
      setValue(resolved);
      const url = new URL(location.href);
      if (resolved) url.searchParams.set(key, resolved);
      else url.searchParams.delete(key);
      writeURL(url, at);
    },
    [key, mode, read]
  );
  return [value, commit] as const;
}
