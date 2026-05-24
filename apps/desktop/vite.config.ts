import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          if (id.includes("@xterm")) return "vendor-terminal";
          if (id.includes("@tanstack")) return "vendor-virtual";
          if (id.includes("@dnd-kit")) return "vendor-dnd";
          if (id.includes("react-markdown") || id.includes("remark-") || id.includes("rehype-") || id.includes("hast-") || id.includes("mdast-") || id.includes("micromark") || id.includes("unified")) {
            return "vendor-markdown";
          }
          if (id.includes("framer-motion") || id.includes("motion-dom") || id.includes("motion-utils")) return "vendor-motion";
          if (id.includes("lucide-react")) return "vendor-icons";
          if (id.includes("react") || id.includes("react-dom") || id.includes("scheduler")) return "vendor-react";
          return "vendor-misc";
        },
      },
    },
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
  },
});
