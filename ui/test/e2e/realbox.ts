import {expect, test, type WorkerInfo} from '@playwright/test';
import {spawn, spawnSync, type ChildProcess} from 'node:child_process';
import fs from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

// The real occulited for a spec (B-102's session-restart spec first, the security keys of task 262
// since): built from this checkout with the web interface `npm run build` embedded, run over a
// state directory of its own on a free loopback port. buildBinary goes into a spec's beforeAll,
// removeBinary into its afterAll; a Box is one daemon over one state directory.

let binary = '';
let repo = '';

/** Builds occulited once per worker; skips the spec without Go. */
export async function buildBinary(workerInfo: WorkerInfo): Promise<void> {
    if (binary) return;

    repo = path.resolve(path.dirname(workerInfo.config.configFile ?? path.join(process.cwd(), 'playwright.config.ts')), '..');
    const probe = spawnSync('go', ['version'], {encoding: 'utf8'});
    test.skip(probe.error !== undefined || probe.status !== 0, 'needs Go to build occulited');
    binary = path.join(fs.mkdtempSync(path.join(os.tmpdir(), 'occulited-e2e-')), 'occulited');
    const build = spawnSync('go', ['build', '-o', binary, './cmd/occulited'], {cwd: repo, encoding: 'utf8'});
    expect(build.status, `go build: ${build.stderr}`).toBe(0);
}

/** Removes the built binary (a spec's afterAll). */
export function removeBinary(): void {
    if (binary) fs.rmSync(path.dirname(binary), {recursive: true, force: true});
    binary = '';
}

export async function freePort(): Promise<number> {
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
export class Box {
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

