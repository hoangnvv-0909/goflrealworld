import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseHotspots, parsePaths, perfToReport } from '../src/perf';
import { tagOf } from '../src/notes';
import { benchCommand, buildPerfPrompt, perfAnalysis, pkgFromArgs } from '../src/perfprompt';

const HOT = JSON.stringify([
  { rank: 1, function: 'sample/slow.contains', file: 'slow/slow.go', baseline_per_op_seconds: 0.000001963, scaled_per_op_seconds: 0.007682159, delta_per_op_seconds: 0.007680196, ndc_per_op: 0.9 },
  { rank: 2, function: 'sample/slow.Dedup', file: 'slow/slow.go', baseline_per_op_seconds: 4.5e-7, scaled_per_op_seconds: 0.0008, delta_per_op_seconds: 0.0008, ndc_per_op: 0.1 },
]);
const PATHS = JSON.stringify([
  { target: 'sample/slow.contains', frames: [
    { name: 'slow.Dedup', file: 'slow/slow.go', line: 9 },
    { name: 'slow.contains', file: 'slow/slow.go', line: 20 },
  ] },
]);
const report = perfToReport(parseHotspots(HOT), parsePaths(PATHS), { roots: ['/p'] });
const base = { report, bench: 'BenchmarkDedup', sizes: '100,2000', pkg: './slow' };

test('E1 lists hotspots with evidence and call path', () => {
  const p = buildPerfPrompt({ ...base, mode: 'E1' });
  assert.match(p, /## 2\. Where gofl ranks the hotspots/);
  assert.match(p, /`slow\/slow\.go` \(score 1\.000\)/);
  assert.match(p, /`sample\/slow\.contains` \(line 20\)/);
  assert.match(p, /1\.96µs → 7\.68ms/);
  assert.match(p, /call path: `Dedup` → `contains`|call path: `slow\.Dedup` → `slow\.contains`/);
  assert.match(p, /## 3\. What to do/);
});

test('E0 differs from E1 only by the ranking section', () => {
  const e0 = buildPerfPrompt({ ...base, mode: 'E0' });
  const e1 = buildPerfPrompt({ ...base, mode: 'E1' });
  assert.doesNotMatch(e0, /ranks the hotspots/);
  assert.doesNotMatch(e0, /slow\.contains/);
  assert.match(e0, /## 2\. What to do/);
  const strip = (t: string) => t.split('\n').filter((l) => !/^## \d/.test(l)).join('\n');
  assert.ok(strip(e1).includes(strip(e0).split('\n').slice(0, 8).join('\n')));
});

test('benchmark command and -pkg', () => {
  assert.equal(benchCommand('BenchmarkDedup', './slow'), "go test -run '^$' -bench '^BenchmarkDedup$' -benchmem ./slow");
  assert.equal(benchCommand('BenchmarkDedup'), "go test -run '^$' -bench '^BenchmarkDedup$' -benchmem ./...");
  assert.equal(pkgFromArgs(['-pkg', './slow', '-setup', 'x']), './slow');
  assert.equal(pkgFromArgs([]), undefined);
});

test('pprof mode has no benchmark command', () => {
  const p = buildPerfPrompt({ report, mode: 'E1' });
  assert.match(p, /No benchmark command is known/);
  assert.match(p, /gofl-localize\.json/);
});

test('perf gives the same Analysis shape as crash: files, prompt, mode', () => {
  const a = perfAnalysis({ ...base, mode: 'E1' });
  assert.equal(a.command, 'perf');
  assert.equal(a.mode, 'E1');
  assert.equal(a.files[0].file, 'slow/slow.go');
  assert.equal(a.files[0].funcs.length, 2);
  assert.match(a.prompt, /gofl-localize\.json/);
  assert.equal(tagOf('perf'), 'hotspot');
  assert.equal(tagOf('crash'), 'suspect');
});
