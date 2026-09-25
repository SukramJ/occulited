// Builds lighttpd's waiting page, deploy/lighttpd/occulite-starting.html: one self-contained file
// from src/starting/template.html, the bundled src/starting/page.ts (with the countdown code the web
// interface uses, src/lib/bootbar.ts, and the product defaults of internal/bootexpect/defaults.json)
// and the logo as data URIs. No external resource: lighttpd serves it while occulited is down, and
// the fork's S50lighttpd fills in its placeholders with sed. Run by `npm run build`; the output is
// committed, so the image build needs no Node.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {build} from 'vite';

const UI = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const OUT = path.resolve(UI, '../deploy/lighttpd/occulite-starting.html');
const PLACEHOLDERS = ['@HOSTNAME@', '@VERSION@', '@PLATFORM@', '@IP@'];

const result = await build({
    configFile: false,
    root: UI,
    logLevel: 'warn',
    publicDir: false,
    build: {
        write: false,
        minify: true,
        target: 'es2020',
        reportCompressedSize: false,
        lib: {entry: path.join(UI, 'src/starting/page.ts'), formats: ['iife'], name: 'occuliteStarting', fileName: () => 'page.js'},
    },
});
const chunk = (Array.isArray(result) ? result : [result]).flatMap((r) => r.output).find((o) => o.type === 'chunk');
if (!chunk) throw new Error('starting page: no script was built');
// a literal </script> in the code would end the element early
const script = chunk.code.replace(/<\/(script)/gi, '<\\/$1').trim();
if (/@[A-Z]+@/.test(script)) throw new Error('starting page: the script contains a placeholder that sed would replace');

const png = (name) => `data:image/png;base64,${fs.readFileSync(path.join(UI, 'src/assets', name)).toString('base64')}`;
// split/join, not replace: the minified code may contain $ sequences replace() would interpret
const fill = (html, marker, value) => html.split(marker).join(value);
let html = fs.readFileSync(path.join(UI, 'src/starting/template.html'), 'utf8');
html = fill(html, '{{LOGO_LIGHT}}', png('openccu-lite-light.png'));
html = fill(html, '{{LOGO_DARK}}', png('openccu-lite-dark.png'));
html = fill(html, '{{SCRIPT}}', script);
for (const p of PLACEHOLDERS) if (!html.includes(p)) throw new Error(`starting page: ${p} is missing`);
if (/\{\{[A-Z_]+\}\}/.test(html)) throw new Error('starting page: a build marker was left');

fs.writeFileSync(OUT, html);
console.log(`starting page: ${path.relative(path.resolve(UI, '..'), OUT)}, ${Math.round(html.length / 1024)} KiB`);
