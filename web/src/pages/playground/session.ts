/** A playground conversation: what it holds, and where it lives between visits.
 *
 *  Runs used to be component state. Opening the request log to read the evidence
 *  for a reply destroyed the reply, and the list was capped at twenty with
 *  nothing on screen saying so. A conversation is written to `localStorage`
 *  after every turn instead, so leaving the page and coming back is not a way to
 *  lose work.
 *
 *  Two caps here are the gateway's, not this file's. `maxTurns` and
 *  `maxContent` are `playgroundMaxTurns` and `playgroundMaxContent` in
 *  `internal/app/playground.go`; the composer stops at the same numbers so that
 *  the operator is told before they write a message the send would reject. If
 *  those constants move, these move with them.
 */

import type { PlaygroundProtocol } from "../../lib/stream";

/** How the request should be shaped on the wire. `auto` leaves the choice to the
 *  gateway, which picks whatever the route can actually serve — the right answer
 *  unless you are testing one path in particular. */
export type RunProtocol = "auto" | PlaygroundProtocol;

export type RunSettings = {
  system: string;
  protocol: RunProtocol;
  maxTokens: number;
  temperature: number;
  stream: boolean;
};

/** What the console can honestly say about a reply it received.
 *
 *  Everything above `servedBy` comes off the response itself — headers and the
 *  stream — and is therefore always true. Everything from `servedBy` down comes
 *  from the request's log row, which is fetched afterwards and may not be
 *  readable; `logFound` says which of the two happened, so the strip can be
 *  quiet about what it does not know instead of printing a blank.
 *
 *  Token counts are nullable on purpose. On a streamed request the log row holds
 *  the tokenizer's *estimate* for input and zero for output in eight of the nine
 *  protocol pairs, so the counts are taken off the wire or not taken at all. */
export type Evidence = {
  requestID: string;
  protocol: PlaygroundProtocol | "";
  elapsedMS: number;
  inputTokens: number | null;
  outputTokens: number | null;
  streamed: boolean;
  /** Parameters the gateway dropped before sending, because the provider had
   *  rejected them. */
  removed: string[];
  /** Parameters it renamed, as `[from, to]`. */
  replaced: Array<[string, string]>;
  /** The endpoint it moved the request to when the provider refused the first
   *  one — `"responses"`, or empty when nothing was switched. */
  switched: string;
  servedBy: string;
  providerName: string;
  upstreamProtocol: string;
  statusCode: number;
  logFound: boolean;
};

export type Turn = {
  id: string;
  role: "user" | "assistant";
  text: string;
  /** Assistant turns only: the alias the console *sent*. Not the model id the
   *  reply echoed back — the two OpenAI-shaped pass-through paths return the
   *  provider's own name for the model, which is not the alias the operator
   *  typed and not the one they would search the logs for. */
  model?: string;
  error?: string;
  errorCode?: string;
  /** Set when the operator pressed Stop. The text above it is as far as the
   *  reply got, which is worth keeping and worth labelling. */
  stopped?: boolean;
  evidence?: Evidence;
};

export type Conversation = {
  id: string;
  /** The route this chat sends to. A chat is tied to one alias: changing it
   *  mid-conversation would make the transcript a record of two different
   *  models with nothing on screen saying where the change happened. */
  model: string;
  settings: RunSettings;
  turns: Turn[];
  updatedAt: number;
};

/** The gateway refuses more than this many turns in one request. */
export const maxTurns = 200;
/** And more than this many characters across all of them. */
export const maxContent = 200_000;
/** Chats kept in this browser. Older ones are dropped, and the page says so. */
export const maxConversations = 20;

const storeKey = "rotakey.playground.v1";

/** Ids only have to be unique within one browser's storage, and they end up in a
 *  URL, so they are short and readable rather than random. */
let minted = 0;
export function mintID(prefix: string) {
  minted += 1;
  return `${prefix}${Date.now().toString(36)}${minted.toString(36)}`;
}

export function defaultSettings(maxOutputTokens?: number): RunSettings {
  return {
    system: "",
    protocol: "auto",
    // The route's own default output cap when it has one, and the gateway's
    // otherwise (playgroundDefaultCap).
    maxTokens: maxOutputTokens && maxOutputTokens > 0 ? maxOutputTokens : 1024,
    temperature: 1,
    stream: true
  };
}

export function newConversation(model: string, settings: RunSettings): Conversation {
  return { id: mintID("c"), model, settings, turns: [], updatedAt: Date.now() };
}

/** What the chat list shows. Derived rather than stored: a title written once at
 *  creation goes stale the moment the first message is edited. */
