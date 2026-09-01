/** Where the operator writes.
 *
 *  The box grows with what is in it up to a ceiling and then scrolls, so a
 *  three-word prompt does not occupy a third of the page and a long one is still
 *  readable. The old composer was a fixed box with its send button positioned at
 *  `right: 27px; bottom: 27px`, which put it on top of the text at 1024px.
 *
 *  Enter sends and Shift+Enter starts a line, with one exception that is not
 *  optional: while an input method editor is composing — every non-Latin
 *  keyboard, including the one this console's owner types Bengali on — Enter is
 *  how a candidate is accepted, and intercepting it sends half a word.
 */

import { useEffect, useRef, type KeyboardEvent } from "react";
import { Send, Square } from "lucide-react";
import { Button } from "../../ui";

export function Composer({
  value,
  onChange,
  onSend,
  onStop,
  running,
  blocked,
  model
}: {
  value: string;
  onChange: (value: string) => void;
  onSend: () => void;
  onStop: () => void;
  running: boolean;
  /** Why nothing can be sent, in a sentence that says what to do instead. Empty
   *  when the composer is usable. */
  blocked: string;
  model: string;
}) {
  const box = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    const node = box.current;
    if (!node) return;
    // Measured from scratch each time: shrinking needs the box to be small
    // before its content height means anything. The ceiling is `max-height` in
    // the stylesheet, so the number that decides how tall this gets is written
    // where every other size in the console is written.
    node.style.height = "auto";
    node.style.height = `${node.scrollHeight}px`;
  }, [value]);

  const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
    event.preventDefault();
    if (!running && blocked === "" && value.trim() !== "") onSend();
  };

  return (
    <form
      className="pg-composer"
      onSubmit={(event) => {
        event.preventDefault();
        onSend();
      }}
    >
      <textarea
        ref={box}
        className="pg-composer__box"
        value={value}
        rows={1}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={onKeyDown}
        disabled={blocked !== ""}
        placeholder={blocked === "" ? "Send a message" : ""}
        aria-label={`Message ${model}`}
      />
      <div className="pg-composer__bar">
        <p className="pg-composer__hint">
          {blocked === "" ? "Enter sends · Shift + Enter starts a new line" : blocked}
        </p>
        {running ? (
          <Button type="button" variant="quiet" onClick={onStop}>
            <Square size={12} aria-hidden="true" /> Stop
          </Button>
        ) : (
          <Button type="submit" disabled={blocked !== "" || value.trim() === ""}>
            <Send size={13} aria-hidden="true" /> Send
          </Button>
        )}
      </div>
    </form>
  );
}
