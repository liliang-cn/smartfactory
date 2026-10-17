import path from 'node:path';
import { defineConfig, type Plugin } from 'vite';
import react from '@vitejs/plugin-react';
import tailwindcss from '@tailwindcss/vite';

/**
 * The console is its own page: /admin and everything under it get admin.html; and so is the phone
 * page for forms filled in by hand, /check, and the waiter's page for the tables, /waiter (the server does the same).
 */
const consolePage: Plugin = {
  name: 'sf-console',
  configureServer(server) {
    server.middlewares.use((req, _res, next) => {
      const query = req.url?.includes('?') ? req.url.slice(req.url.indexOf('?')) : '';
      if (req.url && /^\/admin(\/[^.]*)?(\?.*)?$/.test(req.url)) req.url = `/admin.html${query}`;
      else if (req.url && /^\/check(\?.*)?$/.test(req.url)) req.url = `/check.html${query}`;
      else if (req.url && /^\/waiter(\?.*)?$/.test(req.url)) req.url = `/waiter.html${query}`;
      next();
    });
  },
};

export default defineConfig({
  plugins: [react(), tailwindcss(), consolePage],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  server: {
    port: 4719,
    host: true,
    // `go run ./cmd/smartfactory -demo` in server/ serves the live feed and MCP on :4720
    proxy: { '/api': { target: 'http://127.0.0.1:4720', ws: true }, '/mcp': 'http://127.0.0.1:4720' },
  },
  build: {
    chunkSizeWarningLimit: 1600,
    rollupOptions: { input: { main: path.resolve(import.meta.dirname, 'index.html'), admin: path.resolve(import.meta.dirname, 'admin.html'), check: path.resolve(import.meta.dirname, 'check.html'), waiter: path.resolve(import.meta.dirname, 'waiter.html') } },
  },
});
