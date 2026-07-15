import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  server: {
    port: 15173,
    strictPort: true,
    proxy: {
      "/api": "http://127.0.0.1:18080",
      "/backend-runtime": {
        target: "http://127.0.0.1:18080",
        rewrite: (path) => path.replace(/^\/backend-runtime/, ""),
      },
      "/worker-runtime": {
        target: "http://127.0.0.1:18081",
        rewrite: (path) => path.replace(/^\/worker-runtime/, ""),
      },
      "/nats-runtime": {
        target: "http://127.0.0.1:18222",
        rewrite: (path) => path.replace(/^\/nats-runtime/, ""),
      },
    },
  },
});
