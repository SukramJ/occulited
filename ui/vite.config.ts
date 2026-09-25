import {defineConfig} from 'vite';
import {svelte} from '@sveltejs/vite-plugin-svelte';
import {BUNDLE_LICENSES, bundleLicenses} from './scripts/bundle-licenses';
import {createHash} from 'node:crypto';
import {readFileSync} from 'node:fs';
import type {Plugin} from 'vite';

// task 193: the service worker is emitted with the build's version - a hash over the bundle's file
// names, which change with every change of the code - so a new build drops the old shell cache
function serviceWorker(): Plugin {
    return {
        name: 'ol-service-worker',
        generateBundle(_, bundle) {
            const version = createHash('sha256').update(Object.keys(bundle).sort().join('\n')).digest('hex').slice(0, 12);
            this.emitFile({type: 'asset', fileName: 'sw.js', source: readFileSync(new URL('./src/sw.js', import.meta.url), 'utf8').replaceAll('__VERSION__', version)});
        },
    };
}

// Built into the Go binary via internal/ui (embed.FS). In development, Vite serves the UI and
// proxies the API to a running occulited (`go run ./cmd/occulited --root <fake tree> --listen 127.0.0.1:8183`).
export default defineConfig({
    // task 179: the npm packages the bundle ships, for the image's SBOM (internal/ui/bundle-licenses.json)
    plugins: [svelte(), bundleLicenses(BUNDLE_LICENSES), serviceWorker()],
    build: {outDir: '../internal/ui/dist', emptyOutDir: false, sourcemap: false, target: 'es2022'},
    server: {port: 5174, proxy: {'/api': {target: 'http://127.0.0.1:8183', changeOrigin: false}, '/addons': 'http://127.0.0.1:8183'}},
});
