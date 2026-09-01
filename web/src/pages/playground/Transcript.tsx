/** The conversation, set as a document.
 *
 *  Not a chat bubble in sight. This console is an instrument panel and a
 *  transcript is a record, so the turns are a numbered list: who spoke, what
 *  they said, and — under a reply — what the gateway actually did to produce it.
 *
 *  The evidence line under each reply is the reason this page exists. Everything
 *  in it was already being computed and thrown away: the response headers name
 *  the parameters the gateway dropped, the endpoint it switched to and the
 *  request id, and the log row names the key that served it. None of it reached
 *  a screen before.
 */

import { Copy, Pencil, RotateCcw, Trash2 } from "lucide-react";
import { Label, Markdown, Menu, MenuItem } from "../../ui";
import { apiFamilyLabel, endpointLabel } from "../../lib/copy";
import { formatLatency, formatNumber } from "../../lib/format";
import { pathFor } from "../../routes";
import type { Evidence, Turn } from "./session";

/** `chat` and `responses` are OpenAI-shaped; `messages` is Anthropic-shaped.
 *  Used only to decide whether a translation is worth mentioning. */
function familyOf(protocol: string) {
  return protocol === "messages" ? "anthropic" : "openai";
}

/** Every item is a phrase that begins with a noun or a verb, and none of them
 *  contains the separator the strip itself uses. Both rules come from the same
 *  reading: the strip used to print "azure/gpt-5.6-sol · 5.2s · 7 in · 306 out ·
 *  Chat Completions · eastus · primary", where the model repeated the author
 *  label forty pixels above it, "306 out" never said out of what, and the key's
 *  own label — free text the operator typed — contained a dot of its own, so
 *  there was no way to see where one fact ended and the next began. */
function EvidenceStrip({ evidence, stopped }: { evidence: Evidence; stopped: boolean }) {
  // An empty protocol means no response headers were ever read, which means the
  // gateway refused the request before it reached a model. Every other fact in
  // this strip describes an answer, and there was not one: "Answered in 428ms ·
  // tokens not reported" under a rate-limit refusal is the console reporting a
  // reply that never existed. The endpoint and the key suppress themselves
  // because both are read off that same missing response.
  const answered = evidence.protocol !== "";
  const opening = stopped ? "Stopped after" : answered ? "Answered in" : "Refused after";
  const facts: string[] = [`${opening} ${formatLatency(evidence.elapsedMS)}`];
  if (answered) {
    if (evidence.inputTokens !== null || evidence.outputTokens !== null) {
      const input = evidence.inputTokens === null ? "—" : formatNumber(evidence.inputTokens);
      const output = evidence.outputTokens === null ? "—" : formatNumber(evidence.outputTokens);
      facts.push(`${input} tokens in, ${output} out`);
    } else {
      // Eight of the nine protocol pairs record an estimate or a zero in the log
      // row for a streamed request, so a number here would be a guess wearing the
      // clothes of a measurement.
      facts.push("tokens not reported");
    }
  }
  const endpoint = endpointLabel(evidence.protocol);
  if (endpoint) facts.push(`Sent as ${endpoint}`);
  if (evidence.servedBy) facts.push(`Served by ${evidence.servedBy}`);

  const notes: string[] = [];
  if (evidence.switched) {
    notes.push(`Sent to /${evidence.switched} instead — the provider refused the first endpoint.`);
  }
  if (evidence.removed.length > 0) {
    notes.push(`Dropped before sending: ${evidence.removed.join(", ")}. The provider had rejected them.`);
  }
  if (evidence.replaced.length > 0) {
    notes.push(`Renamed before sending: ${evidence.replaced.map(([from, to]) => `${from} → ${to}`).join(", ")}.`);
  }
  if (evidence.logFound && familyOf(evidence.upstreamProtocol) !== familyOf(evidence.protocol)) {
    const family = apiFamilyLabel(evidence.upstreamProtocol);
    if (family) notes.push(`Translated by Rotakey into ${family}.`);
  }

  return (
    <div className="pg-evidence">
      <ul className="pg-facts">
        {facts.map((fact) => (
          <li key={fact}>{fact}</li>
        ))}
        {evidence.requestID !== "" && (
          <li>
            <a className="pg-facts__link" href={pathFor("requests", { q: evidence.requestID })}>
              Open this request
            </a>
          </li>
        )}
      </ul>
      {notes.map((note) => (
        <p className="pg-note" key={note}>
          {note}
        </p>
      ))}
    </div>
  );
}

