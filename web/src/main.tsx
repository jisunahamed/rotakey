import React from "react";
import ReactDOM from "react-dom/client";
// Three faces, three jobs: Archivo for language, JetBrains Mono for data that has
// to align in a column, Martian Mono for the uppercase micro-labels only. The
// variable builds carry every weight the console asks for in one file each, and
// each subset is its own @font-face, so only latin is fetched.
import "@fontsource-variable/archivo/wght.css";
import "@fontsource-variable/jetbrains-mono/wght.css";
import "@fontsource-variable/martian-mono/wght.css";
import "./tokens.css";
import "./styles.css";
import "./console.css";
import App from "./App";
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
      <App />
    </ErrorBoundary>
  </React.StrictMode>
);
