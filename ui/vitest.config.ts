import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

// Vitest config kept separate from vite.config.ts so the production
// bundle build is unaffected by test settings.
export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./src/test/setup.ts'],
    css: false,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'html'],
    },
  },
});
