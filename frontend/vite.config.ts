import path from "node:path";

import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

export default defineConfig({
  base: "/admin/ui/",
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    proxy: {
      "/admin": {
        target: process.env.VITE_DEV_API_TARGET ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        bypass(req) {
          // Let Vite serve the SPA shell and assets; proxy only JSON/API paths.
          const url = req.url ?? "";
          if (
            url === "/admin" ||
            url === "/admin/" ||
            url.startsWith("/admin/ui/")
          ) {
            return url;
          }
          return undefined;
        },
      },
    },
  },
  build: {
    outDir: "dist",
    sourcemap: false,
    emptyOutDir: true,
  },
});
