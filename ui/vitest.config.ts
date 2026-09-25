import {defineConfig} from 'vitest/config';

// The unit tests of the pure modules under src/lib (metaTree.ts, carried over from
// homematic-manager with its tests): node, no DOM, no Svelte plugin needed.
// `css.include` lets app.css through: Vitest blanks every CSS import by default, `?raw` ones too,
// and theme.test.ts reads the theme tokens out of it (task 53).
export default defineConfig({
    test: {include: ['src/**/*.test.ts'], environment: 'node', css: {include: [/app\.css/]}},
});
