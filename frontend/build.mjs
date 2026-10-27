import { build } from 'esbuild';
import { readdir, readFile, writeFile } from 'node:fs/promises';
import { join } from 'node:path';

await build({ entryPoints: ['frontend/notes-editor.js'], bundle: true, minify: true, format: 'iife', target: ['es2022'], outfile: 'web/notes-editor.bundle.js', legalComments: 'inline' });
const notices = [];
async function collect(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.name.startsWith('.')) continue;
    const path = join(directory, entry.name);
    if (entry.name.startsWith('@')) {
      await collect(path);
      continue;
    }
    try {
      const metadata = JSON.parse(await readFile(join(path, 'package.json'), 'utf8'));
      for (const name of await readdir(path)) {
        if (/^(licen[sc]e|copying)(\.|$)/i.test(name)) {
          notices.push(`${metadata.name} ${metadata.version}\n${await readFile(join(path, name), 'utf8')}`);
        }
      }
    } catch {}
  }
}
await collect('node_modules');
await writeFile('web/notes-editor.LICENSE.txt', notices.join('\n\n--------------------\n\n'));