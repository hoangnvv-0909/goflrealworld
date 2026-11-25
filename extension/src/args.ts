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

export interface PerfOptions {
  /** Project root: file paths in the result are relative to it. */
  dir: string;
  /** Where gofl perf writes hotspots.json and the other files. */
  out: string;
  /** Benchmark mode: the benchmark name and the two sizes ("100,10000"). */
  bench?: string;
  sizes?: string;
  /** pprof mode: two CPU profiles, small and large size. */
  base?: string;
  scaled?: string;
  /** Extra arguments from the settings (gofl.perfArgs), e.g. ["-pkg", "./internal/x"]. */
  extra?: string[];
}

/** gofl perf. It writes files into out and prints no JSON, so there is no -format. */
export function perfRunArgs(o: PerfOptions): string[] {
  // An old setting started with "perf"; the subcommand is added here.
  const extra = (o.extra ?? []).filter((x, i) => !(i === 0 && x === 'perf'));
  const a = ['perf', '-dir', o.dir, '-out', o.out, ...extra];
  if (o.base && o.scaled) {
    a.push('-base', o.base, '-scaled', o.scaled);
  } else if (o.bench) {
    a.push('-bench', o.bench);
    if (o.sizes?.trim()) a.push('-sizes', o.sizes.trim());
  }
  return a;
}
