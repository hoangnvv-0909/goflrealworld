// Checks gofl JSON against the contract. No vscode import, so it is tested
// with plain `node --test`.
import { AgentPrompt, FileHint, Location, Report, SCHEMA_VERSION } from './model';

export class ReportError extends Error {}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Any = any;
const isObj = (v: unknown): v is Record<string, Any> => typeof v === 'object' && v !== null && !Array.isArray(v);

function location(v: Any, where: string): Location {
  if (!isObj(v) || typeof v.file !== 'string' || v.file === '') {
    throw new ReportError(`${where}.file must be a non-empty string`);
  }
  for (const k of ['line', 'col']) {
    if (v[k] !== undefined && (!Number.isInteger(v[k]) || v[k] < 1)) {
      throw new ReportError(`${where}.${k} must be an integer >= 1`);
    }
  }
  if (v.func !== undefined && typeof v.func !== 'string') {
    throw new ReportError(`${where}.func must be a string`);
  }
  return v as Location;
}

function json(text: string): Any {
  try {
    return JSON.parse(text);
  } catch (e) {
    throw new ReportError(`not valid JSON: ${(e as Error).message}`);
  }
}

// CONTRACT.md: refuse a newer version instead of guessing.
function checkVersion(d: Any): void {
  if (!isObj(d)) throw new ReportError('top level must be an object');
  if (typeof d.version !== 'number') throw new ReportError('missing "version"');
  if (d.version > SCHEMA_VERSION) {
    throw new ReportError(`report version ${d.version} is newer than this extension (${SCHEMA_VERSION}); update the extension`);
  }
}

export function parseReport(text: string): Report {
  const d = json(text);
  checkVersion(d);
  if (d.command !== 'crash' && d.command !== 'perf') throw new ReportError('"command" must be "crash" or "perf"');
  if (!Array.isArray(d.findings)) throw new ReportError('"findings" must be an array');
  d.findings.forEach((f: Any, i: number) => {
    if (!isObj(f)) throw new ReportError(`findings[${i}] must be an object`);
    location(f.location, `findings[${i}].location`);
    if (typeof f.score !== 'number') throw new ReportError(`findings[${i}].score must be a number`);
    if (!Number.isInteger(f.rank) || f.rank < 1) throw new ReportError(`findings[${i}].rank must be an integer >= 1`);
    if (f.evidence !== undefined) {
      if (!Array.isArray(f.evidence)) throw new ReportError(`findings[${i}].evidence must be an array`);
      f.evidence.forEach((e: Any, j: number) => {
        if (!isObj(e) || typeof e.kind !== 'string' || typeof e.detail !== 'string') {
          throw new ReportError(`findings[${i}].evidence[${j}] needs kind and detail`);
        }
      });
    }
    if (f.flow !== undefined) {
      if (!Array.isArray(f.flow)) throw new ReportError(`findings[${i}].flow must be an array`);
      f.flow.forEach((l: Any, j: number) => location(l, `findings[${i}].flow[${j}]`));
    }
  });
  if (d.notes !== undefined && (!Array.isArray(d.notes) || d.notes.some((n: Any) => typeof n !== 'string'))) {
    throw new ReportError('"notes" must be an array of strings');
  }
  return d as Report;
}

/** Findings sorted by rank (the JSON order is not promised). */
export function byRank(r: Report) {
  return [...r.findings].sort((a, b) => a.rank - b.rank);
}

/** Parses the output of `gofl crash -agent-prompt -format json`. */
export function parseAgentPrompt(text: string): AgentPrompt {
  const d = json(text);
  checkVersion(d);
  if (d.mode !== 'E0' && d.mode !== 'E1') throw new ReportError('"mode" must be "E0" or "E1"');
  if (typeof d.prompt !== 'string' || d.prompt === '') throw new ReportError('"prompt" must be a non-empty string');
  if (!Array.isArray(d.files)) throw new ReportError('"files" must be an array');
  d.files.forEach((f: Any, i: number) => {
    if (!isObj(f) || typeof f.file !== 'string' || f.file === '' || typeof f.score !== 'number') {
      throw new ReportError(`files[${i}] needs file and score`);
    }
    if (!Array.isArray(f.funcs)) f.funcs = [];
    (f as FileHint).funcs.forEach((fn, j) => {
      if (!isObj(fn) || typeof fn.func !== 'string') throw new ReportError(`files[${i}].funcs[${j}] needs func`);
    });
  });
  return d as AgentPrompt;
}
