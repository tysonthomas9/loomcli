import { spawnSync } from 'node:child_process';
import { mkdir, copyFile, cp, readFile, writeFile, readdir, rm } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('.', import.meta.url));
await rm(path.join(root, 'dist'), { recursive: true, force: true });
const result = spawnSync(process.execPath, [path.join(root, 'node_modules/typescript/bin/tsc'), '--project', 'tsconfig.build.json'], { cwd: root, stdio: 'inherit' });
if (result.status !== 0) process.exit(result.status ?? 1);
await mkdir(path.join(root, 'dist/legacy'), { recursive: true });
await copyFile(path.join(root, 'legacy/scenarios.json'), path.join(root, 'dist/legacy/scenarios.json'));
await mkdir(path.join(root, 'dist/fixture'), { recursive: true });
await copyFile(path.join(root, 'fixture/kernel-process.py'), path.join(root, 'dist/fixture/kernel-process.py'));
const parserLock = JSON.parse(await readFile(path.join(root, 'projections/package-lock.json'), 'utf8'));
const parserManifest = JSON.parse(await readFile(path.join(root, 'projections/package.json'), 'utf8'));
const packages = new Set();
function visitPackage(key) {
  if (packages.has(key)) return;
  if (!parserLock.packages[key]) throw new Error('Parser dependency is missing');
  packages.add(key);
  for (const name of Object.keys(parserLock.packages[key].dependencies ?? {})) {
    let parent = key; let candidate;
    for (;;) {
      candidate = parent + '/node_modules/' + name;
      if (parserLock.packages[candidate]) break;
      if (!parent.includes('/node_modules/')) { candidate = 'node_modules/' + name; break; }
      parent = parent.slice(0, parent.lastIndexOf('/node_modules/'));
    }
    visitPackage(candidate);
  }
}
for (const name of Object.keys(parserManifest.dependencies)) visitPackage('node_modules/' + name);
for (const key of packages) {
  const source = path.join(root, 'projections', key);
  await cp(source, path.join(root, 'dist/projections', key), { recursive: true,
    filter: file => file === source || path.basename(file) !== 'node_modules' });
}
const runtimeFiles = [];
async function visit(relative) {
  for (const entry of await readdir(path.join(root, relative), { withFileTypes: true })) {
    const file = path.posix.join(relative, entry.name);
    if (entry.isDirectory()) await visit(file);
    else if (entry.isFile() && !file.includes('.test.') && file !== 'dist/build-receipt.json') {
      const bytes = await readFile(path.join(root, file));
      runtimeFiles.push({ path: file, bytes: bytes.byteLength, sha256: createHash('sha256').update(bytes).digest('hex') });
    }
  }
}
await visit('dist');
const lockSha256 = createHash('sha256').update(await readFile(path.join(root, 'package-lock.json'))).digest('hex');
await writeFile(path.join(root, 'dist/build-receipt.json'), JSON.stringify({ version: 1, lockSha256, runtimeFiles: runtimeFiles.sort((a,b) => a.path.localeCompare(b.path)) }, null, 2) + '\n');
