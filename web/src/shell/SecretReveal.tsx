/** A key the gateway will never send again, shown once. The checkbox is the whole
 *  point of the panel: it makes the operator state that they have stored the key
 *  before the only copy of it leaves the screen, and it makes closing the panel a
 *  dirty-discard the `Sheet` has to ask about. */

import { useState } from "react";
import { Clipboard } from "lucide-react";
import { clipboardBlocked, copyText } from "../clipboard";
import { Button, InlineNotice, Sheet } from "../ui";

export function SecretReveal({ title, keyValue, message, onClose, notify }: { title: string; keyValue: string; message: string; onClose: () => void; notify: (message: string, tone?: "success" | "danger") => void }) {
  const [confirmed, setConfirmed] = useState(false);
  return (
    <Sheet
      title={title}
      eyebrow="One-time secret"
      onClose={onClose}
      dirty={!confirmed}
      discardMessage="Close without confirming that the key is saved? This key cannot be shown again."
    >
      <InlineNotice tone="danger">{message}</InlineNotice>
      <div className="secret-value"><code>{keyValue}</code><Button variant="quiet" onClick={() => void copyText(keyValue).then(() => notify("Gateway key copied.")).catch(() => notify(clipboardBlocked, "danger"))}><Clipboard size={14} aria-hidden="true" /> Copy</Button></div>
      <label className="confirmation-check"><input type="checkbox" checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)} /><span>I stored this key securely.</span></label>
      {/* The button dismisses the panel and nothing else — the key already exists.
          "Finish" implied a remaining step, and the checkbox above it is what the
          operator actually finishes. */}
      <div className="sheet-actions"><span /><Button disabled={!confirmed} onClick={onClose}>Close</Button></div>
    </Sheet>
  );
}
