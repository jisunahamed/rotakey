/** Focus plumbing shared by every overlay in the console: the sheet, the
 * confirmation dialog, and the inspector drawers once they float over the list
 * instead of sitting beside it. It lives on its own so the dialog does not have to
 * import from the component file that in turn asks the dialog a question.
 */

export const focusableSelector =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])';

/** Keeps Tab inside `container` by bouncing off either end. */
export function trapTab(container: HTMLElement, event: KeyboardEvent) {
  const focusable = Array.from(
    container.querySelectorAll<HTMLElement>(focusableSelector)
  ).filter((element) => element.offsetParent !== null || element === document.activeElement);
  if (focusable.length === 0) return;
  const edge = event.shiftKey ? focusable[0] : focusable[focusable.length - 1];
  if (document.activeElement === edge) {
    event.preventDefault();
    (event.shiftKey ? focusable[focusable.length - 1] : focusable[0]).focus();
  }
}
