import React from "react";
import ReactDOM from "react-dom/client";
// Three faces, three jobs: Archivo for language, JetBrains Mono for data that has
// to align in a column, Martian Mono for the uppercase micro-labels only. The
// variable builds carry every weight the console asks for in one file each, and
// each subset is its own @font-face, so only latin is fetched.
import "@fontsource-variable/archivo/wght.css";
import "@fontsource-variable/jetbrains-mono/wght.css";
import "@fontsource-variable/martian-mono/wght.css";
// Order is precedence — there is no @layer — so this list is the cascade, and it
// runs from what everything reads to what reads everything: the tokens, the bare
// document, the kit, the frame the kit is arranged in, the rotor, and last the
// pages, which are the only rules allowed to know about a provider or a route.
//
// The kit sits above the frame and the pages so that a page cannot quietly
// outrank a primitive by being longer. Nothing can today — every primitive class
// is prefixed `ui-` and no other sheet spells that prefix — and this order is
// what keeps that true once they start sharing names.
//
// scripts/check-css-order.cjs reads this list to reconstruct the bundle, so a
// sheet added here is a sheet the checks cover.
import "./tokens.css";
import "./base.css";
import "./ui/primitives.css";
import "./shell.css";
import "./rotor.css";
import "./pages.css";
import App from "./App";
import { ConfirmProvider } from "./ConfirmDialog";
import { ErrorBoundary } from "./ErrorBoundary";

const root = document.getElementById("root");
if (!root) {
  // A mismatched index.html would otherwise fail with a bare "properties of
  // null" and no indication of what the page is missing.
  throw new Error('Rotakey console could not start: no element with id "root" in the document.');
}

ReactDOM.createRoot(root).render(
  <React.StrictMode>
    <ErrorBoundary>
      <ConfirmProvider>
        <App />
      </ConfirmProvider>
    </ErrorBoundary>
  </React.StrictMode>
);
