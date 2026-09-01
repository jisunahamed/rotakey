import { useEffect, useId, useMemo, useRef, useState } from "react";
import { CircleGauge, CornerDownLeft, Database, FileClock, KeyRound, Route, Search, Settings as SettingsIcon } from "lucide-react";
import { api } from "./api";
import { trapTab } from "./focus";
import { useScrollLock } from "./overlays";
import { pages, pathFor } from "./routes";
import type { Provider } from "./types";

/** The answer to "there is no address for where anything is."
 *
 *  Everything the console holds has a name the operator gave it — a provider, an
 *  API key, a model alias — and until now the only way to reach one was to
 *  remember which of seven pages it lived on and then find it in a list. Here the
 *  name *is* the address: type it, press Enter, arrive.
 *
 *  Every result is a URL and nothing else. A palette that also performs actions
 *  becomes a place where a typo deletes a provider, and this one opens on a
 *  keystroke that is easy to hit by accident. */

/** One thing an operator can be looking for. */
type Hit = {
  id: string;
  /** What they typed, or as close to it as the console can offer. */
  label: string;
  /** Which one of several — the provider a key belongs to, the model behind an
   *  alias. Shown beside the label, never in place of it. */
  detail: string;
  /** Words that should find this without being shown. */
  keywords: string;
  path: string;
  icon: typeof CircleGauge;
  /** The heading it appears under, and the tie-break when two things score the
   *  same: an operator typing "openai" more often means the provider than one of
   *  its forty routes. */
  section: Section;
};

type Section = "Pages" | "Providers" | "Model routes" | "API keys" | "Settings" | "Requests";

/** Section order, and the nudge each one gets when scores tie. */
const sections: ReadonlyArray<{ name: Section; weight: number }> = [
  { name: "Requests", weight: 0 },
  { name: "Pages", weight: 40 },
  { name: "Providers", weight: 30 },
  { name: "Model routes", weight: 20 },
  { name: "API keys", weight: 10 },
  { name: "Settings", weight: 15 }
];

const sectionWeight = new Map(sections.map(({ name, weight }) => [name, weight]));

/** The settings page is one long form, so its rows are indexed by the words that
 *  are actually on them. An operator who wants to change how long request history
 *  is kept should not have to know that Rotakey calls it retention. */
const settingsHits: ReadonlyArray<Omit<Hit, "path" | "icon" | "section">> = [
  { id: "set-routing", label: "Routing mode", detail: "Provider-wise or model-wise aliases", keywords: "alias naming pool pooled rename prefix slug" },
  { id: "set-anthropic", label: "Default Anthropic resource provider", detail: "Which provider serves file uploads and batches", keywords: "files batches upload claude" },
  { id: "set-wait", label: "Capacity wait ceiling", detail: "How long a request waits for a rate-limited key", keywords: "wait queue rate limit delay max_wait_ms milliseconds" },
  { id: "set-timeout", label: "Default request timeout", detail: "How long the gateway waits for a provider", keywords: "timeout seconds slow hang new provider" },
  { id: "set-metadata", label: "Metadata retention", detail: "How long request history is kept", keywords: "retention delete history logs days purge" },
  { id: "set-bodies", label: "Captured body retention", detail: "How long captured request and reply text is kept", keywords: "retention bodies capture prompts replies days" },
  { id: "set-export", label: "Export configuration", detail: "Download providers, routes, keys and limits as a file", keywords: "backup download bundle json save" },
  { id: "set-import", label: "Import configuration", detail: "Restore a setup from a file", keywords: "restore upload bundle json" }
];

/** How well `text` answers `needle`, or -1 for not at all. The ladder is the order
 *  an operator expects: what they typed, then what starts with it, then a word
 *  inside it that starts with it, then anywhere in it. Nothing below that —
 *  fuzzy subsequence matching turns "gpt" into a match for "Get a provider" and
 *  makes the list feel arbitrary. */
function score(text: string, needle: string): number {
  const haystack = text.toLowerCase();
  if (haystack === needle) return 1000;
  if (haystack.startsWith(needle)) return 700;
  const at = haystack.indexOf(needle);
  if (at < 0) return -1;
  // A match that starts a word reads as intentional; one in the middle of a word
  // usually does not. `/` and `-` count as word breaks because model aliases are
  // built out of them.
  const before = haystack[at - 1];
  return before === " " || before === "/" || before === "-" || before === "_" || before === "." ? 500 : 250;
}

/** Every word typed has to appear somewhere, and matching the visible label beats
 *  matching a hidden keyword. Shorter labels win ties, so "gpt-4o" outranks
 *  "gpt-4o-mini-realtime-preview" for the query "gpt-4o". */
