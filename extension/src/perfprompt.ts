// The prompt for a repair Agent in group E (gofl perf). Same shape as
// internal/agentprompt for crash: deterministic text, no source code (the Agent
// sees the project), two modes that share one template.
//
//   E0  the benchmark facts only: what to run, at which sizes
//   E1  E0 plus the top hotspots gofl ranked (3 files, 3 functions each)
//
// E0 differs from E1 only by the ranking section, so the gap between the two
// measures what the hotspots are worth. No vscode import: tested with `node --test`.
import { filesFromReport } from './hints';
import { LOCALIZE_FILE } from './localize';
import { Analysis, Finding, Report } from './model';
import { byRank } from './parse';
import { shortFunc } from './notes';

export interface PerfPromptInput {
  report: Report;
  mode: 'E0' | 'E1';
  /** Benchmark mode: the benchmark name and the two sizes. Absent in pprof mode. */
  bench?: string;
  /** "100,10000". */
  sizes?: string;
  /** Package of the benchmark ("./slow"), from -pkg. Default "./...". */
  pkg?: string;
  /** Extra reproduce commands, e.g. from gofl.repro. */
  repro?: string[];
}

const shell = (s: string) => (/^[\w./=:,-]+$/.test(s) ? s : `'${s.replace(/'/g, `'\\''`)}'`);

/** The command that runs the benchmark once: what the Agent uses to measure its fix. */
export function benchCommand(bench: string, pkg?: string): string {
  const name = bench.startsWith('^') ? bench : `^${bench}$`;
  return `go test -run '^$' -bench ${shell(name)} -benchmem ${pkg?.trim() || './...'}`;
}

/** Value of -pkg in the settings' extra arguments (gofl.perfArgs). */
export function pkgFromArgs(extra: string[]): string | undefined {
  const i = extra.indexOf('-pkg');
  return i >= 0 && i + 1 < extra.length ? extra[i + 1] : undefined;
}

const atLine = (n?: number) => (n && n > 0 ? ` (line ${n})` : '');

function sizeText(sizes?: string): string {
  const p = (sizes ?? '').split(',').map((x) => x.trim()).filter(Boolean);
  return p.length === 2 ? `${p[0]} and ${p[1]}` : '';
}

function hotspotLines(f: Finding): string[] {
  const out = [`   - evidence: ${(f.evidence ?? []).map((e) => e.detail).join('; ') || 'none'}`];
  if (f.flow && f.flow.length > 1) {
    out.push(`   - call path: ${f.flow.map((l) => `\`${shortFunc(l.func ?? l.file)}\``).join(' → ')}`);
  }
  return out;
}

/** The same shape gofl crash gives: files to mark, prompt, mode. */
export function perfAnalysis(i: PerfPromptInput): Analysis {
  const files = filesFromReport(i.report, 3, 3);
  return {
    command: 'perf',
    mode: i.mode,
    prompt: buildPerfPrompt(i),
    files,
    summary: `${i.report.findings.length} hotspot(s), ${files.length} file(s)`,
  };
}

export function buildPerfPrompt(i: PerfPromptInput): string {
  const b: string[] = [];
  b.push('You are fixing a performance problem in a Go project. The whole project is in the current directory.', '');

  b.push('## 1. The problem', '');
  const sz = sizeText(i.sizes);
  if (i.bench) {
    b.push(`- Benchmark: \`${i.bench}\`${sz ? `, run at the sizes ${sz} (one sub-benchmark per size)` : ''}.`);
    b.push('- Symptom: the time per operation grows much faster than the size does.');
    b.push('- Measure with (one command per entry point; the larger size must get faster):');
    b.push(`  - \`${benchCommand(i.bench, i.pkg)}\``);
  } else {
    b.push('- Symptom: a CPU profile at a large size shows functions whose self time grows much faster than at a small size.');
    b.push('- No benchmark command is known; find or write a benchmark that runs the code at a small and a large size.');
  }
  for (const r of i.repro ?? []) {
    if (r.trim()) b.push(`  - \`${r.trim()}\``);
  }

  let n = 2;
  if (i.mode === 'E1') {
    b.push('', `## ${n++}. Where gofl ranks the hotspots`, '');
    b.push('gofl compared CPU profiles of the benchmark at the two sizes and ranked functions by how much their self time grew (per operation). Top files, best first:', '');
    const hints = filesFromReport(i.report, 3, 3);
    if (hints.length === 0) b.push('(no hotspot found)');
    const byFunc = new Map<string, Finding>(byRank(i.report).map((f) => [f.location.func ?? '', f]));
    hints.forEach((h, k) => {
      b.push(`${k + 1}. \`${h.file}\` (score ${h.score.toFixed(3)})`);
      for (const f of h.funcs) {
        b.push(`   - \`${f.func}\`${atLine(f.line)}`);
        const full = byFunc.get(f.func);
        if (full) b.push(...hotspotLines(full).map((l) => l.replace(/^ {3}- /, '     - ')));
      }
    });
    b.push('', 'Treat this as a hint, not as the answer. A CPU profile does not see time spent waiting (I/O, locks, a network or database round trip), ' +
      'and a hotspot may only be slow because its caller calls it too often (a loop, a query per item, repeated work). ' +
      'Look at the callers on the call path before you optimise the hotspot itself. The cause can be outside these files.');
  }

  b.push('', `## ${n}. What to do, in this order`, '');
  b.push('1. Run the benchmark command above at both sizes and note the time per operation.');
  b.push(`2. Find the root cause. Before you change any code, write \`${LOCALIZE_FILE}\` in the project root:`, '');
  b.push('   ```json', '   {"locations": [{"file": "path/relative/to/root.go", "function": "Type.Method", "line": 0}], "explanation": "..."}', '   ```', '');
  b.push('   List the places to fix, most important first.');
  b.push('3. Fix it (a better algorithm or data structure, fewer calls, batching, caching), not only a constant-factor tweak. Keep the behaviour the same.');
  b.push('4. Run the benchmark again: the time per operation at the large size must drop clearly, and its growth with the size must flatten. Every other test of the project must still pass.');
  return b.join('\n') + '\n';
}
