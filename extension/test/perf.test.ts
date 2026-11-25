import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { perfRunArgs } from '../src/args';
import { filesFromReport } from '../src/hints';
import { notesFor, noteFiles } from '../src/notes';
import { parseReport } from '../src/parse';
import { parseHotspots, parsePaths, PerfError, perfStatus, perfToReport, projectPath } from '../src/perf';

// Shape of the real out/hotspots.json and out/critical_paths.json of gofl perf.
const HOT = JSON.stringify([
  { rank: 1, function: 'sample/slow.contains', file: 'slow/slow.go', baseline_self_seconds: 0.28, scaled_self_seconds: 0.42, delta_self_seconds: 0.14, ndc: 0.7 },
  { rank: 2, function: 'sample/slow.Dedup', file: 'slow/slow.go', delta_self_seconds: 0.07, ndc: 0.3 },
]);
const PATHS = JSON.stringify([
  {
    target: 'sample/slow.contains',
    target_rank: 1,
    frames: [
      { name: 'slow.BenchmarkDedup.func1', file: 'slow/slow_test.go', line: 17 },
      { name: 'slow.Dedup', file: 'slow/slow.go', line: 9 },
      { name: 'slow.contains', file: 'slow/slow.go', line: 20 },
    ],
  },
]);
const R = { roots: ['/proj'] };

test('hotspots become a perf report, ranked, scored by growth', () => {
  const rep = perfToReport(parseHotspots(HOT), parsePaths(PATHS), R);
  assert.equal(rep.command, 'perf');
  assert.equal(rep.findings.length, 2);
  assert.equal(rep.findings[0].score, 1);
  assert.equal(rep.findings[1].score, 0.5);
  assert.equal(rep.findings[0].location.line, 20);
  assert.equal(rep.findings[0].flow?.length, 3);
  assert.match(rep.findings[0].evidence![0].detail, /0\.28s → 0\.42s \(\+0\.14s\)/);
  assert.match(rep.findings[0].evidence![1].detail, /70%/);
});

// Real output of: gofl perf -dir . -pkg ./slow -bench BenchmarkDedup -sizes 100,2000
// Benchmark mode ranks by per-operation time; the self-time totals even go negative.
const BENCH = JSON.stringify([
  { rank: 1, function: 'sample/slow.contains', file: 'slow/slow.go', baseline_self_seconds: 0.87, scaled_self_seconds: 0.57, delta_self_seconds: -0.3, ndc: 0, baseline_per_op_seconds: 0.000001963, scaled_per_op_seconds: 0.007682159, delta_per_op_seconds: 0.007680196, ndc_per_op: 0.904788 },
  { rank: 2, function: 'sample/slow.Dedup', file: 'slow/slow.go', baseline_self_seconds: 0.2, scaled_self_seconds: 0.06, delta_self_seconds: -0.14, ndc: 0, baseline_per_op_seconds: 4.51e-7, scaled_per_op_seconds: 0.000808648, delta_per_op_seconds: 0.000808197, ndc_per_op: 0.095212 },
]);

test('benchmark mode: scored and described by per-operation time, not the negative self totals', () => {
  const rep = perfToReport(parseHotspots(BENCH), [], R);
  assert.equal(rep.findings[0].score, 1);
  assert.equal(rep.findings[1].score, 0.105);
  assert.equal(rep.findings[0].evidence![0].kind, 'delta-per-op');
  assert.match(rep.findings[0].evidence![0].detail, /1\.96µs → 7\.68ms \(\+7\.68ms\)/);
  assert.match(rep.findings[0].evidence![1].detail, /90%/);
  assert.ok(rep.findings.every((f) => f.score >= 0));
});

test('the report passes the same validation as any gofl report', () => {
  const rep = perfToReport(parseHotspots(HOT), parsePaths(PATHS), R);
  assert.doesNotThrow(() => parseReport(JSON.stringify(rep)));
});

