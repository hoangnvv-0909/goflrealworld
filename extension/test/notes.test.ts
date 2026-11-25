import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { parseLocalize } from '../src/localize';
import { findFunc, functionRange, noteFiles, notesFor, Session, shortFunc } from '../src/notes';

const SRC = [
  'package crash', // 0
  '', // 1
  'type Registry struct{ m map[string]int }', // 2
  '', // 3
  '// Inc counts.', // 4
  'func (r *Registry) Inc(k string) {', // 5
  '\tr.m[k]++', // 6
  '}', // 7
  '', // 8
  'func NewSession(anon bool) *Session {', // 9
  '\treturn &Session{}', // 10
  '}', // 11
  '',
].join('\n');

const base = (over: Partial<Session> = {}): Session => ({
  command: 'crash',
  mode: 'E1',
  createdAt: 'now',
  files: [{ file: 'crash/crash.go', score: 0.693, funcs: [{ func: 'goflsample/crash.(*Registry).Inc', line: 7, score: 0.693 }] }],
  snapshots: {},
  ...over,
});

test('shortFunc drops the package and the receiver star', () => {
  assert.equal(shortFunc('goflsample/crash.(*Registry).Inc'), 'Registry.Inc');
  assert.equal(shortFunc('goflsample/crash.Nth'), 'Nth');
  assert.equal(shortFunc('Nth'), 'Nth');
});

test('functionRange covers the whole function from any line in it', () => {
  assert.deepEqual(functionRange(SRC, 6), [5, 7]);
  assert.deepEqual(functionRange(SRC, 10), [9, 11]);
  assert.deepEqual(functionRange(SRC, 8), [8, 8]); // between functions: just the line
});

test('findFunc finds methods and functions by name', () => {
  assert.equal(findFunc(SRC, 'goflsample/crash.(*Registry).Inc'), 5);
  assert.equal(findFunc(SRC, 'NewSession'), 9);
  assert.equal(findFunc(SRC, 'Nope'), -1);
});

test('a suspect function is marked over its whole range, with rank and score', () => {
  const n = notesFor('crash/crash.go', SRC, base());
  assert.equal(n.length, 1);
  assert.equal(n[0].kind, 'suspect');
  assert.deepEqual([n[0].line, n[0].endLine], [5, 7]);
  assert.match(n[0].label, /suspect #1 · Registry\.Inc · score 0\.69/);
  assert.match(n[0].hover, /Given to the Agent: yes/);
  assert.deepEqual(notesFor('other.go', SRC, base()), []);
});

test('E0 says the files were not given to the Agent', () => {
  const n = notesFor('crash/crash.go', SRC, base({ mode: 'E0' }));
  assert.match(n[0].hover, /no \(E0/);
});

test('the perf command uses the word hotspot', () => {
  const n = notesFor('crash/crash.go', SRC, base({ command: 'perf', mode: undefined }));
  assert.match(n[0].label, /hotspot #1/);
});

test('the Agent localisation is found by line, or by name when it gives no line', () => {
  const loc = parseLocalize('{"locations":[{"file":"crash/crash.go","function":"NewSession","line":11},{"file":"crash/crash.go","function":"Registry.Inc"}]}');
  const n = notesFor('crash/crash.go', SRC, base({ files: [], agent: loc.locations })).filter((x) => x.kind === 'agent');
  assert.deepEqual(n.map((x) => [x.line, x.endLine]), [[9, 11], [5, 7]]);
});

test('changes since the snapshot: added and removed lines, with the old text on hover', () => {
  const before = SRC;
  const after = SRC.replace('\tr.m[k]++', '\tif r.m == nil {\n\t\tr.m = map[string]int{}\n\t}\n\tr.m[k]++').replace('\treturn &Session{}\n', '');
  const n = notesFor('crash/crash.go', after, base({ files: [], snapshots: { 'crash/crash.go': before } }));
  const added = n.find((x) => x.kind === 'added');
  const removed = n.find((x) => x.kind === 'removed');
  assert.ok(added && removed);
  assert.equal(added.line, 6);
  assert.equal(added.endLine, 8);
  assert.match(removed.hover, /return &Session\{\}/);
});

test('no snapshot, no change notes', () => {
  assert.deepEqual(notesFor('crash/crash.go', SRC + 'x', base({ files: [] })), []);
});

test('noteFiles lists suspects and the Agent picks once each', () => {
  const s = base({ agent: [{ file: 'crash/hard.go' }, { file: 'crash/crash.go' }] });
  assert.deepEqual(noteFiles(s).sort(), ['crash/crash.go', 'crash/hard.go']);
});

test('parseLocalize is tolerant of what an Agent writes', () => {
  assert.deepEqual(parseLocalize('not json').locations, []);
  const l = parseLocalize('{"locations":[{"file":".\\\\crash\\\\a.go","func":"F","line":3},{"nofile":1},{"file":"b.go","line":0}],"explanation":"x"}');
  assert.deepEqual(l.locations, [{ file: 'crash/a.go', func: 'F', line: 3 }, { file: 'b.go' }]);
  assert.equal(l.explanation, 'x');
});