function TurnView({
  turn,
  streaming,
  onCopy,
  onEdit,
  onRerun,
  onDelete
}: {
  turn: Turn;
  streaming: boolean;
  onCopy: (turn: Turn) => void;
  onEdit: (turn: Turn) => void;
  onRerun: (turn: Turn) => void;
  onDelete: (turn: Turn) => void;
}) {
  const author = turn.role === "user" ? "You" : turn.model || "Model";
  const empty = turn.text === "" && !turn.error;
  return (
    <li className={`pg-turn pg-turn--${turn.role}`} aria-busy={streaming || undefined}>
      <div className="pg-turn__meta">
        <Label>{author}</Label>
        <div className="pg-turn__tools">
          <button
            type="button"
            className="icon-button"
            onClick={() => onCopy(turn)}
            disabled={turn.text === ""}
            title="Copy this message"
            aria-label={`Copy the message from ${author}`}
          >
            <Copy size={14} aria-hidden="true" />
          </button>
          <Menu label={`More actions for the message from ${author}`}>
            {turn.role === "user" ? (
              <MenuItem icon={<Pencil size={14} aria-hidden="true" />} onSelect={() => onEdit(turn)}>
                Edit and send again
              </MenuItem>
            ) : (
              <MenuItem icon={<RotateCcw size={14} aria-hidden="true" />} onSelect={() => onRerun(turn)}>
                Ask again
              </MenuItem>
            )}
            <MenuItem tone="danger" icon={<Trash2 size={14} aria-hidden="true" />} onSelect={() => onDelete(turn)}>
              Delete this exchange
            </MenuItem>
          </Menu>
        </div>
      </div>
      <div className="pg-turn__body">
        {turn.role === "user" ? (
          <p className="pg-said">{turn.text}</p>
        ) : (
          <>
            {turn.text !== "" && <Markdown text={turn.text} baseHeading={4} />}
            {streaming && (
              <p className="pg-writing pg-writing--live">
                {turn.text === "" ? "Waiting for the first words." : "Still writing."}
              </p>
            )}
            {!streaming && empty && !turn.stopped && (
              <p className="pg-writing">The model answered with no text. Try a larger reply limit.</p>
            )}
            {!streaming && turn.stopped && <p className="pg-writing">You stopped this reply.</p>}
          </>
        )}
      </div>
      {turn.error && (
        <p className="pg-failed">
          {turn.error}
          {/* The test is "did the reply already start", not "which error code came
              back". `streamed` is only true once the response headers arrived, and
              headers are the moment the gateway commits to a key — after that no
              path retries, whether the failure arrives as an error frame inside
              the 200 or as a dropped connection. Keying this on the
              `stream_interrupted` code alone said nothing on the in-band case,
              which is the common one. */}
          {turn.evidence?.streamed && (
            <span> Streaming gives up automatic key failover once the first byte is sent, so this one did not retry.</span>
          )}
        </p>
      )}
      {turn.evidence && !streaming && <EvidenceStrip evidence={turn.evidence} stopped={turn.stopped === true} />}
    </li>
  );
}

export function Transcript({
  turns,
  streamingID,
  onCopy,
  onEdit,
  onRerun,
  onDelete
}: {
  turns: Turn[];
  /** The reply currently being written, if any. */
  streamingID: string;
  onCopy: (turn: Turn) => void;
  onEdit: (turn: Turn) => void;
  onRerun: (turn: Turn) => void;
  onDelete: (turn: Turn) => void;
}) {
  return (
    <ol className="pg-turns">
      {turns.map((turn) => (
        <TurnView
          key={turn.id}
          turn={turn}
          streaming={turn.id === streamingID}
          onCopy={onCopy}
          onEdit={onEdit}
          onRerun={onRerun}
          onDelete={onDelete}
        />
      ))}
    </ol>
  );
}