test('the notes call it a hotspot and cover the function', () => {
  const rep = perfToReport(parseHotspots(HOT), parsePaths(PATHS), R);
  const files = filesFromReport(rep);
  const session = { command: 'perf' as const, createdAt: 'now', files, snapshots: {} };
  assert.deepEqual(noteFiles(session), ['slow/slow.go']);
  const src = ['package slow', '', 'func contains(xs []int, x int) bool {', '\treturn false', '}', ''].join('\n');
  const n = notesFor('slow/slow.go', src, session);
  assert.ok(n.length > 0);
  assert.match(n[0].label, /hotspot/);
});

test('a missing critical_paths.json or a broken one is no problem', () => {
  assert.deepEqual(parsePaths(undefined), []);
  assert.deepEqual(parsePaths('not json'), []);
  const rep = perfToReport(parseHotspots(HOT), [], R);
  assert.equal(rep.findings[0].location.line, undefined);
  assert.equal(rep.findings[0].flow, undefined);
});

test('an empty hotspot list gives an empty report', () => {
  const rep = perfToReport(parseHotspots('[]'), [], R);
  assert.deepEqual(rep.findings, []);
});

test('bad hotspots.json is refused', () => {
  assert.throws(() => parseHotspots('{}'), PerfError);
  assert.throws(() => parseHotspots('nope'), PerfError);
  assert.throws(() => parseHotspots('[{"rank":1}]'), PerfError);
});

test('absolute paths: under the root, by tail, or outside', () => {
  assert.deepEqual(projectPath('slow\\slow.go', R), { file: 'slow/slow.go', ok: true });
  const abs = 'C:/Other/Machine/sample/slow/slow.go';
  const exists = (p: string) => p === '/proj/sample/slow/slow.go';
  assert.deepEqual(projectPath(abs, { roots: ['/proj'], exists }), { file: 'sample/slow/slow.go', ok: true });
  assert.deepEqual(projectPath(abs, R), { file: abs, ok: false });
  // one part alone (slow.go) is too weak to match
  assert.equal(projectPath('C:/x/slow.go', { roots: ['/proj'], exists: (p) => p === '/proj/slow.go' }).ok, false);
});

test('a file outside the folder is kept and a note says so', () => {
  const hs = parseHotspots(JSON.stringify([{ rank: 1, function: 'a.F', file: 'C:/Other/a.go', delta_self_seconds: 1 }]));
  const rep = perfToReport(hs, [], R);
  assert.equal(rep.findings[0].location.file, 'C:/Other/a.go');
  assert.ok(rep.notes!.some((n) => n.includes('not under the open folder')));
});

test('status: ok, error code from stdout, and from diagnostics.json', () => {
  assert.equal(perfStatus('status: ok (bench mode, 3.0s)\nwrote 1 hotspots', undefined, 0).ok, true);
  const e = perfStatus('status: error NO_BENCH\n', undefined, 1);
  assert.equal(e.ok, false);
  assert.equal(e.code, 'NO_BENCH');
  const d = perfStatus('', JSON.stringify({ status: 'error', error_code: 'NO_MODULE' }), 1);
  assert.equal(d.code, 'NO_MODULE');
  assert.match(d.message, /go\.mod/);
  assert.equal(perfStatus('', undefined, 2).ok, false);
});

test('perfRunArgs: no -format, benchmark or pprof mode, old "perf" prefix dropped', () => {
  const b = perfRunArgs({ dir: '/p', out: '/o', bench: 'BenchmarkX', sizes: ' 100,10000 ', extra: ['-pkg', './x'] });
  assert.deepEqual(b, ['perf', '-dir', '/p', '-out', '/o', '-pkg', './x', '-bench', 'BenchmarkX', '-sizes', '100,10000']);
  assert.ok(!b.includes('-format'));
  const p = perfRunArgs({ dir: '/p', out: '/o', base: 'a.pprof', scaled: 'b.pprof', bench: 'ignored' });
  assert.deepEqual(p, ['perf', '-dir', '/p', '-out', '/o', '-base', 'a.pprof', '-scaled', 'b.pprof']);
  const old = perfRunArgs({ dir: '/p', out: '/o', bench: 'B', extra: ['perf', '-pkg', './x'] });
  assert.deepEqual(old.slice(0, 7), ['perf', '-dir', '/p', '-out', '/o', '-pkg', './x']);
});
