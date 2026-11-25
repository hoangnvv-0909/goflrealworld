// gofl VS Code extension.
//
//  1. Take a crash log (selection, clipboard, a file, or a test run) and have
//     gofl turn it into the prompt for a repair Agent.
//  2. Mark the files gofl suspects, show where the Agent says the fault is
//     (gofl-localize.json), and show what the Agent changed since the analysis.
//
// All decisions that can be made without VS Code live in the other modules
// (parse, notes, linediff, args, ...) and are tested with `node --test`.
// gofl perf plugs in through the same Session: see features at the end.
import * as fs from 'fs';
import * as vscode from 'vscode';
import { crashPromptArgs, crashReportArgs, crashRunArgs } from './args';
import { Notes } from './decorations';
import { filesFromReport } from './hints';
import { diffLines } from './linediff';
import { LOCALIZE_FILE, parseLocalize } from './localize';
import { AgentPrompt, Location, Report } from './model';
import { noteFiles, Session } from './notes';
import { parseAgentPrompt, parseReport, ReportError } from './parse';
import { relativeToRoot, resolveFile } from './paths';
import { execGofl, firstLine } from './runner';
import { GoflTree } from './tree';

const SNAP = 'gofl-snapshot';
const KEY_SESSION = 'gofl.session';
const KEY_REPORT = 'gofl.report';
const KEY_REPRO = 'gofl.lastRepro';

interface CrashSource {
  label: string;
  /** Crash text for stdin. */
  text?: string;
  /** Extra gofl arguments (the test run, instead of stdin). */
  args?: string[];
}

