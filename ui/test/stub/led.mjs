// Task 95: the status LED as occulited answers it, for the stub. The configuration, the overrides,
// locate and the preview live per browser (cookie stub-led=<id>), so the three Playwright projects
// never see each other's changes. stub-led-hw=none is a box without a status LED (a Pi 4 with an
// HmIP-RFUSB), stub-led-active=<id,id> plants active states.

const COLORS = ['red', 'green', 'blue', 'yellow', 'cyan', 'magenta', 'white', 'off'];
const PATTERNS = ['solid', 'slow', 'fast', 'flash', 'double', 'alternate'];
const FIXED = ['shutdown', 'booting', 'preview', 'locate'];
const ERROR_STATES = ['radio-down', 'service-failed', 'storage-replace'];
const WARNING_IDS = ['rega', 'arch', 'meta', 'unclean', 'backup-target', 'backup-userfs', 'certificate', 'storage', 'addon-ownership', 'security-key'];

function defaults() {
    return {
        enabled: true,
        normal: {color: 'blue', pattern: 'solid'},
        night: {enabled: false, from: '22:00', to: '06:30', show: 'errors'},
        states: [
            {id: 'radio-down', enabled: true, color: 'red', pattern: 'solid'},
            {id: 'no-network', enabled: true, color: 'yellow', pattern: 'fast'},
            {id: 'service-failed', enabled: true, color: 'red', pattern: 'slow'},
            {id: 'storage-replace', enabled: true, color: 'red', pattern: 'double'},
            {id: 'interfaces-starting', enabled: true, color: 'magenta', pattern: 'double', over_normal: true},
            {id: 'external', enabled: true},
            {id: 'status-warning', enabled: true, color: 'yellow', pattern: 'slow'},
            {id: 'system-update', enabled: true, color: 'cyan', pattern: 'slow'},
            {id: 'addon-update', enabled: false, color: 'cyan', pattern: 'double'},
            {id: 'no-internet', enabled: false, color: 'blue', pattern: 'fast'},
        ],
        warnings_off: [],
        addon_units: false,
        locate: {color: 'white', pattern: 'fast', duration_s: 300},
        external: {max_duration_s: 3600, allow_until_cleared: true},
        pwr_error_light: false,
    };
}

const boxes = new Map();
function boxOf(jar) {
    const id = jar['stub-led'] ?? '';
    if (!boxes.has(id)) boxes.set(id, {config: defaults(), overrides: [], locate: null, preview: null, since: new Date(Date.now() - 3600 * 1000).toISOString()});
    return boxes.get(id);
}

function send(res, body, status = 200) {
    res.writeHead(status, {'Content-Type': 'application/json'});
    res.end(JSON.stringify(body));
}

function readBody(req, done) {
    let body = '';
    req.on('data', (c) => (body += c));
    req.on('end', () => {
        try {
            done(JSON.parse(body || '{}'));
        } catch {
            done(null);
        }
    });
}

function available(jar) {
    return jar['stub-led-hw'] !== 'none';
}

function view(jar, box) {
    const on = available(jar);
    return {
        available: on,
        ...(on ? {} : {reason: 'no-module'}),
        hardware: {kind: on ? 'rpi-rf-mod' : '', leds: on ? ['red', 'green', 'blue'] : [], colors: COLORS, patterns: PATTERNS, brightness: false},
        pwr: !on,
        config: box.config,
        defaults: defaults(),
        fixed: FIXED,
        error_states: ERROR_STATES,
        warning_ids: WARNING_IDS,
        // task 95 (D-67): the hosts the no-internet check connects to; stub-led-internet=none has none
        internet_hosts: jar['stub-led-internet'] === 'none' ? [] : ['catalogue.example.org'],
    };
}

// task 134: a blink over the normal colour, as occulited's resolver decides it (night aside)
function overNormal(look, config) {
    const bg = config.normal.color;
    if (!look.over_normal || !['slow', 'fast', 'flash', 'double'].includes(look.pattern) || !config.enabled || bg === 'off' || bg === look.color) return look.over_normal ? {over_normal: true} : {};
    return {over_normal: true, background: bg};
}

