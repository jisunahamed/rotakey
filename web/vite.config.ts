import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, ".", "");
  const proxyTarget = env.ROTAKEY_DEV_PROXY || "http://localhost:8080";
  return {
    base: "/admin/",
    plugins: [react()],
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