export function activate(ctx: vscode.ExtensionContext): void {
  const out = vscode.window.createOutputChannel('gofl');
  const tree = new GoflTree();
  let session: Session | undefined = ctx.workspaceState.get<Session>(KEY_SESSION);
  let report: Report | undefined = ctx.workspaceState.get<Report>(KEY_REPORT);

  const roots = () => (vscode.workspace.workspaceFolders ?? []).map((f) => f.uri.fsPath);
  const cfg = () => vscode.workspace.getConfiguration('gofl');
  const notes = new Notes(() => session, roots);

  // Text of a project file now: the editor's (may be unsaved) or the disk's.
  const currentText = (rel: string): string | undefined => {
    const abs = resolveFile(rel, roots());
    if (!abs) return undefined;
    const open = vscode.workspace.textDocuments.find((d) => d.uri.fsPath === abs);
    if (open) return open.getText();
    try {
      return fs.readFileSync(abs, 'utf8');
    } catch {
      return undefined;
    }
  };

  const changes = (): Record<string, number> => {
    const r: Record<string, number> = {};
    if (!session) return r;
    for (const f of Object.keys(session.snapshots)) {
      const now = currentText(f);
      if (now !== undefined) r[f] = diffLines(session.snapshots[f], now).length;
    }
    return r;
  };

  const refresh = () => {
    tree.show({ report, session, changes: changes() });
    notes.refreshAll();
  };
  let treeTimer: NodeJS.Timeout | undefined;
  const refreshSoon = () => {
    clearTimeout(treeTimer);
    treeTimer = setTimeout(refresh, 400);
  };

  const save = async () => {
    await ctx.workspaceState.update(KEY_SESSION, session);
    await ctx.workspaceState.update(KEY_REPORT, report);
  };

  const showError = (msg: string) => {
    out.appendLine(`error: ${msg}`);
    return vscode.window.showErrorMessage(`gofl: ${msg}`, 'Show log').then((b) => b && out.show(true));
  };

  const run = async (args: string[], cwd: string, stdin?: string) => {
    const bin = cfg().get<string>('path', 'gofl');
    out.appendLine(`$ ${bin} ${args.join(' ')}${stdin ? `   (stdin: ${stdin.length} chars)` : ''}`);
    const r = await execGofl(bin, args, { cwd, stdin });
    if (r.stderr) out.appendLine(r.stderr.trimEnd());
    if (r.notFound) {
      await showError(`"${bin}" not found. Install gofl or set gofl.path.`);
      return undefined;
    }
    if (r.timedOut) {
      await showError('gofl timed out.');
      return undefined;
    }
    return r;
  };

  const pickRoot = async (): Promise<string | undefined> => {
    const rs = roots();
    if (rs.length === 0) {
      await showError('open the Go project folder first (the folder where the crash happened).');
      return undefined;
    }
    if (rs.length === 1) return rs[0];
    return (await vscode.window.showQuickPick(rs, { placeHolder: 'Which project is the crash from?' })) ?? undefined;
  };

  // ---- 1. crash log -> Agent prompt -------------------------------------

  const pickSource = async (preferSelection: boolean): Promise<CrashSource | undefined> => {
    const ed = vscode.window.activeTextEditor;
    const selected = ed && !ed.selection.isEmpty ? ed.document.getText(ed.selection) : '';
    if (preferSelection && selected) return { label: 'selection', text: selected };
    const items: (vscode.QuickPickItem & { id: string })[] = [
      ...(selected ? [{ id: 'selection', label: '$(selection) Selected text', description: `${selected.length} characters` }] : []),
      { id: 'clipboard', label: '$(clippy) Clipboard', description: 'a stack trace you copied, e.g. from a terminal' },
      { id: 'file', label: '$(file) A log file…', description: 'a log, or the output of go test -json' },
      ...(ed && !selected ? [{ id: 'editor', label: '$(file-text) The open file', description: ed.document.fileName }] : []),
      { id: 'tests', label: '$(beaker) Run go test now…', description: 'gofl runs the tests and reads the crash from them' },
    ];
    const pick = await vscode.window.showQuickPick(items, { placeHolder: 'Where is the crash log?' });
    if (!pick) return undefined;
    switch (pick.id) {
      case 'selection':
        return { label: 'selection', text: selected };
      case 'editor':
        return { label: 'open file', text: ed!.document.getText() };
      case 'clipboard': {
        const t = await vscode.env.clipboard.readText();
        if (!t.trim()) {
          await showError('the clipboard is empty.');
          return undefined;
        }
        return { label: 'clipboard', text: t };
      }
      case 'file': {
        const f = await vscode.window.showOpenDialog({ canSelectMany: false, title: 'Crash log' });
        if (!f?.[0]) return undefined;
        return { label: f[0].fsPath, text: Buffer.from(await vscode.workspace.fs.readFile(f[0])).toString('utf8') };
      }
      default: {
        const pkgs = await vscode.window.showInputBox({ prompt: 'Packages to test', value: './...' });
        if (pkgs === undefined) return undefined;
        const re = await vscode.window.showInputBox({ prompt: 'Only tests matching this regexp (optional)', placeHolder: '^TestNth$' });
        if (re === undefined) return undefined;
        return { label: 'go test', args: crashRunArgs(pkgs, re) };
      }
    }
  };

  const snapshotFiles = (files: string[]): Record<string, string> => {
    const snaps: Record<string, string> = {};
    for (const f of files) {
      const t = currentText(f);
      if (t !== undefined) snaps[f] = t;
    }
    return snaps;
  };

  const analyze = async (preferSelection: boolean) => {
    const cwd = await pickRoot();
    if (!cwd) return;
    const src = await pickSource(preferSelection);
    if (!src) return;

    // A log does not say how to reproduce the crash; the Agent needs it to check its fix.
    let repro: string[] = cfg().get<string[]>('repro', []);
    if (!src.args && repro.length === 0) {
      const last = ctx.workspaceState.get<string>(KEY_REPRO, '');
      const r = await vscode.window.showInputBox({
        prompt: 'Command that reproduces the crash (optional, the Agent uses it to check its fix)',
        placeHolder: 'go test ./pkg -run TestName',
        value: last,
      });
      if (r === undefined) return;
      await ctx.workspaceState.update(KEY_REPRO, r);
      repro = r.trim() ? [r.trim()] : [];
    }

    const extra = [...cfg().get<string[]>('crashArgs', []), ...(src.args ?? [])];
    const mode = cfg().get<string>('agentMode', 'e1') === 'e0' ? 'e0' : 'e1';
    const done = await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `gofl: analysing the crash (${src.label})…` },
      async () => {
        const p = await run(crashPromptArgs({ dir: cwd, extra, mode, repro }), cwd, src.text);
        if (!p) return undefined;
        if (p.code !== 0) {
          await showError(firstLine(p));
          return undefined;
        }
        let prompt: AgentPrompt;
        try {
          prompt = parseAgentPrompt(p.stdout);
        } catch (e) {
          await showError(`cannot read gofl's answer: ${e instanceof ReportError ? e.message : String(e)}`);
          return undefined;
        }
        // The ranking for the tree. Its failure does not stop the prompt.
        let rep: Report | undefined;
        const r = await run(crashReportArgs({ dir: cwd, extra }), cwd, src.text);
        if (r && r.code === 0) {
          try {
            rep = parseReport(r.stdout);
          } catch (e) {
            out.appendLine(`ranking not shown: ${(e as Error).message}`);
          }
        }
        return { prompt, rep };
      },
    );
    if (!done) return;

    session = {
      command: 'crash',
      mode: done.prompt.mode,
      createdAt: new Date().toISOString(),
      files: done.prompt.files,
      snapshots: snapshotFiles(done.prompt.files.map((f) => f.file)),
      prompt: done.prompt.prompt,
    };
    report = done.rep;
    await loadAgent(false);
    await save();
    refresh();
    await vscode.commands.executeCommand('goflResults.focus');
    await showPrompt();
    const missing = done.prompt.files.filter((f) => !(f.file in session!.snapshots)).map((f) => f.file);
    if (missing.length) {
      vscode.window.showWarningMessage(`gofl: not found in the open folder: ${missing.join(', ')}. Open the project root where the crash happened.`);
    }
    const again = done.prompt.files.length === 0 ? ' No suspect file was found in this log.' : '';
    const pick = await vscode.window.showInformationMessage(
      `gofl: Agent prompt ready (${done.prompt.mode}, ${done.prompt.events} crash event(s), ${done.prompt.files.length} suspect file(s)).${again}`,
      'Copy prompt',
    );
    if (pick === 'Copy prompt') await copyPrompt();
  };

  const showPrompt = async () => {
    if (!session?.prompt) {
      await showError('there is no prompt yet. Run "gofl: Generate Agent prompt from a crash log".');
      return;
    }
    const doc = await vscode.workspace.openTextDocument({ language: 'markdown', content: session.prompt });
    await vscode.window.showTextDocument(doc, { preview: true, viewColumn: vscode.ViewColumn.Beside });
  };

  const copyPrompt = async () => {
    if (!session?.prompt) {
      await showError('there is no prompt yet. Run "gofl: Generate Agent prompt from a crash log".');
      return;
    }
    await vscode.env.clipboard.writeText(session.prompt);
    vscode.window.setStatusBarMessage('gofl: Agent prompt copied', 3000);
  };

  // ---- 2. visual notes ----------------------------------------------------

  /** Reads gofl-localize.json (written by the Agent) into the session. */
  const loadAgent = async (announce: boolean) => {
    if (!session) return;
    for (const r of roots()) {
      const p = vscode.Uri.file(`${r}/${LOCALIZE_FILE}`);
      try {
        const text = Buffer.from(await vscode.workspace.fs.readFile(p)).toString('utf8');
        const loc = parseLocalize(text);
        session.agent = loc.locations;
        await save();
        refresh();
        if (announce) vscode.window.setStatusBarMessage(`gofl: the Agent named ${loc.locations.length} location(s)`, 4000);
        return;
      } catch {
        // no file in this root
      }
    }
    if (announce) vscode.window.showInformationMessage(`gofl: ${LOCALIZE_FILE} not found yet. The Agent writes it before it changes any code.`);
  };

  const showChanges = async () => {
    if (!session) {
      await showError('nothing to compare yet. Generate an Agent prompt first.');
      return;
    }
    const ch = changes();
    const files = Object.keys(ch).filter((f) => ch[f] > 0);
    if (files.length === 0) {
      vscode.window.showInformationMessage('gofl: the files gofl marked are unchanged since the analysis.');
      return;
    }
    const open = async (rel: string) => {
      const abs = resolveFile(rel, roots());
      if (!abs) return;
      await vscode.commands.executeCommand(
        'vscode.diff',
        vscode.Uri.from({ scheme: SNAP, path: '/' + rel }),
        vscode.Uri.file(abs),
        `gofl: ${rel} (at analysis ↔ now)`,
      );
    };
    if (files.length === 1) return open(files[0]);
    const pick = await vscode.window.showQuickPick(
      files.map((f) => ({ label: f, description: `${ch[f]} change${ch[f] > 1 ? 's' : ''}` })),
      { placeHolder: 'Which file to compare?' },
    );
    if (pick) await open(pick.label);
  };

  const openLocation = async (l: Location) => {
    const file = resolveFile(l.file, roots());
    if (!file) {
      vscode.window.showWarningMessage(`gofl: ${l.file} not found. Open the folder where gofl was run (the project root).`);
      return;
    }
    const doc = await vscode.workspace.openTextDocument(file);
    const line = Math.min(Math.max(0, (l.line ?? 1) - 1), doc.lineCount - 1);
    const pos = new vscode.Position(line, Math.max(0, (l.col ?? 1) - 1));
    await vscode.window.showTextDocument(doc, { selection: new vscode.Range(pos, pos) });
  };

  // ---- other commands -----------------------------------------------------

  /** Shows a report (from a file, or from gofl perf) with the same notes. */
  const showReport = async (text: string, root: string | undefined) => {
    try {
      report = parseReport(text);
    } catch (e) {
      await showError(`cannot read report: ${e instanceof ReportError ? e.message : String(e)}`);
      return;
    }
    const files = filesFromReport(report);
    session = {
      command: report.command,
      createdAt: new Date().toISOString(),
      files,
      snapshots: root ? snapshotFiles(files.map((f) => f.file)) : {},
    };
    await save();
    refresh();
    await vscode.commands.executeCommand('goflResults.focus');
  };

  const profile = async () => {
    const cwd = await pickRoot();
    if (!cwd) return;
    const args = [...cfg().get<string[]>('perfArgs', []), '-format', 'json'];
    const r = await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: `gofl ${args[0] ?? ''}…` },
      () => run(args, cwd),
    );
    if (!r) return;
    if (!r.stdout.trim()) return void (await showError(firstLine(r)));
    await showReport(r.stdout, cwd);
  };

  const mode = async () => {
    const pick = await vscode.window.showQuickPick(
      [
        { label: 'E1', description: 'crash facts plus the top 3 files gofl suspects', value: 'e1' },
        { label: 'E0', description: 'crash facts only (the control)', value: 'e0' },
      ],
      { placeHolder: 'What should the Agent prompt contain?' },
    );
    if (pick) await cfg().update('agentMode', pick.value, vscode.ConfigurationTarget.Workspace);
  };

  // ---- wiring -------------------------------------------------------------

  const snapshots: vscode.TextDocumentContentProvider = {
    provideTextDocumentContent: (uri) => session?.snapshots[uri.path.replace(/^\//, '')] ?? '',
  };
  const watcher = vscode.workspace.createFileSystemWatcher(`**/${LOCALIZE_FILE}`);
  const onLocalize = () => void loadAgent(false);
  const cmd = vscode.commands.registerCommand;

  ctx.subscriptions.push(
    out,
    notes,
    watcher,
    vscode.window.registerTreeDataProvider('goflResults', tree),
    vscode.workspace.registerTextDocumentContentProvider(SNAP, snapshots),
    watcher.onDidCreate(onLocalize),
    watcher.onDidChange(onLocalize),
    vscode.window.onDidChangeActiveTextEditor(() => notes.refreshAll()),
    vscode.window.onDidChangeVisibleTextEditors(() => notes.refreshAll()),
    vscode.workspace.onDidChangeTextDocument(() => {
      notes.refreshSoon();
      refreshSoon();
    }),
    vscode.workspace.onDidSaveTextDocument(refreshSoon),
    vscode.workspace.onDidChangeConfiguration((e) => e.affectsConfiguration('gofl') && refresh()),
    cmd('gofl.analyzeLog', () => analyze(false)),
    cmd('gofl.analyzeSelection', () => analyze(true)),
    cmd('gofl.copyPrompt', copyPrompt),
    cmd('gofl.showPrompt', showPrompt),
    cmd('gofl.setMode', mode),
    cmd('gofl.showChanges', showChanges),
    cmd('gofl.reloadAgent', () => loadAgent(true)),
    cmd('gofl.clearSession', async () => {
      session = undefined;
      report = undefined;
      await save();
      refresh();
    }),
    cmd('gofl.openLocation', openLocation),
    cmd('gofl.openReport', async () => {
      const pick = await vscode.window.showOpenDialog({ filters: { 'gofl JSON': ['json'] } });
      if (pick?.[0]) await showReport(Buffer.from(await vscode.workspace.fs.readFile(pick[0])).toString('utf8'), roots()[0]);
    }),
    cmd('gofl.profileBenchmark', profile),
  );

  void loadAgent(false);
  refresh();
  out.appendLine(`gofl extension ready. ${session ? `Restored notes for ${noteFiles(session).length} file(s).` : ''}`);
}

export function deactivate(): void {}