export function conversationTitle(chat: Conversation) {
  const first = chat.turns.find((turn) => turn.role === "user");
  const text = (first?.text ?? "").trim().replace(/\s+/g, " ");
  if (text === "") return "Empty chat";
  return text.length > 64 ? `${text.slice(0, 63)}…` : text;
}

export function contentLength(turns: Turn[]) {
  return turns.reduce((total, turn) => total + turn.text.length, 0);
}

/* ------------------------------------------------------------------ storage */

/** Anything in storage was written by an older build, or by hand, or by another
 *  tab that crashed halfway through. It is read one field at a time and anything
 *  that does not survive is dropped, because a playground that will not open is
 *  worse than a playground that lost a chat. */
function reviveTurn(value: unknown): Turn | null {
  const turn = value as Partial<Turn> | null;
  if (!turn || typeof turn !== "object") return null;
  if (turn.role !== "user" && turn.role !== "assistant") return null;
  if (typeof turn.text !== "string") return null;
  return {
    id: typeof turn.id === "string" && turn.id !== "" ? turn.id : mintID("t"),
    role: turn.role,
    text: turn.text,
    model: typeof turn.model === "string" ? turn.model : undefined,
    error: typeof turn.error === "string" ? turn.error : undefined,
    errorCode: typeof turn.errorCode === "string" ? turn.errorCode : undefined,
    stopped: turn.stopped === true ? true : undefined,
    evidence: turn.evidence && typeof turn.evidence === "object" ? (turn.evidence as Evidence) : undefined
  };
}

function reviveConversation(value: unknown): Conversation | null {
  const chat = value as Partial<Conversation> | null;
  if (!chat || typeof chat !== "object") return null;
  if (typeof chat.id !== "string" || chat.id === "") return null;
  if (typeof chat.model !== "string") return null;
  const stored = chat.settings as Partial<RunSettings> | undefined;
  const base = defaultSettings();
  return {
    id: chat.id,
    model: chat.model,
    settings: {
      system: typeof stored?.system === "string" ? stored.system : base.system,
      protocol:
        stored?.protocol === "chat" || stored?.protocol === "responses" || stored?.protocol === "messages"
          ? stored.protocol
          : "auto",
      maxTokens: Number.isFinite(stored?.maxTokens) ? Number(stored?.maxTokens) : base.maxTokens,
      temperature: Number.isFinite(stored?.temperature) ? Number(stored?.temperature) : base.temperature,
      stream: stored?.stream !== false
    },
    turns: Array.isArray(chat.turns)
      ? chat.turns.map(reviveTurn).filter((turn): turn is Turn => turn !== null)
      : [],
    updatedAt: Number.isFinite(chat.updatedAt) ? Number(chat.updatedAt) : Date.now()
  };
}

export function loadConversations(): Conversation[] {
  let raw: string | null = null;
  try {
    raw = window.localStorage.getItem(storeKey);
  } catch {
    // Storage can be disabled outright. The playground still works; it just
    // forgets, which is what the caller is told when saving fails too.
    return [];
  }
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw) as { conversations?: unknown };
    const list = Array.isArray(parsed?.conversations) ? parsed.conversations : [];
    return list.map(reviveConversation).filter((chat): chat is Conversation => chat !== null);
  } catch {
    return [];
  }
}

/** Writes what fits and reports what did not.
 *
 *  A quota error is not a failure to handle silently: the operator's last reply
 *  is in that list. Older chats are dropped one at a time until the write goes
 *  through, and the sentence explaining it is returned rather than thrown, so
 *  the page can print it where the chats used to be. */
export function saveConversations(list: Conversation[]): { stored: Conversation[]; note: string } {
  let kept = list.slice(0, maxConversations);
  for (;;) {
    try {
      window.localStorage.setItem(storeKey, JSON.stringify({ version: 1, conversations: kept }));
      if (kept.length >= list.length) return { stored: kept, note: "" };
      if (kept.length >= maxConversations) {
        return { stored: kept, note: `This browser keeps your ${maxConversations} most recent chats. Older ones are gone.` };
      }
      return {
        stored: kept,
        note: `This browser is out of storage, so only your ${kept.length} most recent chats are saved.`
      };
    } catch {
      if (kept.length > 1) {
        kept = kept.slice(0, kept.length - 1);
        continue;
      }
      try {
        window.localStorage.removeItem(storeKey);
      } catch {
        // Nothing further to try. The chat below survives in memory only.
      }
      return {
        stored: list.slice(0, 1),
        note: "This browser will not store anything, so this chat lasts until you close the tab."
      };
    }
  }
}
