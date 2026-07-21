import { readdir, readFile, writeFile } from 'node:fs/promises';
import { extname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const textExtensions = new Set(['.css', '.html', '.js', '.json', '.map', '.svg']);

async function normalize(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) {
      await normalize(path);
      continue;
    }
    if (!textExtensions.has(extname(entry.name))) continue;
    const source = await readFile(path, 'utf8');
    const normalized = source.replace(/[\t ]+$/gm, '');
    if (normalized !== source) await writeFile(path, normalized);
  }
}

await normalize(fileURLToPath(new URL('../dist', import.meta.url)));
