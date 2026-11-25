// gofl perf (group E) -> the notes the extension already draws for crash.
//
// gofl perf does not print a finding.Report. It writes files into -out:
//   hotspots.json       ranked functions with their self-time growth (required)
//   critical_paths.json call paths to each hotspot (optional)
//   diagnostics.json    status and error_code
// This module turns them into a Report (command "perf"), so Session, notes,
// tree and diff work unchanged. No vscode import: tested with `node --test`.
import { Evidence, Location, Report, SCHEMA_VERSION } from './model';
import { relativeToRoot } from './paths';

export class PerfError extends Error {}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Any = any;
const isObj = (v: unknown): v is Record<string, Any> => typeof v === 'object' && v !== null && !Array.isArray(v);

export interface Hotspot {
  rank: number;
  function: string;
  file: string;
  baseline_self_seconds?: number;
  scaled_self_seconds?: number;
  delta_self_seconds?: number;
  ndc?: number;
  /** Benchmark mode ranks by these (seconds per operation); the self-time fields are then only totals. */
  baseline_per_op_seconds?: number;
  scaled_per_op_seconds?: number;
  delta_per_op_seconds?: number;
  ndc_per_op?: number;
}

export interface Frame {
  name: string;
  file: string;
  line?: number;
}

export interface CriticalPath {
  target: string;
  frames: Frame[];
}

function json(text: string, what: string): Any {
  try {
    return JSON.parse(text);
  } catch (e) {
    throw new PerfError(`${what} is not valid JSON: ${(e as Error).message}`);
  }
}

const num = (v: unknown): number | undefined => (typeof v === 'number' && Number.isFinite(v) ? v : undefined);

/** hotspots.json: an array. Entries without a function or file name are an error. */
export function parseHotspots(text: string): Hotspot[] {
  const d = json(text, 'hotspots.json');
  if (!Array.isArray(d)) throw new PerfError('hotspots.json must be an array');
  return d.map((h: Any, i: number) => {
    if (!isObj(h) || typeof h.function !== 'string' || h.function === '' || typeof h.file !== 'string' || h.file === '') {
      throw new PerfError(`hotspots.json[${i}] needs "function" and "file"`);
    }
    return {
      rank: Number.isInteger(h.rank) && h.rank >= 1 ? h.rank : i + 1,
      function: h.function,
      file: h.file,
      baseline_self_seconds: num(h.baseline_self_seconds),
      scaled_self_seconds: num(h.scaled_self_seconds),
      delta_self_seconds: num(h.delta_self_seconds),
      ndc: num(h.ndc),
      baseline_per_op_seconds: num(h.baseline_per_op_seconds),
      scaled_per_op_seconds: num(h.scaled_per_op_seconds),
      delta_per_op_seconds: num(h.delta_per_op_seconds),
      ndc_per_op: num(h.ndc_per_op),
    };
  });
}

/** critical_paths.json is only a help: anything unreadable gives no paths. */
export function parsePaths(text: string | undefined): CriticalPath[] {
  if (!text) return [];
  let d: Any;
  try {
    d = JSON.parse(text);
  } catch {
    return [];
  }
  if (!Array.isArray(d)) return [];
  const out: CriticalPath[] = [];
  for (const p of d) {
    if (!isObj(p) || typeof p.target !== 'string' || !Array.isArray(p.frames)) continue;
    const frames: Frame[] = [];
    for (const f of p.frames) {
      if (isObj(f) && typeof f.name === 'string' && typeof f.file === 'string') {
        frames.push({ name: f.name, file: f.file, line: Number.isInteger(f.line) && f.line > 0 ? f.line : undefined });
      }
    }
    out.push({ target: p.target, frames });
  }
  return out;
}

export interface Resolve {
  roots: string[];
  /** Does this absolute path exist? Enables the suffix match below. */
  exists?: (abs: string) => boolean;
}

const isAbs = (p: string) => /^([A-Za-z]:)?[\\/]/.test(p);

/**
 * Project-relative, slash-separated path of a file named in a profile.
 * Profiles keep the path of the machine that made them: under the open folder
 * it is made relative; otherwise the longest tail (at least two parts) that
 * exists under a root is used; otherwise it is returned as is (ok = false).
 */
