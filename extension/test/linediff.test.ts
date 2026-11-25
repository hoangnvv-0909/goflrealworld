import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { diffLines } from '../src/linediff';

test('equal texts have no hunk, whatever the line endings', () => {
  assert.deepEqual(diffLines('a\nb\n', 'a\nb\n'), []);
  assert.deepEqual(diffLines('a\r\nb\r\n', 'a\nb\n'), []);
});

test('a changed line is one hunk with the old text', () => {
  const h = diffLines('a\nreturn xs[n]\nc\n', 'a\nreturn xs[n-1]\nc\n');
  assert.equal(h.length, 1);
  assert.deepEqual(h[0], { oldStart: 1, oldCount: 1, newStart: 1, newCount: 1, oldLines: ['return xs[n]'] });
});

test('added and removed lines', () => {
  const add = diffLines('a\nb\n', 'a\nx\ny\nb\n');
  assert.deepEqual(add.map((h) => [h.newStart, h.newCount, h.oldCount]), [[1, 2, 0]]);
  const del = diffLines('a\nx\ny\nb\n', 'a\nb\n');
  assert.deepEqual(del.map((h) => [h.newStart, h.newCount, h.oldCount, h.oldLines]), [[1, 0, 2, ['x', 'y']]]);
});

test('two separate edits are two hunks', () => {
  const h = diffLines('1\n2\n3\n4\n5\n6\n', '1\nTWO\n3\n4\n5\nSIX\n');
  assert.deepEqual(h.map((x) => x.newStart), [1, 5]);
});

test('a nil check inserted in a function', () => {
  const before = 'func (r *R) Inc(k string) {\n\tr.m[k]++\n}\n';
  const after = 'func (r *R) Inc(k string) {\n\tif r.m == nil {\n\t\tr.m = map[string]int{}\n\t}\n\tr.m[k]++\n}\n';
  const h = diffLines(before, after);
  assert.equal(h.length, 1);
  assert.equal(h[0].newStart, 1);
  assert.equal(h[0].newCount, 3);
  assert.equal(h[0].oldCount, 0);
});

test('a very large middle falls back to one hunk instead of a huge table', () => {
  const a = Array.from({ length: 3000 }, (_, i) => `a${i}`).join('\n');
  const b = Array.from({ length: 3000 }, (_, i) => `b${i}`).join('\n');
  const h = diffLines(a, b);
  assert.equal(h.length, 1);
  assert.equal(h[0].oldCount, 3000);
});
