import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const backend = process.env.CHAT_BACKEND || 'http://localhost:8080';

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      '/api': backend,
      '/auth': backend,
      '/ws': { target: backend, ws: true },
      '/healthz': backend,
      '/readyz': backend,
    },
  },
});
