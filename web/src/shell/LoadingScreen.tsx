/** The half-second before the console knows whether anyone is signed in. It draws
 *  no shell and no page, because it does not yet know which of the three it is
 *  about to become. */

import { Cable } from "lucide-react";

export function LoadingScreen() {
  return (
    <div className="auth-shell">
      <div className="boot-sequence" aria-label="Loading Rotakey">
        <Cable size={24} aria-hidden="true" />
        <div className="boot-line">
          <span />
        </div>
        <p>Starting Rotakey</p>
      </div>
    </div>
  );
}
