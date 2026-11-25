// Runs the gofl binary. No vscode import, so it is tested with node --test
// (the tests run `node` itself as the "binary").
import { spawn } from 'child_process';

export interface RunResult {
  code: number | null;
  stdout: string;
  stderr: string;
  /** The binary does not exist or cannot be started. */
  notFound?: boolean;
  timedOut?: boolean;
}

export interface RunOptions {
  cwd: string;
  /** Text written to the program's standard input (the crash log). */
  stdin?: string;
  timeoutMs?: number;
}

export function execGofl(bin: string, args: string[], o: RunOptions): Promise<RunResult> {
  return new Promise((resolve) => {
    let out = '';
    let err = '';
    let done = false;
    let timedOut = false;
    const finish = (r: RunResult) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      resolve(r);
    };
    const child = spawn(bin, args, { cwd: o.cwd, stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill();
    }, o.timeoutMs ?? 5 * 60 * 1000);
    child.stdout.setEncoding('utf8').on('data', (d: string) => (out += d));
    child.stderr.setEncoding('utf8').on('data', (d: string) => (err += d));
    child.on('error', (e: NodeJS.ErrnoException) =>
      finish({ code: null, stdout: out, stderr: err || e.message, notFound: e.code === 'ENOENT' }),
    );
    child.on('close', (code) => finish({ code, stdout: out, stderr: err, timedOut }));
    // A program that exits without reading stdin makes the write fail with EPIPE: not an error here.
    child.stdin.on('error', () => undefined);
    child.stdin.end(o.stdin ?? '');
  });
}

/** First useful line of a failed run, for a message box. */
export function firstLine(r: RunResult): string {
  const l = (r.stderr || r.stdout).split('\n').find((x) => x.trim() !== '');
  return (l ?? `exit code ${r.code}`).replace(/^gofl( crash)?:\s*/, '').trim();
}
