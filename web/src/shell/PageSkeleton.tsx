/** A page that has been asked for and has not arrived. Four bars rather than a
 *  spinner, so the shape that appears is the shape that was already there — and one
 *  `sr-only` line, because "loading" is the whole of what a screen reader can
 *  usefully be told about a rectangle. */

export function PageSkeleton() {
  return (
    <div className="page-skeleton" role="status" aria-live="polite">
      <span className="sr-only">Loading…</span>
      <span aria-hidden="true" />
      <span aria-hidden="true" />
      <span aria-hidden="true" />
      <span aria-hidden="true" />
    </div>
  );
}
