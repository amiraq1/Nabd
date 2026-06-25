#!/usr/bin/env node
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
//  NABD_OS Build Script
//  Bundles all source into a single executable .mjs
//  Custom resolver plugin bypasses Yarn PnP entirely
// ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

import { build } from 'esbuild';
import { chmod, readFile, writeFile } from 'node:fs/promises';
import { builtinModules } from 'node:module';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const __dirname = dirname(fileURLToPath(import.meta.url));
const ROOT = join(__dirname, '..');
const ENTRY = join(ROOT, 'src', 'index.tsx');
const OUTFILE = join(ROOT, 'dist', 'nabd.mjs');
const LOCAL_MODULES = join(ROOT, 'node_modules');

// All Node.js builtins (with and without node: prefix)
const BUILTINS = new Set([
  ...builtinModules,
  ...builtinModules.map(m => `node:${m}`),
]);

// createRequire anchored to our local project
const localRequire = createRequire(join(ROOT, 'package.json'));

// Plugin: resolve all bare specifiers via local node_modules,
// but skip Node builtins (they stay external).
const localResolverPlugin = {
  name: 'local-node-modules',
  setup(b) {
    // Catch ALL bare specifiers — from src AND from node_modules
    b.onResolve({ filter: /^[^./]/ }, (args) => {
      // Node builtins → external
      if (BUILTINS.has(args.path)) {
        return { path: args.path, external: true };
      }

      // react-devtools-core is an optional ink dep — not needed, map to mock
      if (args.path === 'react-devtools-core') {
        return { path: join(ROOT, 'node_modules', 'react-devtools-core-mock.js') };
      }

      // Try resolving from the importer's directory first (for nested deps),
      // then fall back to the project root
      const resolvers = [];
      if (args.resolveDir) {
        resolvers.push(createRequire(join(args.resolveDir, '_placeholder.js')));
      }
      resolvers.push(localRequire);

      for (const req of resolvers) {
        try {
          const resolved = req.resolve(args.path);
          return { path: resolved };
        } catch (err) {
          // ERR_PACKAGE_PATH_NOT_EXPORTED happens for ESM-only packages like @alcalzone/ansi-tokenize
          if (err.code === 'ERR_PACKAGE_PATH_NOT_EXPORTED' && args.path === '@alcalzone/ansi-tokenize') {
             return { path: join(LOCAL_MODULES, '@alcalzone/ansi-tokenize/build/index.js') };
          }
          // try next resolver
        }
      }

      // Fall through to default esbuild resolution
      return null;
    });
  },
};

async function bundle() {
  console.log('⚡ Bundling NABD_OS...');

  await build({
    entryPoints: [ENTRY],
    bundle: true,
    minify: true,
    platform: 'node',
    target: 'node20',
    format: 'esm',
    outfile: OUTFILE,
    absWorkingDir: ROOT,
    plugins: [localResolverPlugin],
    banner: {
      js: `import { createRequire as __createRequire } from "module";
const require = __createRequire(import.meta.url);`,
    },
    resolveExtensions: ['.tsx', '.ts', '.js', '.mjs'],
    jsx: 'automatic',
    logLevel: 'info',
  });

  // Make executable
  await chmod(OUTFILE, 0o755);

  const stats = await import('node:fs/promises').then(fs => fs.stat(OUTFILE));
  const sizeKB = Math.round(stats.size / 1024);
  console.log(`✓ Built: dist/nabd.mjs (${sizeKB} KB)`);
  console.log('');
  console.log('Install globally:');
  console.log('  npm link');
  console.log('  # — or —');
  console.log('  cp dist/nabd.mjs ~/.local/bin/nabd');
}

bundle().catch((err) => {
  console.error('✖ Build failed:', err.message);
  process.exit(1);
});
