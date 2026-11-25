// Paints the notes of notes.ts on the editors. Decisions live in notes.ts.
import * as vscode from 'vscode';
import { Note, NoteKind, notesFor, Session } from './notes';
import { relativeToRoot } from './paths';

const COLOR: Record<NoteKind, { bg: string; fg: string }> = {
  suspect: { bg: 'gofl.suspectBackground', fg: 'gofl.suspectForeground' },
  agent: { bg: 'gofl.agentBackground', fg: 'gofl.agentForeground' },
  added: { bg: 'gofl.addedBackground', fg: 'gofl.addedForeground' },
  removed: { bg: 'gofl.removedBackground', fg: 'gofl.removedForeground' },
};

interface Types {
  body: vscode.TextEditorDecorationType;
  label: vscode.TextEditorDecorationType;
}

export class Notes implements vscode.Disposable {
  private types = new Map<NoteKind, Types>();
  private timer: NodeJS.Timeout | undefined;

  constructor(private session: () => Session | undefined, private roots: () => string[]) {
    for (const k of Object.keys(COLOR) as NoteKind[]) {
      const c = COLOR[k];
      this.types.set(k, {
        body: vscode.window.createTextEditorDecorationType({
          isWholeLine: true,
          backgroundColor: new vscode.ThemeColor(c.bg),
          overviewRulerColor: new vscode.ThemeColor(c.fg),
          overviewRulerLane: vscode.OverviewRulerLane.Right,
          borderWidth: k === 'removed' ? '0 0 2px 0' : '0',
          borderStyle: 'solid',
          borderColor: new vscode.ThemeColor(c.fg),
        }),
        label: vscode.window.createTextEditorDecorationType({
          after: { color: new vscode.ThemeColor(c.fg), fontStyle: 'italic', margin: '0 0 0 2em' },
        }),
      });
    }
  }

  /** Redraws one editor, now. */
  paint(ed: vscode.TextEditor): void {
    const enabled = vscode.workspace.getConfiguration('gofl').get<boolean>('notes.enabled', true);
    const s = this.session();
    const rel = relativeToRoot(ed.document.uri.fsPath, this.roots());
    const notes: Note[] = enabled && s && rel && ed.document.uri.scheme === 'file' ? notesFor(rel, ed.document.getText(), s) : [];
    const last = Math.max(ed.document.lineCount - 1, 0);
    for (const [kind, t] of this.types) {
      const mine = notes.filter((n) => n.kind === kind);
      ed.setDecorations(
        t.body,
        mine.map((n) => ({
          range: new vscode.Range(Math.min(n.line, last), 0, Math.min(n.endLine, last), 0),
          hoverMessage: new vscode.MarkdownString(n.hover),
        })),
      );
      ed.setDecorations(
        t.label,
        mine.map((n) => {
          const line = Math.min(n.line, last);
          const end = ed.document.lineAt(line).range.end;
          return { range: new vscode.Range(end, end), renderOptions: { after: { contentText: n.label } } };
        }),
      );
    }
  }

  /** Redraws every visible editor, now. */
  refreshAll(): void {
    for (const ed of vscode.window.visibleTextEditors) this.paint(ed);
  }

  /** Redraws every visible editor after a short pause (typing). */
  refreshSoon(): void {
    clearTimeout(this.timer);
    this.timer = setTimeout(() => this.refreshAll(), 250);
  }

  dispose(): void {
    clearTimeout(this.timer);
    for (const t of this.types.values()) {
      t.body.dispose();
      t.label.dispose();
    }
  }
}
