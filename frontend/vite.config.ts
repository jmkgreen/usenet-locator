import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  // Keep the built SPA portable when the reverse proxy mounts it below a
  // path prefix (for example, /usenet-locator/).
  base: "./",
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
      "/readyz": "http://localhost:8080"
    }
  }
});
