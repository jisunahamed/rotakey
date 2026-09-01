import { useEffect, useMemo, useRef, useState } from "react";
import { Check, Copy } from "lucide-react";
import { parseMarkdown, type Block, type Span } from "../lib/markdown";
import { clipboardBlocked, copyText } from "../clipboard";

/** Renders a model's reply.
 *
 *  The parsing is in `lib/markdown.ts` and is deliberately not HTML: this walks
 *  the tree and emits React elements, so every string a model produced is
 *  escaped by React on its way to the screen. There is no path from a reply to
 *  markup, whatever the reply contains.
 *
 *  `baseHeading` is required in spirit and defaulted in practice, for the same
 *  reason `Empty` takes a level: a model that opens with `# Title` would emit an
 *  `<h1>` into a page that already has one, and a screen reader would read the
 *  transcript as a second document. The reply's headings are nested underneath
 *  whatever heading introduced the reply. */
export function Markdown({
  text,
  baseHeading = 3,
  className = ""
}: {
  text: string;
  baseHeading?: number;
  className?: string;
}) {
  // Memoised on the text alone, because the transcript sits in the same
  // component as the composer: without this, every keystroke in the draft
  // re-parses every reply above it, and a long conversation pays for its whole
  // history on each letter typed.
  const blocks = useMemo(() => parseMarkdown(text), [text]);
  return (
    <div className={`ui-md ${className}`.trim()}>
      {blocks.map((block, index) => (
        <BlockView key={index} block={block} baseHeading={baseHeading} />
      ))}
    </div>
  );
}

function BlockView({ block, baseHeading }: { block: Block; baseHeading: number }) {
  switch (block.kind) {
    case "paragraph":
      return (
        <p className="ui-md__p">
          <Spans spans={block.spans} />
        </p>
      );
    case "heading": {
      const depth = Math.min(6, baseHeading + block.level - 1);
      const Heading = `h${depth}` as "h3";
      return (
        <Heading className="ui-md__h">
          <Spans spans={block.spans} />
        </Heading>
      );
    }
    case "code":
      return <CodeBlock language={block.language} code={block.code} open={block.open} />;
    case "list": {
      const items = block.items.map((spans, index) => (
        <li key={index} className="ui-md__li">
          <Spans spans={spans} />
        </li>
      ));
      return block.ordered ? (
        <ol className="ui-md__ol" start={block.start}>
          {items}
        </ol>
      ) : (
        <ul className="ui-md__ul">{items}</ul>
      );
    }
    case "quote":
      return (
        <blockquote className="ui-md__quote">
          {block.blocks.map((inner, index) => (
            <BlockView key={index} block={inner} baseHeading={baseHeading} />
          ))}
        </blockquote>
      );
    case "rule":
      return <hr className="ui-md__rule" />;
  }
}

function Spans({ spans }: { spans: Span[] }) {
  return (
    <>
      {spans.map((span, index) => {
        switch (span.kind) {
          case "text":
            return <span key={index}>{span.text}</span>;
          case "code":
            return (
              <code key={index} className="ui-md__inline-code">
                {span.text}
              </code>
            );
          case "strong":
            return (
              <strong key={index}>
                <Spans spans={span.spans} />
              </strong>
            );
          case "em":
            return (
              <em key={index}>
                <Spans spans={span.spans} />
              </em>
            );
          case "strike":
            return (
              <s key={index}>
                <Spans spans={span.spans} />
              </s>
            );
          case "link":
            return (
              // A model wrote this destination, so it is treated as untrusted
              // outbound: a new tab, and no window handle back to the console.
              <a key={index} className="ui-md__link" href={span.href} target="_blank" rel="noreferrer noopener">
                <Spans spans={span.spans} />
              </a>
            );
          case "break":
            return <br key={index} />;
        }
      })}
    </>
  );
}

/** A fenced block, with the language the model named and a way to take the code
 *  out of the console. The label is not decoration: it is the only thing on
 *  screen that says what the block is, since nothing here highlights syntax. */
function CodeBlock({ language, code, open }: { language: string; code: string; open: boolean }) {
  const [note, setNote] = useState<"idle" | "copied" | "blocked">("idle");
  const timer = useRef<number | undefined>(undefined);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  const copy = async () => {
    try {
      await copyText(code);
      setNote("copied");
    } catch {
      setNote("blocked");
    }
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setNote("idle"), 2400);
  };

  return (
    <figure className="ui-md__code">
      <figcaption className="ui-md__code-bar">
        <span className="ui-md__code-lang">{language || "text"}</span>
        <button type="button" className="ui-md__code-copy" onClick={copy}>
          {note === "copied" ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
          {note === "copied" ? "Copied" : note === "blocked" ? "Copy blocked" : "Copy"}
        </button>
      </figcaption>
      <pre className="ui-md__pre">
        <code>{code}</code>
      </pre>
      {/* The message names what the operator can do about it, because there is
          nothing they can do about the browser's clipboard permission. */}
      {note === "blocked" ? <p className="ui-md__code-note">{clipboardBlocked}</p> : null}
      {open ? <p className="ui-md__code-note">Still arriving.</p> : null}
    </figure>
  );
}
