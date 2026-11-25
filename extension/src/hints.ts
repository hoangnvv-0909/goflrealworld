// Turns a ranked report into "the files to look at": the same reduction as
// agentprompt.Hints in Go (first N files in rank order, at most M functions each).
// gofl crash gets its files from `-agent-prompt -format json`; this is for
// commands that have no Agent prompt yet (gofl perf), so they can use the same
// visual notes.
import { FileHint, Report } from './model';
import { byRank } from './parse';

export function filesFromReport(r: Report, nFiles = 3, nFuncs = 3): FileHint[] {
  const out: FileHint[] = [];
  const at = new Map<string, number>();
  for (const f of byRank(r)) {
    let i = at.get(f.location.file);
    if (i === undefined) {
      if (out.length >= nFiles) continue;
      i = out.length;
      at.set(f.location.file, i);
      out.push({ file: f.location.file, score: f.score, funcs: [] });
    }
    if (out[i].funcs.length < nFuncs) {
      out[i].funcs.push({ func: f.location.func ?? '', line: f.location.line, score: f.score });
    }
  }
  return out;
}