function rank(hit: Hit, terms: string[]): number {
  const hidden = `${hit.detail} ${hit.keywords}`;
  let total = sectionWeight.get(hit.section) ?? 0;
  for (const term of terms) {
    const onLabel = score(hit.label, term);
    const anywhere = Math.max(onLabel, Math.round(score(hidden, term) * 0.4));
    if (anywhere < 0) return -1;
    total += anywhere;
  }
  return total - Math.min(60, hit.label.length);
}

/** Providers, and everything hanging off them, is one request that answers three
 *  quarters of the index. It is held between opens so the second Ctrl+K of a
 *  session is instant, and refreshed on open because a key added a minute ago
 *  should be findable. */
let providerCache: Provider[] = [];

export function CommandPalette({ onGo, onClose }: { onGo: (path: string) => void; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [providers, setProviders] = useState<Provider[]>(providerCache);
  const [loading, setLoading] = useState(providerCache.length === 0);
  const [active, setActive] = useState(0);
  const listID = useId();
  const headingID = useId();
  const panel = useRef<HTMLDivElement | null>(null);
  const field = useRef<HTMLInputElement | null>(null);
  const results = useRef<HTMLDivElement | null>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  // Mounted only while open, so this is unconditional.
  useScrollLock(true);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    field.current?.focus();
    let ignore = false;
    void api<{ providers: Provider[] }>("/api/admin/providers")
      .then((result) => {
        if (ignore) return;
        providerCache = result.providers ?? [];
        setProviders(providerCache);
      })
      // A palette that cannot reach the gateway still navigates: the seven pages
      // are in the index either way, and they are the half that always works.
      .catch(() => undefined)
      .finally(() => { if (!ignore) setLoading(false); });

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        event.preventDefault();
        closeRef.current();
        return;
      }
      if (event.key === "Tab" && panel.current) trapTab(panel.current, event);
    }
    document.addEventListener("keydown", onKeyDown);
    return () => {
      ignore = true;
      document.removeEventListener("keydown", onKeyDown);
      // Only when the palette was dismissed. On a navigation the page underneath
      // has been replaced and focus belongs there, but restoring it is harmless:
      // the element it points at is gone, and `focus` on a detached node is a
      // no-op.
      previous?.focus?.();
    };
  }, []);

  const index = useMemo<Hit[]>(() => {
    const hits: Hit[] = pages.map((page) => ({
      id: `page-${page.id}`,
      label: page.label,
      detail: page.summary,
      keywords: page.aliases,
      path: pathFor(page.id),
      icon: page.icon,
      section: "Pages"
    }));
    for (const provider of providers) {
      hits.push({
        id: `provider-${provider.id}`,
        label: provider.name,
        detail: provider.base_url,
        keywords: `${provider.slug ?? ""} ${provider.api_format === "anthropic" ? "anthropic claude" : "openai"} provider upstream`,
        path: pathFor("providers", { provider: provider.id }),
        icon: Database,
        section: "Providers"
      });
      for (const model of provider.models ?? []) {
        hits.push({
          id: `model-${model.id}`,
          label: model.public_alias,
          detail: `${model.upstream_model} · ${provider.name}`,
          keywords: `${provider.name} model route alias`,
          path: pathFor("models", { model: model.id }),
          icon: Route,
          section: "Model routes"
        });
      }
      for (const credential of provider.credentials ?? []) {
        hits.push({
          id: `key-${credential.id}`,
          label: credential.label,
          detail: `Key on ${provider.name}${credential.secret_suffix ? ` · ends ${credential.secret_suffix}` : ""}`,
          keywords: `${provider.name} api key credential secret`,
          // Keys live inside the provider that owns them, so this is where the
          // operator lands. The provider opens with its key list on screen.
          path: pathFor("providers", { provider: provider.id }),
          icon: KeyRound,
          section: "API keys"
        });
      }
    }
    for (const row of settingsHits) {
      hits.push({ ...row, path: pathFor("settings"), icon: SettingsIcon, section: "Settings" });
    }
    return hits;
  }, [providers]);

  const trimmed = query.trim();
  const matches = useMemo<Hit[]>(() => {
    // Nothing typed: the seven destinations, in rail order. A palette that opens
    // empty teaches nobody what it can do.
    if (!trimmed) return index.filter((hit) => hit.section === "Pages");
    const terms = trimmed.toLowerCase().split(/\s+/).filter(Boolean);
    const scored = index
      .map((hit) => ({ hit, points: rank(hit, terms) }))
      .filter((entry) => entry.points >= 0)
      .sort((left, right) => right.points - left.points || left.hit.label.localeCompare(right.hit.label))
      .slice(0, 40)
      .map((entry) => entry.hit);
    // Whatever was typed, it can also be looked for in the request history — which
    // is the only index the console cannot hold in memory. A request id goes to
    // the front, because someone pasting `req_…` wants exactly one thing.
    const search: Hit = {
      id: "requests-search",
      label: `Search requests for “${trimmed}”`,
      detail: "Request ID or model alias, in the retained history",
      keywords: "",
      path: pathFor("requests", { q: trimmed }),
      icon: FileClock,
      section: "Requests"
    };
    return /^req_[A-Za-z0-9_-]+$/.test(trimmed) ? [search, ...scored] : [...scored, search];
  }, [index, trimmed]);

  // The highlight goes back to the top whenever the list changes underneath it,
  // so Enter always commits the row that is actually marked.
  useEffect(() => setActive(0), [trimmed]);
  const clamped = Math.min(active, Math.max(0, matches.length - 1));
  const current = matches[clamped];

  useEffect(() => {
    results.current?.querySelector<HTMLElement>("[aria-selected='true']")?.scrollIntoView({ block: "nearest" });
  }, [clamped, matches]);

  const go = (hit: Hit | undefined) => {
    if (!hit) return;
    onGo(hit.path);
    onClose();
  };

  const onFieldKeyDown = (event: React.KeyboardEvent<HTMLInputElement>) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      const step = event.key === "ArrowDown" ? 1 : -1;
      // Wrapped here, unlike a page's list: the palette is short, always visible in
      // full, and pressing up on the first result to reach the last is the one
      // place that shortcut is worth having.
      setActive((position) => (matches.length === 0 ? 0 : (position + step + matches.length) % matches.length));
      return;
    }
    if (event.key === "Enter") {
      event.preventDefault();
      go(current);
    }
  };

  let heading: Section | null = null;

  return (
    <div className="palette-layer" role="presentation">
      <button className="palette-scrim" onClick={onClose} aria-label="Close search" tabIndex={-1} />
      <div
        className="palette"
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingID}
        ref={panel}
      >
        <h2 className="sr-only" id={headingID}>Search Rotakey</h2>
        <div className="palette__field">
          <Search size={17} aria-hidden="true" />
          <input
            ref={field}
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={onFieldKeyDown}
            placeholder="Search pages, providers, model routes and API keys"
            aria-label="Search pages, providers, model routes and API keys"
            role="combobox"
            aria-expanded
            aria-controls={listID}
            aria-autocomplete="list"
            aria-activedescendant={current ? `${listID}-${current.id}` : undefined}
            autoComplete="off"
            spellCheck={false}
          />
        </div>
        <div className="palette__results" id={listID} role="listbox" aria-label="Results" ref={results}>
          {/* The request-search row is always offered, so the list is never empty
              and "no matches" has to be said rather than shown. One row left means
              nothing the console holds is called this — which is worth saying,
              because the alternative is an operator concluding their provider is
              gone when they have only misspelled it. */}
          {trimmed && matches.length === 1 && (
            <p className="palette__empty">
              Nothing here is called “{trimmed}”.
              {loading ? " Providers are still loading." : " Try part of a provider name, a model alias or a key label."}
            </p>
          )}
          {matches.map((hit, position) => {
            const Icon = hit.icon;
            const selected = position === clamped;
            // The heading is emitted by the first row of each run rather than by
            // grouping the list, so the results stay one flat sequence for the
            // arrow keys and for a screen reader reading the listbox.
            const openSection = hit.section !== heading ? hit.section : null;
            heading = hit.section;
            return (
              // The wrapper and the heading are both hidden from the listbox, so
              // the options remain its direct children as far as a screen reader
              // is concerned. The heading is a visual grouping; every option
              // already says what it is in its own detail line.
              <div key={hit.id} role="presentation">
                {openSection && <p className="palette__section" aria-hidden="true">{openSection}</p>}
                <div
                  className={`palette__hit${selected ? " is-active" : ""}`}
                  id={`${listID}-${hit.id}`}
                  role="option"
                  aria-selected={selected}
                  onClick={() => go(hit)}
                  onMouseMove={() => setActive(position)}
                >
                  <Icon size={15} aria-hidden="true" />
                  <span className="palette__label">{hit.label}</span>
                  <span className="palette__detail">{hit.detail}</span>
                  {selected && <CornerDownLeft size={14} aria-hidden="true" />}
                </div>
              </div>
            );
          })}
        </div>
        <p className="palette__hint">
          <kbd>↑</kbd><kbd>↓</kbd> to move · <kbd>Enter</kbd> to open · <kbd>Esc</kbd> to close · <kbd>?</kbd> for every shortcut
        </p>
      </div>
    </div>
  );
}
