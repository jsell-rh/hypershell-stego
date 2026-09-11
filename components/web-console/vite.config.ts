import { reactRouter } from "@react-router/dev/vite";
import { defineConfig } from "vite";

export default defineConfig({
  envDir: false,
  plugins: [reactRouter()],
  build: { sourcemap: false, target: "es2022", assetsInlineLimit: 0 },
  ssr: { noExternal: [/^@patternfly\//] },
});
