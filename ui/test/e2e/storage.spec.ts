import {expect, test, type BrowserContext, type Page} from '@playwright/test';
import {pageWidth, scrollTo} from './scroll';

// task 69: the storage health panel on the Status page - each verdict from the stub (stub-storage),
// the notes where a kind of disk has no counter, the warning that jumps to the panel, the phone
// layout and a pill that is readable in both themes (the desktop-dark project runs these too)

async function open(page: Page, context: BrowserContext, baseURL: string | undefined, variant?: string) {
    if (variant) await context.addCookies([{name: 'stub-storage', value: variant, url: baseURL!}]);
    await page.goto('/');
    const panel = page.locator('[data-panel="storage"]');
    await expect(panel).toBeVisible();
    return panel;
}

test('good: the SD card with its identity, its age, the two notes and no warning', async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL);
    await expect(panel.locator('.ol-verdict')).toHaveText('good');
    await expect(panel.locator('.ol-verdict')).toHaveAttribute('data-verdict', 'good');
    await expect(panel.locator('.ol-card-title')).toHaveText('The storage devices look healthy.');
    const card = panel.locator('[data-device="mmcblk0"]');
    await expect(card.locator('.ol-storage-line')).toHaveText('SD card · SanDisk · SN64G · 63.9 GB');
    await expect(card.locator('.ol-mounts')).toHaveText('/, /boot, /usr/local');
    await expect(card.locator('[data-fact="made"]')).toHaveText('06/2024 (2 years old)');
    // an SD card has no wear counter and no SMART: both rows say so rather than staying empty
    await expect(card.locator('[data-fact="wear"]')).toHaveText('not available on an SD card');
    await expect(card.locator('[data-fact="smart"]')).toHaveText('not available on an SD card');
    await expect(card.locator('[data-fact="errors"]')).toContainText('none in the kernel log since this boot');
    await expect(card.locator('[data-fact="errors"]')).toContainText('no file system errors recorded');
    await expect(card.locator('[data-fact="writes"]')).toContainText('8.2 MB a day (over the last 14 days)');
    await expect(card.locator('[data-fact="writes"]')).toContainText('/usr/local: 50.8 GB written since the file system was made');
    await expect(page.locator('[data-notice="storage"]')).toHaveCount(0);
    // the box reads its own disks: no line about the host
    await expect(panel.locator('[data-storage-host]')).toHaveCount(0);
});

test('watch: the eMMC estimates and reasons, and the warning button jumps to the panel', async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL, 'watch');
    await expect(panel.locator('.ol-verdict')).toHaveText('watch');
    await expect(panel).toHaveClass(/\bwarn\b/);
    await expect(panel.locator('.ol-storage-reasons li')).toHaveText([
        'eMMC DG4016: the eMMC has used 80 % of its reserve blocks.',
        'eMMC DG4016: the file system on /usr/local has recorded 2 errors.',
    ]);
    const emmc = panel.locator('[data-device="mmcblk1"]');
    await expect(emmc.locator('[data-cells="cell type A"] .ol-emmc-range')).toHaveText('10–20 %');
    await expect(emmc.locator('[data-cells="cell type B"] .ol-emmc-range')).toHaveText('0–10 %');
    await expect(emmc.locator('[data-fact="eol"]')).toHaveText('80 % used');
    await expect(emmc.locator('[data-fs="mmcblk1p3"]')).toContainText('/usr/local: 2 file system errors recorded');
    await expect(emmc.locator('.ol-storage-line [data-verdict="watch"]')).toHaveText('watch');
    // the USB SSD beside it: SMART with its facts and the wear bar
    const ssd = panel.locator('[data-device="sda"]');
    await expect(ssd.locator('.ol-storage-line')).toHaveText('USB drive · Samsung · PSSD T7 · 500.1 GB');
    await expect(ssd.locator('[data-fact="smart"]')).toHaveText('passed · 4211 hours powered on · 35 °C · 0 reallocated sectors');
    await expect(ssd.locator('[data-fact="wear"]')).toContainText('3 % of its rated life used');
    await expect(ssd.locator('[data-fact="writes"]')).toContainText('(estimated since this boot)');

    const notice = page.locator('[data-notice="storage"]');
    await expect(notice.locator('.ol-notice-text')).toHaveText('A storage device should be watched. eMMC DG4016: the eMMC has used 80 % of its reserve blocks. eMMC DG4016: the file system on /usr/local has recorded 2 errors.');
    await expect(notice.locator('.ol-notice-text')).not.toContainText(/\bpage\b/);
    await scrollTo(page, 0);
    await notice.getByRole('link', {name: 'Storage health', exact: true}).click();
    await expect(page).toHaveURL(/\/#storage$/);
    await expect(page.locator('#storage')).toBeInViewport();
    await expect(page.getByRole('heading', {level: 1, name: 'Status'})).toBeVisible();
});

