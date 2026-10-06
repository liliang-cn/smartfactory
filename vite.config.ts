import path from 'node:path';
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  server: {
    port: 4719,
    host: true,
    // `go run ./server/cmd/smartfactory` serves the live feed and MCP on :4720
    proxy: { '/api': { target: 'http://127.0.0.1:4720', ws: true }, '/mcp': 'http://127.0.0.1:4720' },
  },
  build: { chunkSizeWarningLimit: 1600 },
});
