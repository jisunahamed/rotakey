/** The playground.
 *
 *  Two panes: the chats on the left, the conversation on the right. Not three.
 *  The third pane used to hold a full route editor — a second, divergent copy of
 *  the one on Models, whose identically-labelled buttons ran different code —
 *  and it needed about a thousand pixels of width to exist at all. Models owns
 *  the route now; this page sends messages to it and says what happened.
 *
 *  The frame is built here rather than out of `Workbench` for one reason:
 *  `Workbench` drawers its *inspector* below 900px, because on every other page
 *  the list is the page. Here the conversation is the page, so the list is what
 *  becomes a drawer. `useDrawerOverlay` still owns the trap, the Escape key and
 *  the scrim, so a narrow screen behaves like every other narrow screen.
 */

import { useCallback, useEffect, useMemo, useRef, useState, type Ref } from "react";
import { ChevronDown, MessagesSquare, Plus, SlidersHorizontal, Trash2 } from "lucide-react";
import { api } from "../../api";
import { clipboardBlocked, copyText } from "../../clipboard";
import { endpointLabel } from "../../lib/copy";
import { countOf, errorMessage, formatNumber } from "../../lib/format";
import { useMediaQuery } from "../../lib/hooks";
import { normalizeProviders } from "../../lib/normalize";
import { readyKeyCount, routeBlockReason } from "../../lib/keys";
import { useTitleDetail, useURLState, type Page } from "../../routes";
import type { Credential, ModelRoute, Provider } from "../../types";
import {
  Button,
  Dot,
  Empty,
  Field,
  Label,
  Menu,
  MenuItem,
  Notice,
  SearchInput,
  SectionHeader,
  Sheet,
  Toggle,
  useConfirm,
  useDrawerOverlay,
  useListKeys,
  type ConsoleState
} from "../../ui";
import { Composer } from "./Composer";
import { Transcript } from "./Transcript";
import { runTurn } from "./run";
import {
  contentLength,
  conversationTitle,
  defaultSettings,
  loadConversations,
  maxContent,
  maxConversations,
  maxTurns,
  mintID,
  newConversation,
  saveConversations,
  type Conversation,
  type RunProtocol,
  type RunSettings,
  type Turn
} from "./session";

type Route = ModelRoute & { provider: Provider; credentials: Credential[] };

/** What the console can say about a route without sending anything to it. The
 *  playground never probes: probing is Models' job, and a page that quietly
 *  fired requests at every route it listed would be spending the operator's
 *  money to draw a dot. */
function routeState(route: Route): ConsoleState {
  if (!route.enabled || !route.provider.enabled) return "disabled";
  if (route.capability_status === "failed") return "failed";
  if (readyKeyCount(route) === 0) return "unavailable";
  if (route.capability_status === "probe_verified" || route.capability_status === "catalog_verified") {
    return "healthy";
  }
  return "unverified";
}

/** A number the operator types. `Number(event.target.value)` turns a cleared
 *  field into 0 and commits it, which is how a reply limit becomes zero while
 *  the box looks empty. The text is held as text and only a value inside the
 *  stated bounds is committed. */
function NumberField({
  label,
  hint,
  value,
  min,
  max,
  step,
  onCommit
}: {
  label: string;
  hint: string;
  value: number;
  min: number;
  max: number;
  step: number;
  onCommit: (value: number) => void;
}) {
  const [text, setText] = useState(String(value));
  useEffect(() => setText(String(value)), [value]);
  const parsed = Number(text);
  const bad = text.trim() === "" || !Number.isFinite(parsed) || parsed < min || parsed > max;
  return (
    <Field label={label} hint={hint} error={bad ? `Enter a number from ${min} to ${max}.` : ""}>
      {(control) => (
        <input
          {...control}
          type="number"
          value={text}
          min={min}
          max={max}
          step={step}
          onChange={(event) => {
            setText(event.target.value);
            const next = Number(event.target.value);
            if (event.target.value.trim() !== "" && Number.isFinite(next) && next >= min && next <= max) {
              onCommit(next);
            }
          }}
        />
      )}
    </Field>
  );
}

