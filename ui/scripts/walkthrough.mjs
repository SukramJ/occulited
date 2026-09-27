// openccu-lite task 274: the screenshots of the fork's docs/walkthrough - every page of the UI,
// German, light theme, taken from the stub server (test/stub/server.mjs) with demo data laid over
// its answers here, so the test suite's data and expectations stay as they are.
//
//   npm run build
//   PORT=8841 node test/stub/server.mjs &
//   env -u WAYLAND_DISPLAY node scripts/walkthrough.mjs http://127.0.0.1:8841 <out-dir> [name ...]
//
// Two layers of demo data: the stub's JSON answers are patched per endpoint (a system with a plain
// name, no warnings, local logins, no ULA), and before every shot the page's text is swept with
// RULES, which mask what must never be on a published image - device serials, SGTINs, host names
// and addresses that are not documentation ones. The images are PNG; the caller compresses them.
import {chromium} from '@playwright/test';
import {execFileSync} from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';

const [base, out, ...only] = process.argv.slice(2);
if (!base || !out) {
    console.error('usage: walkthrough.mjs <stub base URL> <out dir> [shot name ...]');
    process.exit(2);
}
fs.mkdirSync(out, {recursive: true});

const HOST = 'openccu-lite';
let head = 'f0775320000000000000000000000000000000000';
try {
    head = execFileSync('git', ['rev-parse', 'HEAD'], {encoding: 'utf8'}).trim();
} catch {
    /* outside a checkout: the placeholder */
}

