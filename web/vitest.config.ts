import { defineConfig } from "vitest/config";
import path from "node:path";

// First Vitest config in the repo. Mirrors the tsconfig path alias so specs can
// import `@/...` the same way application code does. jsdom is required because
// component specs render React trees that touch `document` / `window`.
export default defineConfig({
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "."),
    },
  },
  test: {
    environment: "jsdom",
    globals: false,
    setupFiles: ["./test/setup.ts"],
    include: [
      "**/*.test.ts",
      "**/*.test.tsx",
      "**/*.spec.ts",
      "**/*.spec.tsx",
    ],
    exclude: [
      "**/node_modules/**",
      "**/.next/**",
      "**/.omc/**",
      "**/dist/**",
    ],
  },
});