test('replace: SMART failed and kernel errors decide it, the worn NVMe is marked on its own line', async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL, 'replace');
    await expect(panel.locator('.ol-verdict')).toHaveText('replace');
    await expect(panel).toHaveClass(/\berr\b/);
    await expect(panel.locator('.ol-card-title')).toHaveText('A storage device should be replaced soon.');
    await expect(panel.locator('.ol-storage-reasons li')).toHaveText([
        "SATA drive KINGSTON SA400S37240G: the drive's own health check (SMART) failed.",
        'SATA drive KINGSTON SA400S37240G: 7 storage errors in the kernel log since this boot.',
    ]);
    const sata = panel.locator('[data-device="sda"]');
    await expect(sata.locator('[data-fact="smart"] .ol-bad')).toHaveText('failed');
    await expect(sata.locator('[data-fact="smart"]')).toContainText('1520 reallocated sectors · 16 pending sectors');
    await expect(sata.locator('[data-fact="errors"] .ol-bad')).toHaveText('7 storage errors in the kernel log since this boot');
    await expect(sata.locator('[data-fact="errors"] .ol-bad')).toHaveAttribute('title', /I\/O error, dev sda/);
    await expect(sata.locator('.ol-wear')).toHaveClass(/\bwarn\b/);
    const nvme = panel.locator('[data-device="nvme0n1"]');
    await expect(nvme.locator('.ol-storage-line [data-verdict="watch"]')).toBeVisible();
    await expect(nvme.locator('[data-fact="wear"]')).toContainText('83 % of its rated life used');
    await expect(page.locator('[data-notice="storage"] .ol-notice-text')).toHaveText(/^A storage device should be replaced soon\. /);
});

test('an old SD card with heavy writes is one to watch, and says why', async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL, 'old-sd');
    await expect(panel.locator('.ol-storage-reasons li')).toHaveText(['SD card GB1QT: the card is 8 years old and receives 620.0 MB a day.']);
    await expect(panel.locator('[data-device="mmcblk0"] [data-fact="made"]')).toHaveText('07/2018 (8 years old)');
});

// task 111: a VM and a container say once that the host monitors the disks, with no SMART row and no
// SMART error, and the verdict stays the one the other facts give
test('a container: the host monitors the disks, and the host disk it lists has no SMART row', async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL, 'container');
    await expect(panel.locator('[data-storage-host]')).toHaveText('Disk health is monitored by the host');
    await expect(panel.locator('.ol-verdict')).toHaveText('good');
    const disk = panel.locator('[data-device="sda"]');
    await expect(disk.locator('.ol-storage-line')).toHaveText('SATA drive · Samsung SSD 870 EVO 1TB · 1.0 TB');
    await expect(disk.locator('[data-fact="smart"], [data-fact="wear"]')).toHaveCount(0);
    await expect(panel).not.toContainText('not available');
    await expect(page.locator('[data-notice="storage"]')).toHaveCount(0);
});

test('the host line in German', async ({page, context, baseURL}) => {
    await page.addInitScript(() => localStorage.setItem('ol.language', 'de'));
    const panel = await open(page, context, baseURL, 'virtual');
    await expect(panel.locator('[data-storage-host]')).toHaveText('Die Datenträger-Gesundheit überwacht der Host');
});

test("a VM's disk says its health is the host's, and no SD card note", async ({page, context, baseURL}) => {
    const panel = await open(page, context, baseURL, 'virtual');
    await expect(panel.locator('[data-storage-host]')).toHaveText('Disk health is monitored by the host');
    const disk = panel.locator('[data-device="sda"]');
    await expect(disk.locator('.ol-storage-line')).toHaveText('Virtual disk · QEMU HARDDISK · 34.4 GB');
    await expect(disk.locator('[data-fact="virtual"]')).toHaveText("virtual disk — its health is the host's");
    await expect(disk.locator('[data-fact="smart"], [data-fact="wear"]')).toHaveCount(0);
    await expect(panel).not.toContainText('not available on an SD card');
});

test('the other warnings bring the storage warning along (stub-warn)', async ({page, context, baseURL}) => {
    await context.addCookies([{name: 'stub-warn', value: '1', url: baseURL!}]);
    await page.goto('/');
    await expect(page.locator('[data-notice="storage"]')).toBeVisible();
    await expect(page.locator('[data-panel="storage"] .ol-verdict')).toHaveText('watch');
});

test('at 360 px the panel fits, the facts stack, and nothing runs out sideways', async ({page, context, baseURL}) => {
    await page.setViewportSize({width: 360, height: 780});
    for (const variant of ['replace', 'watch', 'virtual', 'container']) {
        await context.clearCookies();
        const panel = await open(page, context, baseURL, variant);
        await expect(panel.locator('.ol-storage-dev').first()).toBeVisible();
        expect(await pageWidth(page), variant).toBeLessThanOrEqual(360);
        const box = (await panel.boundingBox())!;
        expect(box.x + box.width, variant).toBeLessThanOrEqual(360);
        // one column: a fact's value sits under its label
        const dt = (await panel.locator('.ol-storage-facts dt').first().boundingBox())!;
        const dd = (await panel.locator('.ol-storage-facts dd').first().boundingBox())!;
        expect(dd.y, variant).toBeGreaterThanOrEqual(dt.y + dt.height - 1);
    }
});

// WCAG contrast of the pill's text against its own background, in whichever theme the project runs
test('the verdict pills are readable in this theme', async ({page, context, baseURL}) => {
    for (const variant of ['good', 'watch', 'replace']) {
        await context.clearCookies();
        const panel = await open(page, context, baseURL, variant);
        const ratio = await panel.locator('.ol-verdict').evaluate((el) => {
            const rgb = (s: string) => (s.match(/[\d.]+/g) ?? []).slice(0, 3).map(Number);
            const lum = ([r, g, b]: number[]) => {
                const c = [r!, g!, b!].map((v) => {
                    const x = v / 255;
                    return x <= 0.03928 ? x / 12.92 : ((x + 0.055) / 1.055) ** 2.4;
                });
                return 0.2126 * c[0]! + 0.7152 * c[1]! + 0.0722 * c[2]!;
            };
            const st = getComputedStyle(el);
            const a = lum(rgb(st.color));
            const b = lum(rgb(st.backgroundColor));
            return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
        });
        expect(ratio, variant).toBeGreaterThanOrEqual(3);
    }
});
