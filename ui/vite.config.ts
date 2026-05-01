import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The SPA is served by longue-vue under /ui/ in production (see ui/embed.go);
// `base` must match that prefix so asset URLs resolve correctly both in dev
// and in the embedded bundle. `server.proxy` forwards API calls during
// `npm run dev` to a local longue-vue on :8080 so the SPA can be developed with
// hot reload without wiring a second auth path for CORS.
export default defineConfig({
  base: '/ui/',
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    port: 5173,
    proxy: {
      '/v1': 'http://localhost:8080',
      '/healthz': 'http://localhost:8080',
      '/readyz': 'http://localhost:8080',
      '/metrics': 'http://localhost:8080',
    },
  },
});
