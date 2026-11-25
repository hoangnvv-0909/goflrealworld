import * as assert from 'node:assert/strict';
import { test } from 'node:test';
import { execGofl, firstLine } from '../src/runner';

// The "gofl binary" is node itself, so the test needs nothing installed.
const node = process.execPath;
const cwd = process.cwd();

test('stdin goes to the program and stdout comes back', async () => {
  const r = await execGofl(node, ['-e', 'process.stdin.pipe(process.stdout)'], { cwd, stdin: 'panic: boom\n' });
  assert.equal(r.code, 0);
  assert.equal(r.stdout, 'panic: boom\n');
});

test('a failing program gives its code and stderr', async () => {
  const r = await execGofl(node, ['-e', 'console.error("gofl crash: no crash found"); process.exit(2)'], { cwd });
  assert.equal(r.code, 2);
  assert.equal(firstLine(r), 'no crash found');
});

test('a missing binary is reported as not found', async () => {
  const r = await execGofl('gofl-does-not-exist-xyz', [], { cwd });
  assert.equal(r.notFound, true);
});

test('a program that never reads stdin does not break the run', async () => {
  const r = await execGofl(node, ['-e', 'console.log("ok")'], { cwd, stdin: 'x'.repeat(1 << 20) });
  assert.equal(r.code, 0);
  assert.equal(r.stdout.trim(), 'ok');
});

test('the timeout kills a slow program', async () => {
  const r = await execGofl(node, ['-e', 'setTimeout(()=>{}, 60000)'], { cwd, timeoutMs: 300 });
  assert.equal(r.timedOut, true);
});
