import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
import { devAPI } from "./dev-api";

/** public/theme.js puts the operator's theme on <html> before the stylesheets
 *  load, so the first paint is already the right colour. Three constraints meet
 *  here and only one arrangement satisfies all of them: it has to be a classic
 *  script, because a module is deferred and would run after the paint it exists
 *  to fix; it has to be a file rather than an inline block, because the gateway
 *  serves the console under `script-src 'self'` with no nonce; and it cannot sit
 *  in index.html, because Vite warns on every build about a script tag it is not
 *  allowed to bundle. Injecting the tag says the same thing without the warning. */
const prepaintTheme: Plugin = {
  name: "rotakey-prepaint-theme",
  transformIndexHtml() {
    return [{ tag: "script", attrs: { src: "/admin/theme.js" }, injectTo: "head-prepend" }];
  }
};

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const proxyTarget = env.ROTAKEY_DEV_PROXY || "http://localhost:8080";
  // With no gateway to proxy to, every page renders as a login card and layout
  // can only be checked by reading CSS. `npm run dev:mock` serves /api from a
  // typed fixture instead, so the console can actually be looked at. It replaces
  // the proxy rather than sitting in front of it: two things answering the same
  // path is how a fixture ends up shadowing a real backend nobody noticed was
  // running. A mode rather than an env file, because `.env.*` is git-ignored and
  // a shared affordance nobody else can run is not one. Dev only — the plugin
  // has no build hooks.
  const mock = mode === "mock";
  return {
    base: "/admin/",
    plugins: mock ? [react(), prepaintTheme, devAPI()] : [react(), prepaintTheme],
    build: {
      outDir: "dist",
      sourcemap: false,
      emptyOutDir: true
    },
    server: {
      port: 5173,
      proxy: mock
        ? undefined
        : {
            "/api": { target: proxyTarget, changeOrigin: true },
            "/v1": { target: proxyTarget, changeOrigin: true }
          }
    }
  };
});
