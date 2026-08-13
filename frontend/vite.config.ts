import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

const backend = process.env.CHAT_BACKEND || 'http://127.0.0.1:8081';
const port = Number(process.env.CHAT_FRONTEND_PORT || 8080);

export default defineConfig({
  plugins: [react()],
  server: {
    host: '0.0.0.0',
    port,
    strictPort: true,
    allowedHosts: ['chat.lsong.org'],
    proxy: {
      '/api': backend,
      '/auth': backend,
      '/realtime': { target: backend, ws: true },
      '/healthz': backend,
      '/readyz': backend,
    },
  },
});