export function PlaygroundPage({
  navigate,
  notify
}: {
  navigate: (page: Page, query?: Record<string, string>) => void;
  notify: (message: string, tone?: "success" | "danger") => void;
}) {
  const ask = useConfirm();
  const listKeys = useListKeys();
  const compact = useMediaQuery("(max-width: 900px)");

  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState("");
  const [chats, setChats] = useState<Conversation[]>(() => loadConversations());
  const [sessionID, setSessionID] = useURLState("session");
  // A bookmark from an earlier build points at a route id. It is honoured once,
  // on arrival, and then replaced by the chat it opened.
  const [modelParam, setModelParam] = useURLState("model");
  const [draft, setDraft] = useState("");
  const [storageNote, setStorageNote] = useState("");
  const [status, setStatus] = useState("");
  const [streamingID, setStreamingID] = useState("");
  const [pickerOpen, setPickerOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [listOpen, setListOpen] = useState(false);
  const [pickerQuery, setPickerQuery] = useState("");

  const running = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const scroller = useRef<HTMLDivElement | null>(null);
  const chatsRef = useRef(chats);
  chatsRef.current = chats;

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      running.current?.abort();
    };
  }, []);

  /** Writes the chats to this browser and reports whatever would not fit. Called
   *  at the points where work would be lost — never once per streamed token. */
  const commit = useCallback((next: Conversation[]) => {
    const { stored, note } = saveConversations(next);
    setChats(stored);
    setStorageNote(note);
  }, []);

  const updateChat = useCallback((id: string, change: (chat: Conversation) => Conversation) => {
    setChats((current) => current.map((chat) => (chat.id === id ? change(chat) : chat)));
  }, []);

  useEffect(() => {
    let ignore = false;
    void (async () => {
      try {
        const result = await api<{ providers: Provider[] }>("/api/admin/providers");
        if (ignore) return;
        setProviders(normalizeProviders(result.providers));
        setLoadError("");
      } catch (caught) {
        if (!ignore) setLoadError(errorMessage(caught));
      } finally {
        if (!ignore) setLoading(false);
      }
    })();
    return () => {
      ignore = true;
    };
  }, []);

  const routes: Route[] = useMemo(
    () =>
      providers.flatMap((provider) =>
        provider.models.map((model) => ({ ...model, provider, credentials: provider.credentials }))
      ),
    [providers]
  );

  // One chat always exists once there is something to send to, so the page opens
  // on a composer rather than on an instruction to press New chat.
  useEffect(() => {
    if (loading || routes.length === 0) return;
    setChats((current) => {
      if (current.length > 0) return current;
      const first = routes[0];
      return [newConversation(first.public_alias, defaultSettings(first.default_max_output_tokens))];
    });
  }, [loading, routes]);

  const chat = chats.find((item) => item.id === sessionID) ?? chats[0];
  const route = chat ? routes.find((item) => item.public_alias === chat.model) : undefined;
  useTitleDetail(chat?.model ?? "");

  useEffect(() => {
    if (chat && sessionID !== chat.id) setSessionID(chat.id, "replace");
  }, [chat, sessionID, setSessionID]);

  const startChat = useCallback(
    (target: Route) => {
      const fresh = newConversation(target.public_alias, defaultSettings(target.default_max_output_tokens));
      // An empty chat holds nothing, so opening a new one clears the blanks
      // rather than stacking them up against the twenty-chat ceiling.
      commit([fresh, ...chatsRef.current.filter((item) => item.turns.length > 0)]);
      setSessionID(fresh.id);
      setDraft("");
      setListOpen(false);
      setPickerOpen(false);
    },
    [commit, setSessionID]
  );

  useEffect(() => {
    if (modelParam === "" || loading) return;
    const wanted = routes.find((item) => item.id === modelParam || item.public_alias === modelParam);
    setModelParam("", "replace");
    if (wanted) startChat(wanted);
  }, [modelParam, loading, routes, setModelParam, startChat]);

  /** Sends `history` and writes the reply into the chat as it arrives. `history`
   *  ends with the user turn being answered; everything after it in the chat is
   *  replaced, which is what makes "ask again" and "edit and send again" the
   *  same operation with a different starting point. */
  const runFrom = useCallback(
    async (target: Conversation, history: Turn[]) => {
      const reply: Turn = { id: mintID("t"), role: "assistant", text: "", model: target.model };
      const controller = new AbortController();
      running.current = controller;
      setStreamingID(reply.id);
      setStatus("Sent. Waiting for the reply.");
      updateChat(target.id, (current) => ({ ...current, turns: [...history, reply], updatedAt: Date.now() }));

      const outcome = await runTurn({
        chat: target,
        turns: history,
        signal: controller.signal,
        onText: (text) =>
          updateChat(target.id, (current) => ({
            ...current,
            turns: current.turns.map((turn) => (turn.id === reply.id ? { ...turn, text } : turn))
          }))
      });

      if (!mounted.current) return;
      const finished: Turn = {
        ...reply,
        text: outcome.text,
        error: outcome.error || undefined,
        errorCode: outcome.errorCode || undefined,
        stopped: outcome.stopped || undefined,
        evidence: outcome.evidence
      };
      commit(
        chatsRef.current.map((item) =>
          item.id === target.id ? { ...item, turns: [...history, finished], updatedAt: Date.now() } : item
        )
      );
      setStreamingID("");
      running.current = null;
      setStatus(
        outcome.error ? "The reply failed." : outcome.stopped ? "You stopped the reply." : "Reply received."
      );
    },
    [commit, updateChat]
  );

  const send = () => {
    if (!chat || streamingID !== "") return;
    const text = draft.trim();
    if (text === "") return;
    const asked: Turn = { id: mintID("t"), role: "user", text };
    setDraft("");
    void runFrom(chat, [...chat.turns, asked]);
  };

  const stop = () => {
    running.current?.abort();
    setStatus("Stopping.");
  };

  const copy = async (text: string, what: string) => {
    try {
      await copyText(text);
      notify(`${what} copied.`);
    } catch {
      notify(clipboardBlocked, "danger");
    }
  };

  /** Turns delete in pairs. A transcript that ends up with two questions in a row
   *  is one the gateway refuses to send, and the operator would find that out at
   *  the next message rather than at the deletion. */
  const deleteExchange = async (turn: Turn) => {
    if (!chat) return;
    const index = chat.turns.findIndex((item) => item.id === turn.id);
    if (index < 0) return;
    const first = turn.role === "user" ? index : Math.max(0, index - 1);
    const count = chat.turns[first]?.role === "user" && chat.turns[first + 1]?.role === "assistant" ? 2 : 1;
    if (!(await ask({
      title: "Delete this exchange?",
      body:
        count === 2
          ? "The message and the reply under it are both removed from this chat. The request itself stays in the request log."
          : "The message is removed from this chat. The request itself stays in the request log.",
      confirmLabel: "Delete"
    }))) return;
    const turns = [...chat.turns.slice(0, first), ...chat.turns.slice(first + count)];
    commit(chatsRef.current.map((item) => (item.id === chat.id ? { ...item, turns } : item)));
  };

  const editTurn = (turn: Turn) => {
    if (!chat) return;
    const index = chat.turns.findIndex((item) => item.id === turn.id);
    if (index < 0) return;
    setDraft(turn.text);
    commit(chatsRef.current.map((item) => (item.id === chat.id ? { ...item, turns: item.turns.slice(0, index) } : item)));
  };

  const rerunTurn = (turn: Turn) => {
    if (!chat) return;
    const index = chat.turns.findIndex((item) => item.id === turn.id);
    if (index < 1) return;
    void runFrom(chat, chat.turns.slice(0, index));
  };

  const updateSettings = (change: Partial<RunSettings>) => {
    if (!chat) return;
    commit(
      chatsRef.current.map((item) =>
        item.id === chat.id ? { ...item, settings: { ...item.settings, ...change } } : item
      )
    );
  };

  const chooseRoute = (target: Route) => {
    if (!chat) return;
    setPickerOpen(false);
    // A transcript is a record of one model answering. Changing the model with
    // turns already in it would make the page a record of two, with nothing on
    // screen saying where the change happened.
    if (chat.turns.length > 0) {
      startChat(target);
      return;
    }
    commit(chatsRef.current.map((item) => (item.id === chat.id ? { ...item, model: target.public_alias } : item)));
  };

  const clearChat = async () => {
    if (!chat || chat.turns.length === 0) return;
    if (!(await ask({
      title: "Clear this chat?",
      body: "Every message in it is removed. The requests themselves stay in the request log.",
      confirmLabel: "Clear chat"
    }))) return;
    commit(chatsRef.current.map((item) => (item.id === chat.id ? { ...item, turns: [] } : item)));
  };

  const deleteChat = async () => {
    if (!chat) return;
    if (!(await ask({
      title: "Delete this chat?",
      body: "It is removed from this browser. The requests themselves stay in the request log.",
      confirmLabel: "Delete chat"
    }))) return;
    const remaining = chatsRef.current.filter((item) => item.id !== chat.id);
    commit(remaining);
    setSessionID(remaining[0]?.id ?? "", "replace");
  };

  // The transcript follows the reply while the operator is at the bottom of it,
  // and stays put when they have scrolled up to read something.
  //
  // Whether they are still at the bottom is decided by comparing the scroller
  // against the position this effect itself last wrote, and that comparison is
  // the whole trick. Two simpler versions of it are wrong. Measuring the
  // distance from the bottom on every render was the first: the effect runs
  // *after* React has painted the new text, so one chunk taller than the
  // threshold puts the scroller far from the bottom with nobody having touched
  // it, and following never resumes — measured on a forty-paragraph reply, it
  // stopped 1,210px short of the end and the operator watched a motionless page
  // while the reply was still being written. Listening for `scroll` instead was
  // the second: the event is dispatched a frame after the gesture, so a chunk
  // that renders inside that frame runs this effect first, still believing it is
  // being followed, and drags the operator back down — reproduced by scrolling a
  // mid-stream transcript to 1,200px and watching it return to 8,353px.
  //
  // Growing content never moves `scrollTop`. So a `scrollTop` that no longer
  // matches what was written here means a person moved it, and nothing else does.
  const following = useRef(true);
  const pinned = useRef(-1);
  const opened = useRef("");
  const replying = useRef("");

  useEffect(() => {
    const node = scroller.current;
    if (!node) return;
    if (opened.current !== (chat?.id ?? "")) {
      // A different conversation is a fresh read, and every chat opens on its
      // newest message however the last one was left.
      opened.current = chat?.id ?? "";
      following.current = true;
    } else if (replying.current === "" && streamingID !== "") {
      // A reply just started, which happens only because the operator asked for
      // one. Wherever they had scrolled to, the thing they want is at the bottom.
      following.current = true;
    } else if (pinned.current >= 0 && Math.abs(node.scrollTop - pinned.current) > 1) {
      following.current = node.scrollHeight - node.scrollTop - node.clientHeight < 160;
    }
    replying.current = streamingID;

    if (!following.current) return;
    node.scrollTop = node.scrollHeight;
    pinned.current = node.scrollTop;
  }, [chat?.id, chat?.turns, streamingID]);

  const drawer = useDrawerOverlay({
    open: listOpen,
    active: compact,
    onClose: () => setListOpen(false)
  });

  const needle = pickerQuery.trim().toLowerCase();
  const pickable = routes.filter(
    (item) =>
      needle === "" ||
      `${item.public_alias} ${item.upstream_model} ${item.provider.name}`.toLowerCase().includes(needle)
  );

  // A reply that came back with no text at all — the provider refused it, or you
  // pressed Stop before the first word — is still a turn in the transcript, and
  // the next message would send it. The gateway will not take an empty message
  // and would answer with "message 2 is empty", which names a position in a list
  // the operator never sees. So the composer closes here instead, and names the
  // menu on that turn, where both ways out already are. Only once the reply is
  // over: a turn that is still streaming is empty for its first few hundred
  // milliseconds, and closing the composer then would be a different lie.
  const lastTurn = chat?.turns[chat.turns.length - 1];
  const blocked = !chat
    ? ""
    : !route
      ? `${chat.model} is no longer a model route. Choose another model to keep going.`
      : streamingID === "" && lastTurn?.role === "assistant" && lastTurn.text === ""
        ? "The last reply came back with no text, and Rotakey will not send an empty message. Open the actions menu on that reply and choose Ask again, or delete the exchange, to keep going."
        : chat.turns.length >= maxTurns
          ? `This chat has ${maxTurns} messages, which is as many as Rotakey will send in one request. Start a new chat to keep going.`
          : contentLength(chat.turns) >= maxContent
            ? `This chat has reached ${formatNumber(maxContent)} characters, which is as much as Rotakey will send in one request. Start a new chat to keep going.`
            : "";

  const blockReason = route ? routeBlockReason(route) : "";
  // Every part names its own value. The old line read "Chosen by Rotakey · 16.4K
  // tokens · temperature 1 · streaming", where the first part was a floating
  // phrase, the second never said the number was a ceiling on the *reply*, and
  // the fourth had "one reply" as its opposite — three words that only make
  // sense to someone who already knows what the sheet behind them contains.
  const settingsSummary = chat
    ? [
        chat.settings.protocol === "auto"
          ? "Endpoint chosen by Rotakey"
          : `Sent as ${endpointLabel(chat.settings.protocol)}`,
        `up to ${formatNumber(chat.settings.maxTokens)} tokens back`,
        `temperature ${chat.settings.temperature}`,
        chat.settings.stream ? "reply streams in" : "reply arrives at once",
        chat.settings.system.trim() === "" ? "" : "with a system message"
      ]
        .filter((part) => part !== "")
        .join(" · ")
    : "";

  return (
    <div className="pg-page">
      <SectionHeader
        level={1}
        title="Playground"
        meta={
          loading
            ? "Loading model routes"
            : `${countOf(routes.length, "model route")} · ${countOf(chats.length, "chat")} in this browser`
        }
      />
      <p className="sr-only" role="status" aria-live="polite">
        {status}
      </p>

      {loadError !== "" && (
        <Notice tone="danger" title="The model routes could not be loaded">
          {loadError}
        </Notice>
      )}
      {storageNote !== "" && <Notice tone="warning">{storageNote}</Notice>}

      {!loading && routes.length === 0 && loadError === "" ? (
        <Empty
          level={2}
          size="page"
          title="There is nothing to send a message to"
          description="A model route is the name your applications ask for. Add one on Models and it appears here."
          action={<Button onClick={() => navigate("models")}>Go to Models</Button>}
        />
      ) : (
        <div className="pg-frame">
          <aside
            className={`pg-chats${listOpen ? " is-open" : ""}`}
            ref={drawer as Ref<HTMLElement>}
            tabIndex={-1}
            inert={compact && !listOpen}
            aria-label="Your chats"
          >
            <header className="pg-chats__head">
              <Label>Chats</Label>
              <Button
                variant="quiet"
                onClick={() => {
                  const base = route ?? routes[0];
                  if (base) startChat(base);
                }}
                disabled={routes.length === 0}
              >
                <Plus size={13} aria-hidden="true" /> New
              </Button>
            </header>
            <div className="pg-chats__list" onKeyDown={listKeys}>
              {chats.map((item) => (
                <button
                  key={item.id}
                  type="button"
                  data-row
                  className={`pg-chats__row${item.id === chat?.id ? " is-selected" : ""}`}
                  onClick={() => {
                    setSessionID(item.id);
                    setListOpen(false);
                  }}
                >
                  <span className="pg-chats__title">{conversationTitle(item)}</span>
                  <span className="pg-chats__model">{item.model}</span>
                </button>
              ))}
            </div>
            <p className="pg-chats__note">
              Chats are kept in this browser only, up to {maxConversations}. Nothing is sent anywhere until you
              press Send.
            </p>
          </aside>

          <section className="pg-chat" aria-label="Conversation">
            <header className="pg-chat__head">
              {compact && (
                <button
                  type="button"
                  className="icon-button"
                  onClick={() => setListOpen(true)}
                  aria-label="Show your chats"
                  title="Chats"
                >
                  <MessagesSquare size={16} aria-hidden="true" />
                </button>
              )}
              <button
                type="button"
                className="pg-model"
                onClick={() => setPickerOpen(true)}
                disabled={routes.length === 0}
              >
                {route && <Dot state={routeState(route)} />}
                <span className="pg-model__name">{chat?.model ?? "Choose a model"}</span>
                <ChevronDown size={14} aria-hidden="true" />
              </button>
              <span className="pg-chat__where">
                {route && (
                  <>
                    <span className="pg-chat__provider">{route.provider.name}</span>
                    <span className="pg-chat__upstream">{route.upstream_model}</span>
                  </>
                )}
              </span>
              <Menu label="More actions for this chat">
                <MenuItem
                  disabled={!chat || chat.turns.length === 0}
                  onSelect={() =>
                    copy(
                      (chat?.turns ?? [])
                        .map((turn) => `${turn.role === "user" ? "You" : turn.model || "Model"}:\n${turn.text}`)
                        .join("\n\n"),
                      "Transcript"
                    )
                  }
                >
                  Copy the whole transcript
                </MenuItem>
                <MenuItem
                  disabled={!route}
                  onSelect={() => navigate("requests", { q: chat?.model ?? "" })}
                >
                  Open this model in the request log
                </MenuItem>
                <MenuItem disabled={!route} onSelect={() => navigate("models", { q: chat?.model ?? "" })}>
                  Edit this route on Models
                </MenuItem>
                <MenuItem
                  tone="danger"
                  disabled={!chat || chat.turns.length === 0 || streamingID !== ""}
                  onSelect={clearChat}
                >
                  Clear the messages
                </MenuItem>
                <MenuItem
                  tone="danger"
                  disabled={!chat || streamingID !== ""}
                  icon={<Trash2 size={14} aria-hidden="true" />}
                  onSelect={deleteChat}
                >
                  Delete this chat
                </MenuItem>
              </Menu>
            </header>

            <div className="pg-scroll" ref={scroller}>
              {chat && chat.turns.length > 0 ? (
                <Transcript
                  turns={chat.turns}
                  streamingID={streamingID}
                  onCopy={(turn) => void copy(turn.text, "Message")}
                  onEdit={editTurn}
                  onRerun={rerunTurn}
                  onDelete={(turn) => void deleteExchange(turn)}
                />
              ) : (
                <Empty
                  level={2}
                  size="pane"
                  title="Nothing sent yet"
                  description="What you send here goes through the same key rotation, limits and failover as your applications. Every reply says which key answered it."
                />
              )}
            </div>

            <div className="pg-foot">
              {blockReason !== "" && blocked === "" && (
                <Notice
                  tone="warning"
                  action={
                    <Button variant="quiet" onClick={() => navigate("models", { q: chat?.model ?? "" })}>
                      Open the route
                    </Button>
                  }
                >
                  This route cannot serve a request right now: {blockReason}. Sending will fail until that is
                  fixed.
                </Notice>
              )}
              <button type="button" className="pg-runline" onClick={() => setSettingsOpen(true)} disabled={!chat}>
                <SlidersHorizontal size={13} aria-hidden="true" />
                <span>{settingsSummary}</span>
              </button>
              <Composer
                value={draft}
                onChange={setDraft}
                onSend={send}
                onStop={stop}
                running={streamingID !== ""}
                blocked={blocked}
                model={chat?.model ?? "the model"}
              />
            </div>
          </section>
        </div>
      )}

      {pickerOpen && (
        <Sheet title="Choose a model" onClose={() => setPickerOpen(false)}>
          <SearchInput
            label="Search model routes"
            value={pickerQuery}
            onChange={setPickerQuery}
            placeholder="Alias, model or provider"
          />
          <div className="pg-picker" onKeyDown={listKeys}>
            {pickable.map((item) => {
              const state = routeState(item);
              const ready = readyKeyCount(item);
              return (
                <button
                  key={item.id}
                  type="button"
                  data-row
                  className={`pg-picker__row${item.public_alias === chat?.model ? " is-selected" : ""}`}
                  onClick={() => chooseRoute(item)}
                >
                  <Dot state={state} />
                  <span className="pg-picker__alias">{item.public_alias}</span>
                  <span className="pg-picker__where">
                    {item.provider.name} · {item.upstream_model}
                  </span>
                  <span className="pg-picker__keys">
                    {ready === 0 ? routeBlockReason(item) : `${formatNumber(ready)} keys ready`}
                  </span>
                </button>
              );
            })}
          </div>
          {pickable.length === 0 && (
            <Empty
              level={3}
              size="pane"
              title="No route matches that"
              description="Search by the alias your applications ask for, the provider's own model name, or the provider."
            />
          )}
        </Sheet>
      )}

      {settingsOpen && chat && (
        <Sheet title="Run settings" onClose={() => setSettingsOpen(false)}>
          <p className="pg-sheet-note">
            These apply to this chat only, and take effect on the next message you send.
          </p>
          <Field
            label="System message"
            hint="optional"
          >
            <textarea
              rows={4}
              value={chat.settings.system}
              onChange={(event) => updateSettings({ system: event.target.value })}
              placeholder="Standing instructions sent ahead of every message in this chat"
            />
          </Field>
          <Field label="Send as" hint="the wire format">
            <select
              value={chat.settings.protocol}
              onChange={(event) => updateSettings({ protocol: event.target.value as RunProtocol })}
            >
              <option value="auto">{endpointLabel("auto")}</option>
              <option value="chat">{endpointLabel("chat")}</option>
              <option value="responses">{endpointLabel("responses")}</option>
              <option value="messages">{endpointLabel("messages")}</option>
            </select>
          </Field>
          <p className="pg-sheet-note">
            Leave this on {endpointLabel("auto")} unless you are testing one path in particular. Rotakey
            translates between all three, and the reply says which one it used.
          </p>
          <NumberField
            label="Reply limit"
            hint="tokens"
            value={chat.settings.maxTokens}
            min={1}
            max={1_000_000}
            step={1}
            onCommit={(value) => updateSettings({ maxTokens: value })}
          />
          <NumberField
            label="Temperature"
            hint="0 is repeatable, 2 is wild"
            value={chat.settings.temperature}
            min={0}
            max={2}
            step={0.1}
            onCommit={(value) => updateSettings({ temperature: value })}
          />
          <Toggle
            checked={chat.settings.stream}
            onChange={(value) => updateSettings({ stream: value })}
            label="Show the reply as it is written"
            description="Turning this off waits for the whole reply. Streaming gives up automatic key failover once the first byte is sent, so a provider that fails mid-reply is not retried on another key."
          />
        </Sheet>
      )}
    </div>
  );
}
