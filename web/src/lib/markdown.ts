/**
 * A small markdown reader for model replies.
 *
 * It is hand-written rather than installed for two reasons. The console has one
 * runtime dependency and the plan is to keep it that way; and every library
 * worth using renders to HTML, which would mean handing a model's output to
 * dangerouslySetInnerHTML. This produces a tree instead, and React escapes
 * every string in it. A reply cannot inject markup no matter what it contains.
 *
 * Two things it does that a general markdown parser does not need to:
 *
 *  - It parses a reply that is still arriving. An unterminated fence is an open
 *    code block, not three literal backticks, so a code block does not flicker
 *    between shapes as it streams in.
 *  - It refuses any link scheme other than http, https and mailto. A model can
 *    write `[click](javascript:…)` as easily as anything else, and the operator
 *    reading the reply did not author it.
 *
 * What it deliberately does not do: syntax highlighting, tables, footnotes,
 * reference links, or HTML passthrough. A model reply that contains raw HTML
 * shows the HTML, which is the honest thing to do with it.
 */

export type Span =
  | { kind: "text"; text: string }
  | { kind: "code"; text: string }
  | { kind: "strong"; spans: Span[] }
  | { kind: "em"; spans: Span[] }
  | { kind: "strike"; spans: Span[] }
  | { kind: "link"; href: string; spans: Span[] }
  | { kind: "break" };

export type Block =
  | { kind: "paragraph"; spans: Span[] }
  | { kind: "heading"; level: number; spans: Span[] }
  | { kind: "code"; language: string; code: string; open: boolean }
  | { kind: "list"; ordered: boolean; start: number; items: Span[][] }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "rule" };

const FENCE = /^(\s{0,3})(`{3,}|~{3,})\s*([^`]*)$/;
const HEADING = /^ {0,3}(#{1,6})\s+(.*)$/;
const RULE = /^ {0,3}([-*_])(\s*\1){2,}\s*$/;
const BULLET = /^ {0,3}[-*+]\s+(.*)$/;
const NUMBERED = /^ {0,3}(\d{1,9})[.)]\s+(.*)$/;
const QUOTE = /^ {0,3}> ?(.*)$/;

