import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => ({
  plugins: [react()],
  define: { 'process.env.NODE_ENV': JSON.stringify('production') },
  build: {
    outDir: mode === 'csp' ? '.csp' : '../cmd/infrafile/web/dist',
    emptyOutDir: true,
    sourcemap: false,
    cssCodeSplit: false,
    lib: {
      entry: mode === 'csp' ? 'tests/csp.tsx' : 'src/main.tsx',
      formats: ['es'],
      fileName: () => 'app.js',
      cssFileName: 'app',
    },
  },
}));
