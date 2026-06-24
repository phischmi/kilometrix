import { defineConfig } from "vite";
import tailwindcss from "@tailwindcss/vite";

// Wails liefert die gebauten Assets über seinen eigenen Server aus; relative Basis.
export default defineConfig({
  plugins: [tailwindcss()],
  base: "./",
  build: { outDir: "dist", emptyOutDir: true },
});