export function projectPath(file: string, r: Resolve): { file: string; ok: boolean } {
  const f = file.replace(/\\/g, '/');
  if (!isAbs(f)) return { file: f.replace(/^\.\//, ''), ok: true };
  const rel = relativeToRoot(f, r.roots);
  if (rel) return { file: rel, ok: true };
  if (r.exists) {
    const parts = f.split('/').filter((x) => x !== '' && !/^[A-Za-z]:$/.test(x));
    for (let i = 0; i <= parts.length - 2; i++) {
      const tail = parts.slice(i).join('/');
      for (const root of r.roots) {
        if (r.exists(`${root.replace(/[\\/]+$/, '')}/${tail}`)) return { file: tail, ok: true };
      }
    }
  }
  return { file: f, ok: false };
}

const tailName = (fn: string) => fn.slice(fn.lastIndexOf('/') + 1);

/** 0.00000196 -> "1.96µs", 0.0077 -> "7.68ms". */
export function human(sec: number): string {
  const a = Math.abs(sec);
  if (a >= 1) return `${sec.toFixed(2)}s`;
  if (a >= 1e-3) return `${(sec * 1e3).toFixed(2)}ms`;
  if (a >= 1e-6) return `${(sec * 1e6).toFixed(2)}µs`;
  return `${(sec * 1e9).toFixed(0)}ns`;
}

/** The growth gofl ranked by: seconds per operation in benchmark mode, else total self seconds. */
const growth = (h: Hotspot): number => h.delta_per_op_seconds ?? h.delta_self_seconds ?? 0;

/** The Report the extension draws. Hotspots are ranked by their growth, score 0..1. */
export function perfToReport(hotspots: Hotspot[], paths: CriticalPath[], r: Resolve): Report {
  const maxDelta = Math.max(0, ...hotspots.map(growth));
  const outside = new Set<string>();
  const where = (file: string): string => {
    const p = projectPath(file, r);
    if (!p.ok) outside.add(p.file);
    return p.file;
  };
  const findings = [...hotspots]
    .sort((a, b) => a.rank - b.rank)
    .map((h) => {
      const path = paths.find((p) => p.target === h.function);
      const own = path?.frames.find((f) => f.name === tailName(h.function));
      const flow: Location[] | undefined = path?.frames.length
        ? path.frames.map((f) => ({ file: where(f.file), line: f.line, func: f.name }))
        : undefined;
      const evidence: Evidence[] = [];
      if (h.delta_per_op_seconds !== undefined) {
        const from = h.baseline_per_op_seconds !== undefined ? `${human(h.baseline_per_op_seconds)} → ` : '';
        const to = h.scaled_per_op_seconds !== undefined ? `${human(h.scaled_per_op_seconds)} ` : '';
        evidence.push({ kind: 'delta-per-op', detail: `self time per operation ${from}${to}(+${human(h.delta_per_op_seconds)})` });
      } else if (h.delta_self_seconds !== undefined) {
        const from = h.baseline_self_seconds !== undefined ? `${h.baseline_self_seconds.toFixed(2)}s → ` : '';
        const to = h.scaled_self_seconds !== undefined ? `${h.scaled_self_seconds.toFixed(2)}s ` : '';
        evidence.push({ kind: 'delta-self', detail: `self time ${from}${to}(+${h.delta_self_seconds.toFixed(2)}s)` });
      }
      const share = h.ndc_per_op ?? h.ndc;
      if (share !== undefined) {
        evidence.push({ kind: 'ndc', detail: `${Math.round(share * 100)}% of the total increase` });
      }
      const loc: Location = { file: where(h.file), func: h.function };
      if (own?.line) loc.line = own.line;
      return {
        location: loc,
        score: maxDelta > 0 ? Math.max(0, Math.round((growth(h) / maxDelta) * 1000) / 1000) : 0,
        rank: h.rank,
        ...(evidence.length ? { evidence } : {}),
        ...(flow ? { flow } : {}),
      };
    });
  const notes = ['CPU profile does not see time spent waiting on I/O.'];
  if (outside.size) {
    notes.push(`not under the open folder (the profile came from another machine?): ${[...outside].slice(0, 3).join(', ')}`);
  }
  return { version: SCHEMA_VERSION, command: 'perf', findings, notes };
}

const MESSAGES: Record<string, string> = {
  NO_GO: 'Go is missing or unusable. Install Go, or set gofl.perfArgs ["-go", "<path>"].',
  NO_MODULE: 'no go.mod found. Open the Go project folder (the one with go.mod).',
  NO_BENCH: 'the benchmark or its sizes were not found. Check the benchmark name and sizes (b.Run("size=100", ...)).',
  BUILD_FAIL: 'the test binary does not compile.',
  TEST_NEEDS_ENV: 'setup failed, or the benchmark failed or skipped itself. Set it up with -setup / -env.',
  TIMEOUT: 'a setup command or a benchmark run took too long.',
  NO_PROFILE: 'a CPU profile is missing, unreadable or empty.',
  NO_POSITIVE_DELTA: 'no function got slower between the two sizes, so there is no hotspot. Use a larger size gap.',
  ENTRY_NOT_COVERING: 'a hotspot lies outside the entry function.',
};

export interface PerfStatus {
  ok: boolean;
  code?: string;
  message: string;
}

/** Reads the run's outcome: the last "status:" line of stdout, else diagnostics.json. */
export function perfStatus(stdout: string, diagnostics: string | undefined, exitCode: number | null): PerfStatus {
  const m = /^status:\s*(ok|error)\b[ \t]*([A-Z_]+)?/m.exec(stdout);
  let code = m?.[1] === 'error' ? m[2] : undefined;
  let ok = m ? m[1] === 'ok' : exitCode === 0;
  if (diagnostics) {
    try {
      const d = JSON.parse(diagnostics);
      if (d?.status === 'error') {
        ok = false;
        if (typeof d.error_code === 'string') code = d.error_code;
      }
    } catch {
      // diagnostics.json is only a help
    }
  }
  if (ok) return { ok: true, message: 'ok' };
  return { ok: false, code, message: code ? `${code}: ${MESSAGES[code] ?? 'see Output → gofl'}` : 'gofl perf failed. See Output → gofl.' };
}
