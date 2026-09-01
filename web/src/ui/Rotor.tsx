/** The rotor: one segment per API key in the pool, in the order the router walks
 *  them, coloured by the state that decides whether it will be used, with the key
 *  serving the next request marked. It answers the question the inspector is opened
 *  for — which key is up, and how much of the pool can still serve — in a single
 *  glance, where the list below it answers the same question one row at a time.
 *
 *  It earns its place at forty keys rather than at four: a pool that size is a long
 *  scroll, and the band is the only reading that fits the whole of it on screen.
 *
 *  The track is decorative: every segment's information is already in the list
 *  underneath it as a labelled, focusable row, or on the Providers page in the key
 *  rows below. So the track is hidden from assistive technology and the caption
 *  above it carries the reading in words. */
export function Rotor({
  keys,
  stalled = false,
  stalledNote
}: {
  keys: Array<{ id: string; status: string; cursor?: boolean; unknown?: boolean }>;
  /** True when nothing in the pool can serve the next request. The track has no
   *  cursor to point at, which has to be said in words as well as drawn. */
  stalled?: boolean;
  stalledNote?: string;
}) {
  if (keys.length === 0) return null;
  const servable = keys.filter((key) => key.status === "healthy").length;
  return (
    <div className={`rotor${stalled ? " rotor--stalled" : ""}`}>
      <div className="rotor__caption">
        <span>Key rotation</span>
        <span className="rotor__tally">
          {servable}/{keys.length} can serve
        </span>
      </div>
      <div className="rotor__track" aria-hidden="true">
        {keys.map((key) => (
          <span
            key={key.id}
            className={`rotor__segment rotor__segment--${key.unknown ? "unknown" : key.status}${key.cursor ? " rotor__segment--cursor" : ""}`}
          />
        ))}
      </div>
      {stalled && stalledNote && <p className="rotor__stalled-note">{stalledNote}</p>}
    </div>
  );
}
