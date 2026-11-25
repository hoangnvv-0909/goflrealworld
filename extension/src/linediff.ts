// Line diff between the text of a file when gofl analysed it (the snapshot) and
// its text now. No vscode import, so it is tested with node --test.

export interface Hunk {
  /** 0-based first line of the hunk in the old text, and how many lines it had. */
  oldStart: number;
  oldCount: number;
  /** 0-based first line in the new text, and how many lines it has. */
  newStart: number;
  newCount: number;
  oldLines: string[];
}

/** Above this many cells the LCS table is skipped and the middle is one hunk. */
const MAX_CELLS = 4_000_000;

const lines = (t: string): string[] => {
  const l = t.replace(/\r\n/g, '\n').split('\n');
  if (l.length && l[l.length - 1] === '') l.pop(); // text ends with a newline
  return l;
};

/** Hunks that turn `oldText` into `newText`, in file order. */
export function diffLines(oldText: string, newText: string): Hunk[] {
  const a = lines(oldText);
  const b = lines(newText);
  let pre = 0;
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++;
  let suf = 0;
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++;
  const ma = a.slice(pre, a.length - suf);
  const mb = b.slice(pre, b.length - suf);
  if (ma.length === 0 && mb.length === 0) return [];
  if (ma.length === 0 || mb.length === 0 || ma.length * mb.length > MAX_CELLS) {
    return [{ oldStart: pre, oldCount: ma.length, newStart: pre, newCount: mb.length, oldLines: ma }];
  }
  // LCS table over the middle part.
  const n = ma.length;
  const m = mb.length;
  const t: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      t[i][j] = ma[i] === mb[j] ? t[i + 1][j + 1] + 1 : Math.max(t[i + 1][j], t[i][j + 1]);
    }
  }
  const out: Hunk[] = [];
  let i = 0;
  let j = 0;
  let cur: Hunk | undefined;
  const flush = () => {
    if (cur) out.push(cur);
    cur = undefined;
  };
  while (i < n || j < m) {
    if (i < n && j < m && ma[i] === mb[j]) {
      flush();
      i++;
      j++;
      continue;
    }
    cur ??= { oldStart: pre + i, oldCount: 0, newStart: pre + j, newCount: 0, oldLines: [] };
    if (j < m && (i === n || t[i][j + 1] >= t[i + 1][j])) {
      cur.newCount++;
      j++;
    } else {
      cur.oldCount++;
      cur.oldLines.push(ma[i]);
      i++;
    }
  }
  flush();
  return out;
}
