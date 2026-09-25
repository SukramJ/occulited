// The App's home-screen icons (task 193, the PWA): the brand blue of the favicon with a white house,
// rendered by Chromium from an SVG - the same route as the wordmark's script, since the toolchain has
// no raster library. The maskable one fills the whole square (Android masks it); the others keep the
// rounded corners. Usage: node scripts/make-icons.mjs  (writes public/icons/*.png)
import {chromium} from 'playwright';
import fs from 'node:fs';

const OUT = new URL('../public/icons/', import.meta.url).pathname;
fs.mkdirSync(OUT, {recursive: true});
const svg = (size, maskable) => `<svg xmlns="http://www.w3.org/2000/svg" width="${size}" height="${size}" viewBox="0 0 100 100">
  <rect width="100" height="100" rx="${maskable ? 0 : 22}" fill="#2a7de1"/>
  <g transform="translate(${maskable ? 30 : 26} ${maskable ? 30 : 26}) scale(${maskable ? 1.66 : 2})" fill="none" stroke="#fff" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
    <path d="M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>
  </g>
</svg>`;
const br = await chromium.launch();
const pg = await (await br.newContext({deviceScaleFactor: 1})).newPage();
for (const [name, size, maskable] of [['icon-192.png', 192, false], ['icon-512.png', 512, false], ['maskable-512.png', 512, true], ['apple-touch-icon.png', 180, true]]) {
    await pg.setViewportSize({width: size, height: size});
    await pg.setContent(`<style>html,body{margin:0;background:transparent}</style>${svg(size, maskable)}`);
    await pg.locator('svg').screenshot({path: OUT + name, omitBackground: true});
}
await br.close();
console.log('icons written to', OUT);
