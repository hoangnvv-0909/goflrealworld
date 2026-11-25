// Command lines for the gofl binary. Pure, so they are tested without VS Code.
// Adding gofl perf later means adding a case here, not changing the callers.

export interface CrashOptions {
  /** Project root, passed as -dir so paths in the output are relative to it. */
  dir: string;
  /** Extra arguments from the settings (gofl.crashArgs), e.g. ["-profile", "svc.json"]. */
  extra?: string[];
}

/** The ranked findings (for the tree). The crash text goes to stdin. */
export function crashReportArgs(o: CrashOptions): string[] {
  return ['crash', '-dir', o.dir, ...(o.extra ?? []), '-format', 'json'];
}

/** The Agent prompt plus the files it names, as JSON. The crash text goes to stdin. */
export function crashPromptArgs(o: CrashOptions & { mode: 'e0' | 'e1'; repro?: string[] }): string[] {
  const a = ['crash', '-dir', o.dir, ...(o.extra ?? []), '-agent-prompt', '-agent-mode', o.mode, '-format', 'json'];
  for (const r of o.repro ?? []) {
    if (r.trim()) a.push('-repro', r.trim());
  }
  return a;
}

/** Makes gofl run the tests itself and read the crash from them, instead of reading stdin. */
export function crashRunArgs(pkgs: string, run: string): string[] {
  const a = ['-test', pkgs.trim() || './...'];
  if (run.trim()) a.push('-run', run.trim());
  return a;
}
