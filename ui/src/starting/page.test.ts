import {describe, expect, it} from 'vitest';
import html from '../../../deploy/lighttpd/occulite-starting.html?raw';
import table from '../../../internal/bootexpect/defaults.json';

// The generated waiting page as it is committed (npm run build writes it): what lighttpd serves and
// S50lighttpd fills in. The behaviour is tested in the browser (test/e2e/starting-page.spec.ts).
describe('the waiting page lighttpd serves', () => {
    const script = html.slice(html.lastIndexOf('<script>'));

    it('carries the four placeholders S50lighttpd fills in', () => {
        for (const p of ['@HOSTNAME@', '@VERSION@', '@PLATFORM@', '@IP@']) expect(html).toContain(p);
        // and no build marker of the template
        expect(html).not.toMatch(/\{\{[A-Z_]+\}\}/);
    });

    it('loads nothing from anywhere: styles, script and logo are inline', () => {
        expect(html).not.toMatch(/\b(src|href)\s*=\s*["']?(https?:)?\/\//i);
        expect(html).not.toMatch(/<link\b/i);
        expect(html).not.toMatch(/<script[^>]*\bsrc=/i);
        expect(html).not.toMatch(/@import|url\(\s*["']?(https?:)?\/\//i);
        expect(html).toContain('src="data:image/png;base64,');
    });

    it('refreshes itself without JavaScript', () => {
        expect(html).toContain('<noscript><meta http-equiv="refresh" content="10"></noscript>');
    });

    it('its script reads the countdown entry, knows the product defaults and has no placeholder sed would change', () => {
        expect(script).toContain('occulite.reboot');
        expect(script).toContain('/api/system/v1/health');
        expect(script).not.toMatch(/@[A-Z]+@/);
        // the table is bundled as an object literal: every product's name is in it
        for (const product of Object.keys(table.products)) expect(script).toContain(product);
    });

    it('stays small', () => {
        expect(html.length).toBeLessThan(80_000);
    });
});
