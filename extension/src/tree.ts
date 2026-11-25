// The side-bar tree: the files gofl suspects (as given to the Agent), the
// Agent's own localisation, what changed since the analysis, and the full
// ranking from the report.
import * as vscode from 'vscode';
import { Location, Report } from './model';
import { byRank } from './parse';
import { Session, shortFunc, tagOf } from './notes';

export class Node extends vscode.TreeItem {
  constructor(label: string, public children: Node[] = [], loc?: Location, command?: vscode.Command) {
    super(label, children.length ? vscode.TreeItemCollapsibleState.Collapsed : vscode.TreeItemCollapsibleState.None);
    if (loc) {
      this.command = { command: 'gofl.openLocation', title: 'Open', arguments: [loc] };
      this.tooltip = loc.func ? `${loc.func}\n${where(loc)}` : where(loc);
    }
    if (command) this.command = command;
  }
}

const where = (l: Location) => `${l.file}${l.line ? ':' + l.line : ''}`;
const base = (f: string) => f.split('/').pop() ?? f;
const short = (l: Location) => `${base(l.file)}${l.line ? ':' + l.line : ''}`;

export interface TreeInput {
  report?: Report;
  session?: Session;
  /** Number of changed hunks per project-relative file, since the analysis. */
  changes: Record<string, number>;
}

function expanded(n: Node): Node {
  n.collapsibleState = vscode.TreeItemCollapsibleState.Expanded;
  return n;
}

export function buildTree(i: TreeInput): Node[] {
  const out: Node[] = [];
  const s = i.session;
  const tag = tagOf(s?.command ?? 'crash');

  if (s) {
    const files = s.files.map((f, idx) => {
      const kids = f.funcs.map((fn) => {
        const n = new Node(shortFunc(fn.func) || '(function)', [], { file: f.file, line: fn.line, func: fn.func });
        n.description = fn.line ? `line ${fn.line}` : '';
        return n;
      });
      const n = new Node(`#${idx + 1}  ${base(f.file)}`, kids, { file: f.file });
      n.description = `${f.file} · score ${f.score.toFixed(2)}`;
      return n;
    });
    const mode = s.mode === 'E0' ? ' (E0: not given to the Agent)' : s.mode === 'E1' ? ' (given to the Agent)' : '';
    out.push(expanded(new Node(`gofl ${tag} files${mode}`, files)));

    if (s.agent?.length) {
      const locs = s.agent.map((l) => {
        const n = new Node(l.func ? shortFunc(l.func) : base(l.file), [], l);
        n.description = short(l);
        return n;
      });
      out.push(expanded(new Node("Agent's localisation", locs)));
    } else {
      out.push(new Node("Agent's localisation: waiting for gofl-localize.json"));
    }

    const changed = Object.entries(i.changes).filter(([, n]) => n > 0);
    if (changed.length) {
      const items = changed.map(([file, n]) => {
        const node = new Node(base(file), [], { file });
        node.description = `${n} change${n > 1 ? 's' : ''} · ${file}`;
        return node;
      });
      const head = expanded(new Node(`Agent's changes (${changed.length} file${changed.length > 1 ? 's' : ''})`, items));
      head.command = { command: 'gofl.showChanges', title: 'Compare' };
      out.push(head);
    } else {
      out.push(new Node('Agent has not changed the suspect files yet'));
    }
    if (s.prompt) {
      const n = new Node('Agent prompt', [], undefined, { command: 'gofl.showPrompt', title: 'Show' });
      n.description = `${s.mode ?? ''} · click to open`;
      out.push(n);
    }
  }

  const r = i.report;
  if (r) {
    const findings = byRank(r).map((f) => {
      const kids: Node[] = [];
      for (const e of f.evidence ?? []) kids.push(new Node(`${e.kind}: ${e.detail}`));
      if (f.flow?.length) {
        const steps = f.flow.map((l, k) => {
          const n = new Node(`${k + 1}. ${l.func ? shortFunc(l.func) : '(?)'}`, [], l);
          n.description = short(l);
          return n;
        });
        kids.push(new Node(`Call path (${steps.length} steps)`, steps));
      }
      const n = new Node(`#${f.rank}  ${f.location.func ? shortFunc(f.location.func) : short(f.location)}`, kids, f.location);
      n.description = `${short(f.location)} · score ${f.score.toFixed(2)}`;
      return n;
    });
    out.push(new Node(`gofl ${r.command} ranking · ${r.findings.length} locations`, findings));
    if (r.notes?.length) out.push(new Node('Limits', r.notes.map((t) => new Node(t))));
  }
  return out;
}

export class GoflTree implements vscode.TreeDataProvider<Node> {
  private roots: Node[] = [];
  private emitter = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this.emitter.event;

  show(i: TreeInput): void {
    this.roots = buildTree(i);
    this.emitter.fire(undefined);
  }
  getTreeItem(n: Node): vscode.TreeItem {
    return n;
  }
  getChildren(n?: Node): Node[] {
    return n ? n.children : this.roots;
  }
}
