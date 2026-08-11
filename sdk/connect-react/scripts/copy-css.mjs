import { copyFileSync, mkdirSync, existsSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const src = join(root, 'src', 'theme', 'tokens.css');
const dest = join(root, 'dist', 'theme', 'tokens.css');
mkdirSync(dirname(dest), { recursive: true });
if (!existsSync(src)) {
  console.error('Missing', src);
  process.exit(1);
}
copyFileSync(src, dest);
console.log('Copied theme CSS →', dest);