function state(jar, box) {
    const now = Date.now();
    if (box.locate && Date.parse(box.locate.until) <= now) box.locate = null;
    if (box.preview && Date.parse(box.preview.until) <= now) box.preview = null;
    const planted = (jar['stub-led-active'] ?? '').split(',').filter(Boolean);
    const active = planted.map((id) => ({id, since: box.since, ...(id === 'service-failed' ? {detail: 'ssdpd'} : id === 'radio-down' ? {detail: 'rfd'} : {})}));
    let shown = {...box.config.normal, source: 'normal', id: 'normal', since: box.since};
    const row = box.config.states.find((s) => s.enabled && planted.includes(s.id));
    if (box.preview) shown = {...box.preview, ...overNormal(box.preview, box.config), source: 'preview', id: 'preview', since: new Date(now).toISOString()};
    else if (box.locate) shown = {color: box.locate.color, pattern: box.locate.pattern, source: 'locate', id: 'locate', since: new Date(now).toISOString()};
    else if (!box.config.enabled) shown = {color: 'off', pattern: 'solid', source: 'off', id: 'off', since: box.since};
    else if (row) shown = {color: row.color, pattern: row.pattern, ...(row.color2 ? {color2: row.color2} : {}), ...overNormal(row, box.config), source: 'state', id: row.id, since: box.since, ...(active.find((a) => a.id === row.id)?.detail ? {detail: active.find((a) => a.id === row.id).detail} : {})};
    else if (box.overrides.length) shown = {...box.overrides[0], source: 'override', since: box.overrides[0].since};
    return {
        available: available(jar),
        ...(available(jar) ? {} : {reason: 'no-module'}),
        shown,
        active,
        overrides: box.overrides,
        ...(box.locate ? {locate: box.locate} : {}),
        ...(box.preview ? {preview: box.preview} : {}),
        night: false,
        booting: false,
        conflict: jar['stub-led-conflict'] === '1',
        ...(available(jar) ? {} : {pwr: {enabled: box.config.pwr_error_light, error: false}}),
        capabilities: {colors: COLORS, patterns: PATTERNS, brightness: false},
    };
}

/** Answers the /api/system/v1/led routes; false for every other path. */
export function ledRoute(req, u, res, jar) {
    const p = u.pathname;
    if (p !== '/api/system/v1/led' && !p.startsWith('/api/system/v1/led/') && p !== '/api/system/v1/led/overrides') return false;
    const box = boxOf(jar);
    const unsupported = () => send(res, {error: 'unsupported', message: 'this system has no status LED'}, 501);
    const key = `${req.method} ${p}`;
    switch (key) {
        case 'GET /api/system/v1/led':
            send(res, view(jar, box));
            return true;
        case 'PUT /api/system/v1/led':
            readBody(req, (b) => {
                const known = defaults().states.map((s) => s.id);
                if (!b || !Array.isArray(b.states) || b.states.some((s) => !known.includes(s.id))) return send(res, {error: 'invalid', message: 'invalid: unknown state'}, 422);
                box.config = b;
                send(res, view(jar, box));
            });
            return true;
        case 'GET /api/system/v1/led/state':
            send(res, state(jar, box));
            return true;
        case 'POST /api/system/v1/led/locate':
            req.resume();
            req.on('end', () => {
                if (!available(jar)) return unsupported();
                box.locate = {color: box.config.locate.color, pattern: box.config.locate.pattern, until: new Date(Date.now() + box.config.locate.duration_s * 1000).toISOString()};
                send(res, state(jar, box));
            });
            return true;
        case 'DELETE /api/system/v1/led/locate':
            req.resume();
            req.on('end', () => {
                box.locate = null;
                send(res, state(jar, box));
            });
            return true;
        case 'POST /api/system/v1/led/preview':
            readBody(req, (b) => {
                if (!available(jar)) return unsupported();
                if (!b || !COLORS.includes(b.color)) return send(res, {error: 'invalid', message: 'invalid: colour'}, 422);
                box.preview = {color: b.color, pattern: b.pattern || 'solid', ...(b.color2 ? {color2: b.color2} : {}), ...(b.over_normal ? {over_normal: true} : {}), until: new Date(Date.now() + 10_000).toISOString()};
                send(res, state(jar, box));
            });
            return true;
        case 'POST /api/system/v1/led/override':
            readBody(req, (b) => {
                if (!available(jar)) return unsupported();
                const o = {id: b?.id || 'admin', color: b?.color ?? 'green', pattern: b?.pattern || 'solid', priority: b?.priority || 'normal', night: b?.night || 'respect', by: 'admin', since: new Date().toISOString()};
                box.overrides = [o, ...box.overrides.filter((x) => x.id !== o.id)];
                send(res, {...state(jar, box), override: o});
            });
            return true;
        case 'DELETE /api/system/v1/led/overrides':
            req.resume();
            req.on('end', () => {
                box.overrides = [];
                send(res, state(jar, box));
            });
            return true;
    }
    return false;
}
