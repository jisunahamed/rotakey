/** Copying text, from anywhere in the console.
 *
 *  This lived inside App.tsx, which meant the crash panel — the one screen whose
 *  whole job is handing the operator something to paste into a report — could not
 *  reach it. It is a pure function with no console state in it, so it moves out
 *  whole. */

/** Every "your browser blocked the copy" message in the console is this sentence.
 *  It names the way out rather than the failure, because there is nothing the
 *  operator can do about the permission and something they can do about the text. */
export const clipboardBlocked =
  "Your browser blocked the copy. Select the text and copy it manually.";

export async function copyText(value: string) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value);
      return;
    } catch {
      // HTTP/IP deployments often block the modern Clipboard API. Fall through.
    }
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.readOnly = true;
  textarea.style.position = "fixed";
  textarea.style.inset = "0 auto auto -9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  textarea.setSelectionRange(0, value.length);
  try {
    if (!document.execCommand("copy")) throw new Error("Clipboard copy failed");
  } finally {
    textarea.remove();
  }
}
