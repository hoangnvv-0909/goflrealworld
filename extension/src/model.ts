// Mirror of internal/finding/finding.go (docs/CONTRACT.md section 1).
// Keep in sync by hand: any change there bumps finding.SchemaVersion.

/** finding.SchemaVersion this extension understands. */
export const SCHEMA_VERSION = 1;

export interface Location {
  file: string; // slash-separated, relative to the project root
  line?: number;
  col?: number;
  func?: string;
}

export interface Evidence {
  kind: string;
  detail: string;
}

export interface Finding {
  location: Location;
  score: number;
  rank: number;
  evidence?: Evidence[];
  flow?: Location[];
}

/** The commands whose reports the extension understands. */
export type Command = 'crash' | 'perf';

export interface Report {
  version: number;
  command: Command;
  findings: Finding[];
  notes?: string[];
}

/** One function the Agent was pointed at (internal/agentprompt FuncHint). */
export interface FuncHint {
  func: string;
  line?: number;
  score: number;
}

/** One of the top files given to the Agent (internal/agentprompt FileHint). */
export interface FileHint {
  file: string;
  score: number;
  funcs: FuncHint[];
}

/** Output of `gofl crash -agent-prompt -format json` (cmd/gofl agentPromptDoc). */
export interface AgentPrompt {
  version: number;
  mode: 'E0' | 'E1';
  prompt: string;
  events: number;
  groups: number;
  files: FileHint[];
}
