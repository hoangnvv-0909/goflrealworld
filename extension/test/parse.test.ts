import * as assert from 'node:assert/strict';
import * as fs from 'node:fs';
import * as os from 'node:os';
import * as path from 'node:path';
import { test } from 'node:test';
import { crashPromptArgs, crashReportArgs, crashRunArgs } from '../src/args';
import { filesFromReport } from '../src/hints';
import { byRank, parseAgentPrompt, parseReport, ReportError } from '../src/parse';
import { relativeToRoot, resolveFile } from '../src/paths';

const samples = path.join(__dirname, '..', '..', 'samples');
const sample = (n: string) => fs.readFileSync(path.join(samples, n), 'utf8');

test('the sample reports parse', () => {
  for (const n of ['example.json', 'goflsample-crash.json', 'goflsample-perf.json']) {
    const r = parseReport(sample(n));
    assert.ok(r.findings.length > 0, n);
  }
});

test('a newer schema version is refused', () => {
  assert.throws(() => parseReport('{"version":99,"command":"crash","findings":[]}'), ReportError);
  assert.throws(() => parseAgentPrompt('{"version":99,"mode":"E1","prompt":"x","files":[]}'), ReportError);
});

test('bad reports say what is wrong', () => {
  assert.throws(() => parseReport('nope'), /not valid JSON/);
  assert.throws(() => parseReport('{"version":1,"command":"x","findings":[]}'), /command/);
  assert.throws(() => parseReport('{"version":1,"command":"crash","findings":[{"location":{"file":""},"score":1,"rank":1}]}'), /file/);
});

test('byRank sorts', () => {
  const r = parseReport(sample('goflsample-crash.json'));
  r.findings.reverse();
  assert.deepEqual(byRank(r).map((f) => f.rank), [1, 2, 2]);
});

const PROMPT = JSON.stringify({
  version: 1, mode: 'E1', prompt: '# crash', events: 3, groups: 1,
  files: [{ file: 'crash/hard.go', score: 0.693, funcs: [{ func: 'goflsample/crash.(*Session).Token', line: 63, score: 0.693 }] }],
});

test('the agent-prompt document parses', () => {
  const d = parseAgentPrompt(PROMPT);
  assert.equal(d.mode, 'E1');
  assert.equal(d.files[0].funcs[0].line, 63);
  assert.throws(() => parseAgentPrompt('{"version":1,"mode":"E2","prompt":"x","files":[]}'), /mode/);
  assert.throws(() => parseAgentPrompt('{"version":1,"mode":"E1","prompt":"","files":[]}'), /prompt/);
});

test('filesFromReport keeps the first 3 files and 3 functions each, in rank order', () => {
  const r = parseReport(JSON.stringify({
    version: 1, command: 'perf',
    findings: [
      ['a.go', 'A1', 9], ['a.go', 'A2', 8], ['b.go', 'B1', 7], ['a.go', 'A3', 6], ['a.go', 'A4', 5], ['c.go', 'C1', 4], ['d.go', 'D1', 3],
    ].map(([file, func, score], i) => ({ location: { file, func, line: 1 }, score, rank: i + 1 })),
  }));
  const f = filesFromReport(r);
  assert.deepEqual(f.map((x) => x.file), ['a.go', 'b.go', 'c.go']);
  assert.deepEqual(f[0].funcs.map((x) => x.func), ['A1', 'A2', 'A3']);
  assert.equal(f[0].score, 9);
});

test('gofl command lines', () => {
  assert.deepEqual(crashReportArgs({ dir: '/p' }), ['crash', '-dir', '/p', '-format', 'json']);
  assert.deepEqual(
    crashPromptArgs({ dir: '/p', mode: 'e0', repro: ['go test ./a -run T', '  '], extra: ['-profile', 'x.json'] }),
    ['crash', '-dir', '/p', '-profile', 'x.json', '-agent-prompt', '-agent-mode', 'e0', '-format', 'json', '-repro', 'go test ./a -run T'],
  );
  assert.deepEqual(crashRunArgs('', ''), ['-test', './...']);
  assert.deepEqual(crashRunArgs('./crash', '^TestNth$'), ['-test', './crash', '-run', '^TestNth$']);
});

test('resolveFile and relativeToRoot', () => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'gofl-'));
  fs.mkdirSync(path.join(root, 'crash'));
  fs.writeFileSync(path.join(root, 'crash', 'a.go'), 'x');
  assert.equal(resolveFile('crash/a.go', [root]), path.join(root, 'crash', 'a.go'));
  assert.equal(resolveFile('crash/none.go', [root]), undefined);
  assert.equal(relativeToRoot(path.join(root, 'crash', 'a.go'), [root]), 'crash/a.go');
  assert.equal(relativeToRoot(path.join(os.tmpdir(), 'elsewhere.go'), [root]), undefined);
});
