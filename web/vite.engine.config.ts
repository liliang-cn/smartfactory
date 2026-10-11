// Builds src/server/engine.ts into one plain script for the Go server's embedded JS engine.
import path from 'node:path';
import { defineConfig } from 'vite';

export default defineConfig({
  resolve: { alias: { '@': path.resolve(import.meta.dirname, 'src') } },
  build: {
    outDir: path.resolve(import.meta.dirname, '../server/internal/engine/js'),
    emptyOutDir: false,
    // goja implements ES5.1 plus most of ES2015+; let esbuild lower the rest.
    target: 'es2017',
    minify: false,
    lib: { entry: path.resolve(import.meta.dirname, 'src/server/engine.ts'), formats: ['iife'], name: 'SFEngine', fileName: () => 'engine.js' },
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
});