export function parseMarkdown(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let paragraph: string[] = [];

  const flush = () => {
    if (paragraph.length === 0) return;
    blocks.push({ kind: "paragraph", spans: parseSpans(paragraph.join("\n")) });
    paragraph = [];
  };

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];

    const fence = FENCE.exec(line);
    if (fence) {
      flush();
      const marker = fence[2];
      const code: string[] = [];
      let closed = false;
      index++;
      for (; index < lines.length; index++) {
        const candidate = lines[index].trim();
        if (candidate.startsWith(marker[0].repeat(marker.length)) && /^[`~]+$/.test(candidate)) {
          closed = true;
          break;
        }
        code.push(lines[index]);
      }
      blocks.push({
        kind: "code",
        language: fence[3].trim().split(/\s+/)[0] ?? "",
        code: code.join("\n"),
        open: !closed
      });
      continue;
    }

    if (line.trim() === "") {
      flush();
      continue;
    }

    const rule = RULE.exec(line);
    if (rule) {
      flush();
      blocks.push({ kind: "rule" });
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      flush();
      blocks.push({
        kind: "heading",
        level: heading[1].length,
        spans: parseSpans(heading[2].replace(/\s+#+\s*$/, ""))
      });
      continue;
    }

    if (QUOTE.test(line)) {
      flush();
      const quoted: string[] = [];
      for (; index < lines.length; index++) {
        const inner = QUOTE.exec(lines[index]);
        if (!inner) break;
        quoted.push(inner[1]);
      }
      index--;
      blocks.push({ kind: "quote", blocks: parseMarkdown(quoted.join("\n")) });
      continue;
    }

    if (BULLET.test(line) || NUMBERED.test(line)) {
      flush();
      const ordered = !BULLET.test(line);
      const first = NUMBERED.exec(line);
      const items: string[] = [];
      for (; index < lines.length; index++) {
        const current = lines[index];
        const bullet = BULLET.exec(current);
        const numbered = NUMBERED.exec(current);
        if (bullet && !ordered) {
          items.push(bullet[1]);
          continue;
        }
        if (numbered && ordered) {
          items.push(numbered[2]);
          continue;
        }
        // An indented or plain line directly under an item continues it, which
        // is how a model writes a wrapped bullet.
        if (items.length > 0 && current.trim() !== "" && !bullet && !numbered) {
          items[items.length - 1] += `\n${current.trim()}`;
          continue;
        }
        break;
      }
      index--;
      blocks.push({
        kind: "list",
        ordered,
        start: ordered && first ? Number(first[1]) : 1,
        items: items.map(parseSpans)
      });
      continue;
    }

    paragraph.push(line);
  }
  flush();
  return blocks;
}

/** Only schemes a reply cannot use to run something survive. */
export function safeHref(href: string): string | null {
  const value = href.trim();
  if (value === "") return null;
  if (/^(https?:|mailto:)/i.test(value)) return value;
  // A bare path or fragment is safe and common in documentation replies.
  if (/^[./#?]/.test(value)) return value;
  return null;
}

/** The link label is bounded on both length and newlines, and both bounds are
 *  load-bearing rather than stylistic. An unbounded `[^\]]*` is greedy to the end
 *  of the paragraph, so a reply carrying a long run of `[` with no `]` — which a
 *  model will produce if you ask it to — costs the engine one full scan per
 *  bracket, and the whole parse goes quadratic: 100k characters measured at 6.3
 *  seconds on the main thread, re-paid on every stream delta. Capping the label
 *  makes the work per bracket constant. A link whose text runs past 512
 *  characters or across a line break is not one a reply writes; it falls through
 *  and renders as the literal text it was. */
const INLINE = /(`+)([\s\S]*?)\1|(\*\*|__)([\s\S]+?)\3|(~~)([\s\S]+?)\5|(\*|_)([\s\S]+?)\7|\[([^\]\n]{0,512})\]\(([^()\s]*)\)/;

export function parseSpans(source: string): Span[] {
  const spans: Span[] = [];
  let rest = source;

  const pushText = (text: string) => {
    if (text === "") return;
    // Every newline inside a paragraph breaks the line, with or without the two
    // trailing spaces CommonMark asks for. A model writing two sentences on two
    // lines means two lines, and it did not put the spaces there.
    const parts = text.split(/ {2,}\n|\n/);
    parts.forEach((part, index) => {
      if (index > 0) spans.push({ kind: "break" });
      if (part !== "") spans.push({ kind: "text", text: part });
    });
  };

  for (;;) {
    const match = INLINE.exec(rest);
    if (!match || match.index === undefined) break;
    pushText(rest.slice(0, match.index));
    rest = rest.slice(match.index + match[0].length);

    if (match[1] !== undefined) {
      spans.push({ kind: "code", text: match[2].replace(/^ | $/g, "") });
    } else if (match[3] !== undefined) {
      spans.push({ kind: "strong", spans: parseSpans(match[4]) });
    } else if (match[5] !== undefined) {
      spans.push({ kind: "strike", spans: parseSpans(match[6]) });
    } else if (match[7] !== undefined) {
      spans.push({ kind: "em", spans: parseSpans(match[8]) });
    } else {
      const href = safeHref(match[10] ?? "");
      const label = parseSpans(match[9] ?? "");
      if (href) {
        spans.push({ kind: "link", href, spans: label });
      } else {
        // A refused scheme is shown exactly as the model wrote it, so nothing
        // disappears silently and the operator can see what it tried to link.
        pushText(`[${match[9] ?? ""}](${match[10] ?? ""})`);
      }
    }
  }
  pushText(rest);
  return spans;
}
