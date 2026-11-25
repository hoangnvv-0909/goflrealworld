// Visual notes: what to draw on one file. All the decisions are here and pure,
// so they are tested without VS Code; decorations.ts only paints them.
//
// Three layers (the same for gofl crash and, later, gofl perf):
//   suspect           where gofl points (the top files given to the Agent)
//   agent             where the Agent says the fault is (gofl-localize.json)
//   added / removed   what changed since gofl analysed the file (the Agent's fix)
import { diffLines } from './linediff';
import { FileHint, Location } from './model';

export type NoteKind = 'suspect' | 'agent' | 'added' | 'removed';

export interface Note {
  kind: NoteKind;
  /** 0-based inclusive line range. */
  line: number;
  endLine: number;
  /** Short text shown after the first line. */
  label: string;
  /** Markdown shown on hover. */
  hover: string;
}

/** What the notes are drawn from. Plain data, so it can be saved. */
export interface Session {
  command: 'crash' | 'perf';
  mode?: 'E0' | 'E1';
  createdAt: string;
  /** The files gofl pointed at, best first. */
  files: FileHint[];
  /** File text at analysis time, by project-relative path. */
  snapshots: Record<string, string>;
  /** The Agent's own locations, once it has written gofl-localize.json. */
  agent?: Location[];
  prompt?: string;
}

/** "goflsample/crash.(*Registry).Inc" -> "Registry.Inc"; "pkg.Func" -> "Func". */
export function shortFunc(fn: string): string {
  const base = fn.slice(fn.lastIndexOf('/') + 1);
  const dot = base.indexOf('.');
  const rest = dot >= 0 ? base.slice(dot + 1) : base;
  return rest.replace(/^\(\*?([^)]+)\)\./, '$1.');
}

const splitLines = (t: string): string[] => t.replace(/\r\n/g, '\n').split('\n');

/** 0-based range of the Go function that holds `line` (0-based), or just that line. */
export function functionRange(text: string, line: number): [number, number] {
  const ls = splitLines(text);
  if (line < 0 || line >= ls.length) return [line, line];
  let start = -1;
  for (let i = line; i >= 0; i--) {
    if (/^func\s/.test(ls[i])) {
      start = i;
      break;
    }
    if (i < line && ls[i] === '}') break; // we left the function we were in
  }
  if (start < 0) return [line, line];
  for (let i = start; i < ls.length; i++) {
    if (ls[i] === '}') return [start, i];
    if (i === start && /\}\s*$/.test(ls[i]) && ls[i].includes('{')) return [start, i]; // one-line func
  }
  return [start, line];
}

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

/** 0-based line of the function named `fn` in `text`, or -1. */
export function findFunc(text: string, fn: string): number {
  const name = shortFunc(fn).split('.').pop() ?? fn;
  if (!name) return -1;
  const re = new RegExp('^func\\s+(\\([^)]*\\)\\s*)?' + escapeRe(name) + '\\s*[\\[(]');
  return splitLines(text).findIndex((l) => re.test(l));
}

const clamp = (n: number, total: number) => Math.min(Math.max(n, 0), Math.max(total - 1, 0));

/** The notes for one file. `file` is project-relative; `text` is its text now. */
export function notesFor(file: string, text: string, s: Session): Note[] {
  const total = splitLines(text).length;
  const out: Note[] = [];
  const tag = s.command === 'perf' ? 'hotspot' : 'suspect';

  s.files.forEach((f, idx) => {
    if (f.file !== file) return;
    const detail = f.funcs.map((x) => `- \`${shortFunc(x.func)}\`${x.line ? ` (line ${x.line})` : ''}`).join('\n');
    const given = s.mode ? `\n\nGiven to the Agent: ${s.mode === 'E1' ? 'yes' : 'no (E0, crash facts only)'}` : '';
    const placed = new Set<number>();
    for (const fn of f.funcs) {
      if (!fn.line) continue;
      const [a, b] = functionRange(text, clamp(fn.line - 1, total));
      if (placed.has(a)) continue;
      placed.add(a);
      out.push({
        kind: 'suspect',
        line: a,
        endLine: b,
        label: `gofl ${tag} #${idx + 1} · ${shortFunc(fn.func)} · score ${f.score.toFixed(2)}`,
        hover: `**gofl ${tag} #${idx + 1}** (score ${f.score.toFixed(3)})\n\n${detail}${given}`,
      });
    }
    if (placed.size === 0) {
      out.push({
        kind: 'suspect', line: 0, endLine: 0,
        label: `gofl ${tag} #${idx + 1} · score ${f.score.toFixed(2)}`,
        hover: `**gofl ${tag} #${idx + 1}**\n\n${detail}${given}`,
      });
    }
  });

  for (const l of s.agent ?? []) {
    if (l.file !== file) continue;
    let at = l.line ? l.line - 1 : -1;
    if (at < 0 && l.func) at = findFunc(text, l.func);
    if (at < 0) {
      out.push({
        kind: 'agent', line: 0, endLine: 0, label: `Agent: ${l.func ? shortFunc(l.func) : 'this file'}`,
        hover: `**Agent** names this file${l.func ? ` (\`${l.func}\`)` : ''}, but gives no line.`,
      });
      continue;
    }
    const [a, b] = functionRange(text, clamp(at, total));
    out.push({
      kind: 'agent', line: a, endLine: b, label: `Agent says: ${l.func ? shortFunc(l.func) : 'here'}`,
      hover: `**Agent localisation**${l.func ? `\n\n\`${l.func}\`` : ''}${l.line ? ` (line ${l.line})` : ''}`,
    });
  }

  const before = s.snapshots[file];
  if (before !== undefined) {
    for (const h of diffLines(before, text)) {
      const old = h.oldLines.length ? '```go\n' + h.oldLines.join('\n') + '\n```' : '';
      if (h.newCount > 0) {
        out.push({
          kind: 'added', line: h.newStart, endLine: h.newStart + h.newCount - 1,
          label: h.oldCount ? `changed (${h.oldCount} → ${h.newCount} lines)` : `added ${h.newCount} line(s)`,
          hover: h.oldCount ? `**Before the fix:**\n\n${old}` : '**New lines** (not in the file gofl analysed)',
        });
      } else {
        const at = clamp(h.newStart, total);
        out.push({
          kind: 'removed', line: at, endLine: at,
          label: `removed ${h.oldCount} line(s)`, hover: `**Removed by the fix:**\n\n${old}`,
        });
      }
    }
  }
  return out;
}

/** Project-relative paths of every file that has notes: the suspects and the Agent's picks. */
export function noteFiles(s: Session): string[] {
  const set = new Set<string>(s.files.map((f) => f.file));
  for (const l of s.agent ?? []) set.add(l.file);
  return [...set];
}
