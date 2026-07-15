import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  test: {
    environment: "jsdom",
    setupFiles: "./src/test/setup.ts",
    include: ["src/**/*.test.{ts,tsx}"],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary", "lcov"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/main.tsx", "src/types/**", "src/test/**", "src/vite-env.d.ts"],
      thresholds: {
        statements: 60,
        branches: 45,
        functions: 55,
        lines: 64,
      },
    },
  },
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
