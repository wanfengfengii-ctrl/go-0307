// Deterministic, dependency-free build for the operations page. It assembles
// the single-page UI into web/dist so the Go service can embed and serve it.
import { mkdir, copyFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = dirname(fileURLToPath(import.meta.url));
const dist = join(root, 'dist');

await mkdir(dist, { recursive: true });

// Static shell: the page references /app.js, served next to index.html.
await copyFile(join(root, 'index.html'), join(dist, 'index.html'));
await copyFile(join(root, 'app.css'), join(dist, 'app.css'));
await copyFile(join(root, 'src', 'app.js'), join(dist, 'app.js'));

// Emit a build manifest so the page can show the deterministic build id.
const buildId = 'web-1.0.0';
await writeFile(join(dist, 'manifest.json'), JSON.stringify({ buildId }, null, 2));

console.log(`built frontend into ${dist}`);
