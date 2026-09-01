import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

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
  return {
    base: "/admin/",
    plugins: [react(), prepaintTheme],
    build: {
      outDir: "dist",
      sourcemap: false,
      emptyOutDir: true
    },
    server: {
      port: 5173,
      proxy: {
        "/api": { target: proxyTarget, changeOrigin: true },
        "/v1": { target: proxyTarget, changeOrigin: true }
      }
    }
  };
});
