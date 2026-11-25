// Location.file is slash-separated and relative to the project root (CONTRACT.md).
// No vscode import, so it is tested with node --test.
import * as fs from 'fs';
import * as path from 'path';

/** Absolute path of `file` in the first root that has it, or undefined. */
export function resolveFile(file: string, roots: string[]): string | undefined {
  if (path.isAbsolute(file)) return fs.existsSync(file) ? file : undefined;
  for (const r of roots) {
    const p = path.join(r, ...file.split('/'));
    if (fs.existsSync(p)) return p;
  }
  return undefined;
}

/** Slash-separated path of `abs` relative to the first root that contains it. */
export function relativeToRoot(abs: string, roots: string[]): string | undefined {
  for (const r of roots) {
    const rel = path.relative(r, abs);
    if (rel && !rel.startsWith('..') && !path.isAbsolute(rel)) return rel.split(path.sep).join('/');
  }
  return undefined;
}