// ---- the API layer: string replacements on the JSON text, then patches per endpoint ----
const TEXT = [
    [/"openccu"/g, `"${HOST}"`],
    [/"ccu-vm-1"/g, `"${HOST}"`],
    [/openccu\.home\.arpa/g, `${HOST}.home.arpa`],
    [/CN=openccu,/g, `CN=${HOST},`],
    [/@openccu\b(?!-)/g, `@${HOST}`],
    [/"openccu-(\d{4}-)/g, `"${HOST}-$1`],
];
// the Log page's lines: what a quiet system writes, instead of the stub's numbered samples
const DEMO_LOG = [
    ['info', 'occulited', 'listening on 127.0.0.1:8183'],
    ['info', 'rfd', 'XmlRpc: init from 127.0.0.1:31999, interface id 1007'],
    ['info', 'hmipserver', 'HmIP-RF: 14 devices loaded, key server not needed'],
    ['notice', 'hmipserver', 'Access point HmIP-HAP connected via LAN'],
    ['info', 'addon-mosquitto', 'New client connected from 127.0.0.1:50422 as hm2mqtt (p2, c1, k60)'],
    ['info', 'occulited', 'lite-rpc: SSE stream opened for token home-assistant'],
    ['info', 'addon-hm2mqtt', 'mqtt connected mqtt://127.0.0.1:1883'],
    ['warning', 'rfd', 'Device HM-CC-TC unreachable: no answer to 3 attempts'],
    ['info', 'occulited', 'service messages: 2 active (LOW_BAT, UNREACH)'],
    ['info', 'lighttpd', 'server started (lighttpd/1.4.82)'],
    ['info', 'occulited', 'addons: catalogue refreshed, 2 updates available'],
    ['notice', 'occulited', 'backup: nightly backup written to target NAS (4.7 MB, 12 s)'],
    ['info', 'addon-redmatic', 'Node-RED: Started flows'],
    ['err', 'addon-redmatic', 'ccu-connection: HmIP-RF ping timeout, reconnecting'],
    ['info', 'addon-redmatic', 'ccu-connection: HmIP-RF connected'],
];
/** endpoint path (no query) -> function(json, shot) returning the patched json */
const PATCH = {
    '/api/system/v1/health': (j) => ({...j, version: head, release: '1.0.0'}),
    '/api/system/v1/status': (j) => ({...j, occulited_version: head, version: {...j.version, lite: '1.0.0', version: '3.89.9.20260914'}}),
    '/api/system/v1/system-update': (j) => ({...j, running: {...j.running, lite: '1.0.0', version: '3.89.9.20260914'}}),
    '/api/system/v1/warnings': (j) => ({...j, warnings: []}),
    '/api/auth/v1/config': (j) => ({...j, mode: 'local', running: 'local', restart_required: false, name: '', issuer: '', client_id: '', client_secret_set: false}),
    '/api/system/v1/network': (j) => {
        for (const i of j.network?.interfaces ?? []) {
            i.ipv6 = (i.ipv6 ?? []).filter((a) => a.scope !== 'unique-local');
            i.ipv4 = (i.ipv4 ?? []).map((a) => (a.address === '10.10.0.2' ? {...a, address: '192.0.2.119'} : a));
            if (i.name === 'wlan0') i.ipv4 = [{address: '192.0.2.120', prefix: 24}];
        }
        if (j.network?.ipv6_state) j.network.ipv6_state.dns = ['2001:db8:1::1'];
        return j;
    },
    '/api/system/v1/firewall': (j) => {
        const drop = (o) => {
            if (o && typeof o === 'object') {
                delete o.migration;
                Object.values(o).forEach(drop);
            }
        };
        drop(j);
        return j;
    },
    '/api/system/v1/backup/targets': (j) => ({
        ...j,
        targets: (j.targets ?? []).filter((t) => t.kind !== 'directory').map((t) => (t.kind === 'sftp' ? {...t, name: 'Server im Keller'} : t.kind === 'share' ? {...t, name: 'NAS'} : t)),
    }),
    '/api/system/v1/log': (j) => (Array.isArray(j.lines) && j.lines.some((l) => /^Sample log line/.test(l.message ?? ''))
        ? {...j, lines: j.lines.slice(-DEMO_LOG.length).map((l, i) => {
            const [severity, tag, message] = DEMO_LOG[i % DEMO_LOG.length];
            return {...l, severity, tag, unit: tag, message, area: undefined};
        })}
        : j),
    '/api/system/v1/ssh/keys': () => ({managed: [{type: 'ssh-ed25519', bits: 256, comment: 'anna@laptop', fingerprint: 'SHA256:Q3v8XmL2pR7tN0bK5sW1yE4hJ6cF9gD2aZ8uV3iO5qT'}], other: []}),
    '/api/system/v1/certificate': (j) => (j.current ? {...j, current: {...j.current, issuer: j.current.subject, issuer_cn: HOST, issuer_org: 'HomeMatic', fingerprint: j.current.fingerprint?.replace(/^FD:/, 'A7:')}} : j),
    '/api/system/v1/https': (j) => ({...j, redirect_fqdn_host: HOST, redirect_fqdn_target: `${HOST}.home.arpa`}),
    '/api/system/v1/services': (j) => ({
        ...j,
        services: [
            ...(j.services ?? []).map((s) => (s.kind === 'addon' && s.policy_mode === 'confined' && !s.user ? {...s, user: s.id} : s)),
            {id: 'occu-etc-writable', kind: 'system', running: true, enabled: true, unit_file_state: 'enabled', managed: true, category: 'core', user: 'root'},
        ],
    }),
    '/api/system/v1/backup/schedule': (j) => ({...j, path: '/media/net/nas/openccu-lite', on_userfs: false, real_path: '/media/net/nas/openccu-lite'}),
};

// ---- the page layer: what the sweep masks before each shot (run in the page) ----
const RULES = [
    ['step-ca\\.lan\\.example\\.org', 'ca.example.org'],
    ['lan\\.example\\.org', 'example.org'],
    ['\\bnas\\.(lan|lab)\\b', 'nas.home.arpa'],
    ['Build host( \\(TrueNAS dataset via SSH\\))?', 'Server im Keller'],
    ['TrueNAS', 'NAS'],
    ['Lab CA', 'Heimnetz-CA'],
    ['labLAB[A-Za-z0-9]*', 'Q3v8XmL2pR7tN0bK5sW1yE4hJ6cF9gD2aZ8uV3iO5qT'],
    ['\\blab\\b', 'laptop'],
    ['\\bLab\\b', 'Heimnetz'],
    ['authentik', 'SSO'],
    ['Access point cellar', 'Access Point Keller'],
    ['\\bsebastian\\b', 'anna'],
    // the author's full name stays off the images (the handle names him)
    ['\\bSebastian R\\w+ \\(hobbyquaker\\)', 'hobbyquaker'],
    ['\\bSebastian R\\w+', 'hobbyquaker'],
    ['\\b10\\.10\\.0\\.2\\b', '192.0.2.119'],
    ['\\b192\\.168\\.(0|1)\\.(\\d{1,3})(?![\\d/])', '192.0.2.$2'],
    ['\\b192\\.168\\.0\\.0/24\\b', '192.0.2.0/24'],
    ['1709ADFA5E', '0A1B2C3D4E'],
    ['4118f6a32', '0a1b2c3d4'],
];
function sweep(rules) {
    // a stable fake per original, so one device keeps one serial across the pages
    const hash = (s) => {
        let h = 2166136261;
        for (const c of s) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
        return h;
    };
    const hex = (s, n) => {
        let r = '';
        let h = hash(s);
        while (r.length < n) {
            r += h.toString(16).toUpperCase().padStart(8, '0');
            h = Math.imul(h ^ 0x9e3779b9, 16777619) >>> 0;
        }
        return r.slice(0, n);
    };
    const fixed = rules.map(([re, to]) => [new RegExp(re, 'g'), to]);
    const fn = [
        // SGTINs, plain and in groups of four: masked
        [/\b30[0-9A-F]{2}(-[0-9A-F]{4}){5}\b/gi, () => 'XXXX-XXXX-XXXX-XXXX-XXXX-XXXX'],
        [/\b30[0-9A-F]{22}\b/gi, () => 'XXXXXXXXXXXXXXXXXXXXXXXX'],
        // HmIP device addresses (14 hex digits, 00…): a fake of the same shape
        [/\b00[0-9A-F]{12}\b/g, (m) => `00${hex(m, 12)}`],
        // BidCos serials (three letters, seven digits)
        [/\b([A-Z]EQ)(\d{7})\b/g, (m, p) => p + String(hash(m) % 10000000).padStart(7, '0')],
    ];
    const apply = (s) => {
        let r = s;
        for (const [re, to] of fixed) r = r.replace(re, to);
        for (const [re, f] of fn) r = r.replace(re, f);
        return r;
    };
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walk.nextNode()) nodes.push(walk.currentNode);
    for (const n of nodes) {
        const r = apply(n.nodeValue);
        if (r !== n.nodeValue) n.nodeValue = r;
    }
    for (const el of document.querySelectorAll('input, textarea')) {
        if (el.value) {
            const r = apply(el.value);
            if (r !== el.value) el.value = r;
        }
        if (el.placeholder) el.placeholder = apply(el.placeholder);
    }
    for (const el of document.querySelectorAll('[title]')) el.title = apply(el.title);
}

// ---- the shots ----
const SETUP = {setup_required: true, authenticated: false};
const SIGNED_OUT = {setup_required: false, authenticated: false};
const fresh = {
    '/api/system/v1/firmware': (j) => ({...j, enabled: false}),
    '/api/system/v1/system-update': (j) => ({...PATCH['/api/system/v1/system-update'](j), feed: {...j.feed, enabled: false}}),
    '/api/system/v1/catalog': (j) => ({...j, daily: false}),
};
const longPress = async (p, sel) => {
    const box = await p.locator(sel).first().boundingBox();
    await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
    await p.mouse.down();
    await p.waitForTimeout(1500);
    await p.mouse.up();
};
const SHOTS = [
    {name: '01-einrichtung', path: '/', state: SETUP},
    {name: '02-willkommen', path: '/welcome', patch: fresh},
    {name: '03-anmeldung', path: '/login', state: SIGNED_OUT},
    {name: '04-status', path: '/'},
    {name: '05-bedienung', path: '/app/e/rooms/og', app: true},
    {name: '06-bedienung-kanal', path: '/app/e/rooms/og', app: true, act: (p) => p.locator('[data-app-tile]', {hasText: 'Bad Thermostat'}).click(), height: 900},
    {name: '07-systemmenue', path: '/', act: (p) => p.locator('.ol-systab').click(), height: 900},
    {name: '08-schnittstellen', path: '/system/interfaces'},
    {name: '09-lan-geraete', path: '/system/lan-devices'},
    {name: '10-schluessel', path: '/system/keys'},
    {name: '11-netzwerk', path: '/system/network'},
    {name: '12-firewall', path: '/system/firewall'},
    {name: '13-fernzugriff', path: '/system/remote-access'},
    {name: '14-vertrauensspeicher', path: '/system/trust'},
    {name: '15-zertifikat', path: '/system/certificates'},
    {name: '16-benutzer', path: '/system/users'},
    {name: '17-dienste', path: '/system/services'},
    {name: '18-protokoll', path: '/system/log', height: 900},
    {name: '19-speicher', path: '/system/storage'},
    {name: '20-sicherung', path: '/system/backup'},
    {name: '21-updates', path: '/system/updates'},
    {name: '22-statusleuchte', path: '/system/led'},
    {name: '23-einstellungen', path: '/settings'},
    {name: '24-konto', path: '/account'},
    {name: '25-lizenzen', path: '/licenses'},
    {name: '26-ausschalten', path: '/', act: (p) => p.locator('.ol-powerbtn').click(), height: 900},
    {name: '27-zusatzsoftware', path: '/addons'},
    {name: '28-namen-raeume', path: '/app/e/rooms/og', app: true, act: (p) => p.locator('[data-app-entry="taxonomy"]').click(), height: 900},
    {name: '29-namen-kanal', path: '/app/e/rooms/og', app: true, act: (p) => longPress(p, '[data-app-tile]'), height: 900},
];

const browser = await chromium.launch({args: ['--lang=de-DE']});
for (const shot of SHOTS) {
    if (only.length && !only.includes(shot.name)) continue;
    const ctx = await browser.newContext({viewport: {width: 1280, height: 900}, colorScheme: 'light', locale: 'de-DE', timezoneId: 'Europe/Berlin', serviceWorkers: 'block'});
    await ctx.addInitScript(() => {
        try {
            localStorage.setItem('ol.language', 'de');
            localStorage.setItem('ol.theme', 'light');
        } catch {
            /* no storage: the defaults */
        }
    });
    if (shot.app) await ctx.addCookies([{name: 'stub-app', value: `walk-${shot.name}`, url: base}]);
    await ctx.route('**/api/**', async (route) => {
        const req = route.request();
        const u = new URL(req.url());
        if (req.method() === 'GET' && u.pathname === '/api/auth/v1/state' && shot.state) return route.fulfill({json: shot.state});
        if (req.method() !== 'GET') return route.fallback();
        let response;
        try {
            response = await route.fetch();
        } catch {
            return route.fallback();
        }
        const type = response.headers()['content-type'] ?? '';
        if (!type.includes('json')) return route.fulfill({response});
        let text = await response.text();
        for (const [re, to] of TEXT) text = text.replace(re, to);
        let json;
        try {
            json = JSON.parse(text);
        } catch {
            return route.fulfill({response, body: text});
        }
        const own = shot.patch?.[u.pathname];
        const p = own ?? PATCH[u.pathname];
        if (p) json = p(json, shot);
        return route.fulfill({response, json});
    });
    const page = await ctx.newPage();
    await page.goto(base + shot.path);
    await page.waitForTimeout(1800);
    if (shot.act) {
        try {
            await shot.act(page);
        } catch (e) {
            console.error(`${shot.name}: ${e.message.split('\n')[0]}`);
        }
        await page.waitForTimeout(900);
    }
    let h = shot.height;
    if (!h) {
        h = await page.evaluate(() => {
            const s = document.querySelector('.ol-scrollport');
            const hd = document.querySelector('.ol-header');
            return (s ? s.scrollHeight : document.body.scrollHeight) + (hd ? hd.offsetHeight : 0);
        });
        h = Math.min(Math.max(h, 600), 4000);
        await page.setViewportSize({width: 1280, height: h});
        await page.waitForTimeout(500);
    }
    await page.evaluate(sweep, RULES);
    await page.screenshot({path: path.join(out, `${shot.name}.png`)});
    // WALK_TEXT_DIR: the page's text as shot (inputs included), for the check that nothing slipped through
    if (process.env.WALK_TEXT_DIR) {
        const text = await page.evaluate(() => [document.body.innerText, ...[...document.querySelectorAll('input, textarea')].map((e) => `${e.value} ${e.placeholder ?? ''}`), ...[...document.querySelectorAll('[title]')].map((e) => e.title)].join('\n'));
        fs.mkdirSync(process.env.WALK_TEXT_DIR, {recursive: true});
        fs.writeFileSync(path.join(process.env.WALK_TEXT_DIR, `${shot.name}.txt`), text);
    }
    console.log(shot.name, h);
    await ctx.close();
}
await browser.close();
