import {expect, test, type Page} from '@playwright/test';
import {spawn, spawnSync, type ChildProcess} from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

// B-102 (D-67): a login survives a restart of occulited and a reboot, and so does a logout. Unlike
// the other specs this one runs the real occulited - built from this checkout with the web
// interface `npm run build` embedded - over a state directory of its own, and restarts it the way
// a box does: stopped (SIGTERM) or killed (a crash), and started again over the same state with the
// tmpfs session mirror emptied as a reboot empties it.

let binary = '';
let repo = '';

test.beforeAll(async ({}, workerInfo) => {
    repo = path.resolve(path.dirname(workerInfo.config.configFile ?? path.join(process.cwd(), 'playwright.config.ts')), '..');
    const probe = spawnSync('go', ['version'], {encoding: 'utf8'});
    test.skip(probe.error !== undefined || probe.status !== 0, 'needs Go to build occulited');
    binary = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'occulited-e2e-')), 'occulited');
    const build = spawnSync('go', ['build', '-o', binary, './cmd/occulited'], {cwd: repo, encoding: 'utf8'});
    expect(build.status, `go build: ${build.stderr}`).toBe(0);
});

test.afterAll(() => {
    if (binary) fs.rmSync(path.dirname(binary), {recursive: true, force: true});
});

async function freePort(): Promise<number> {
    return new Promise((resolve, reject) => {
        const srv = net.createServer();
        srv.on('error', reject);
        srv.listen(0, '127.0.0.1', () => {
            const port = (srv.address() as net.AddressInfo).port;
            srv.close(() => resolve(port));
        });
    });
}

/** One box: its state directory, its tmpfs mirror, and the occulited running over them. */
class Box {
    dir = fs.mkdtempSync(path.join(os.tmpdir(), 'occulited-box-'));
    state = path.join(this.dir, 'state');
    mirror = path.join(this.dir, 'run');
    port = 0;
    proc: ChildProcess | null = null;
    log = '';

    get url() {
        return `http://127.0.0.1:${this.port}`;
    }

    async start() {
        if (!this.port) this.port = await freePort();
        fs.mkdirSync(path.join(this.dir, 'root'), {recursive: true});
        fs.writeFileSync(path.join(this.dir, 'occulited.json'), JSON.stringify({listen: `127.0.0.1:${this.port}`, state_dir: this.state}));
        const proc = spawn(binary, ['--config', path.join(this.dir, 'occulited.json'), '--root', path.join(this.dir, 'root'), '--session-dir', this.mirror, '--log', 'stderr'], {stdio: ['ignore', 'pipe', 'pipe']});
        proc.stdout!.on('data', (b) => (this.log += b));
        proc.stderr!.on('data', (b) => (this.log += b));
        this.proc = proc;
        for (let i = 0; i < 150; i++) {
            if (proc.exitCode !== null) throw new Error(`occulited exited: ${this.log}`);
            try {
                if ((await fetch(`${this.url}/api/system/v1/health`)).ok) return;
            } catch {
                // not listening yet
            }
            await new Promise((r) => setTimeout(r, 100));
        }
        throw new Error(`occulited did not answer: ${this.log}`);
    }

    async stop(signal: 'SIGTERM' | 'SIGKILL') {
        const proc = this.proc;
        if (!proc || proc.exitCode !== null) return;
        const exited = new Promise((r) => proc.once('exit', r));
        proc.kill(signal);
        await exited;
        this.proc = null;
    }

    /** A reboot: occulited goes down, the tmpfs mirror is gone, occulited starts over the same state. */
    async reboot(signal: 'SIGTERM' | 'SIGKILL') {
        await this.stop(signal);
        fs.rmSync(this.mirror, {recursive: true, force: true});
        await this.start();
    }

    /** Every file under the box (state directory and mirror) whose name or content holds needle. */
    holding(needle: string): string[] {
        const out: string[] = [];
        const walk = (d: string) => {
            for (const e of fs.readdirSync(d, {withFileTypes: true})) {
                const p = path.join(d, e.name);
                if (p.includes(needle)) out.push(p);
                if (e.isDirectory()) walk(p);
                else if (e.isFile() && fs.readFileSync(p).includes(needle)) out.push(p);
            }
        };
        walk(this.dir);
        return out;
    }

    async authenticated(sid: string): Promise<boolean> {
        const r = await fetch(`${this.url}/api/auth/v1/state`, {headers: {Cookie: `occulite_session=${sid}`}});
        return (await r.json()).authenticated === true;
    }
}

let box: Box;

test.beforeEach(async () => {
    box = new Box();
    await box.start();
    const setup = await fetch(`${box.url}/api/auth/v1/setup`, {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({username: 'admin', password: 'restart-pass-1'})});
    const body = await setup.text();
    expect(setup.status, body).toBe(200);
    // the setup logs in too; that session ends, so the browser's is the only one
    const out = await fetch(`${box.url}/api/auth/v1/logout`, {method: 'POST', headers: {Cookie: `occulite_session=${JSON.parse(body).sid}`}});
    expect(out.status).toBe(200);
});

test.afterEach(async () => {
    await box.stop('SIGKILL');
    fs.rmSync(box.dir, {recursive: true, force: true});
});

async function login(page: Page) {
    await page.goto(`${box.url}/`);
    const form = page.locator('form.ol-card');
    await expect(form).toBeVisible();
    await page.locator('input:not([type=password])').first().fill('admin');
    await page.locator('input[type=password]').fill('restart-pass-1');
    await page.getByRole('button', {name: 'Login'}).click();
    await expectLoggedIn(page);
    const cookie = (await page.context().cookies(box.url)).find((c) => c.name === 'occulite_session');
    expect(cookie?.value).toMatch(/^[A-Z2-7]{26}$/); // task 125: 26 characters of base32
    return cookie!.value;
}

async function expectLoggedIn(page: Page) {
    await expect(page.locator('header.ol-header a[href="/account"]')).toBeAttached();
    await expect(page.locator('form.ol-card')).toHaveCount(0);
}

test('a login survives a crash and a reboot of occulited', async ({page}) => {
    test.setTimeout(90_000);
    const sid = await login(page);

    // killed, not stopped: the login was written when it happened, not at a shutdown
    await box.reboot('SIGKILL');
    await page.reload();
    await expectLoggedIn(page);
    expect(await box.authenticated(sid)).toBe(true);

    // and a clean restart on top
    await box.reboot('SIGTERM');
    await page.reload();
    await expectLoggedIn(page);

    // neither the state directory nor the mirror holds the session id
    expect(box.holding(sid)).toEqual([]);
    expect(fs.readdirSync(box.mirror)).toHaveLength(1);
});

test('a logout survives a reboot of occulited', async ({page}) => {
    test.setTimeout(90_000);
    const sid = await login(page);
    await box.reboot('SIGTERM');
    await page.reload();
    await expectLoggedIn(page);

    await page.goto(`${box.url}/account`);
    await page.getByRole('button', {name: 'Logout'}).click();
    await expect(page.locator('form.ol-card')).toBeVisible();

    await box.reboot('SIGKILL');
    await page.reload();
    await expect(page.locator('form.ol-card')).toBeVisible();
    // the id itself, sent again as a cookie, names no session either
    expect(await box.authenticated(sid)).toBe(false);
    expect(fs.readdirSync(box.mirror)).toHaveLength(0);
    expect(box.holding(sid)).toEqual([]);
});
