import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
		environment: "jsdom",
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
		all: true,
		include: ["src/**/*.{ts,tsx}"],
		exclude: ["src/main.tsx"],
      thresholds: { lines: 80, statements: 80 }
    }
  }
});
