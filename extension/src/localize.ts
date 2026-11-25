// The Agent's own answer: gofl-localize.json in the project root, written by
// the prompt of internal/agentprompt. No vscode import.
import { Location } from './model';

export const LOCALIZE_FILE = 'gofl-localize.json';

export interface Localization {
  locations: Location[];
  explanation?: string;
}

/** Reads the Agent's answer. It is tolerant: the Agent writes it by hand. */
export function parseLocalize(text: string): Localization {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  let d: any;
  try {
    d = JSON.parse(text);
  } catch {
    return { locations: [] };
  }
  const out: Location[] = [];
  for (const l of Array.isArray(d?.locations) ? d.locations : []) {
    if (!l || typeof l.file !== 'string' || l.file === '') continue;
    const fn = typeof l.function === 'string' ? l.function : typeof l.func === 'string' ? l.func : undefined;
    const loc: Location = { file: l.file.replace(/\\/g, '/').replace(/^\.\//, '') };
    if (Number.isInteger(l.line) && l.line > 0) loc.line = l.line;
    if (fn) loc.func = fn;
    out.push(loc);
  }
  return { locations: out, explanation: typeof d?.explanation === 'string' ? d.explanation : undefined };
}
