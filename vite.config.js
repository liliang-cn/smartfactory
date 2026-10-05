import { defineConfig } from 'vite';

export default defineConfig({
  server: { port: 4719, host: true },
  build: { chunkSizeWarningLimit: 900 },
});
