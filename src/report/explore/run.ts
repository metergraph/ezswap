// runExplorer: the thin layer that owns the terminal. It switches to the
// alternate screen, hides the cursor and puts stdin in raw mode; reads keys
// into the pure reducer; writes each frame from renderScreen in one write;
// redraws when the terminal is resized. Whatever ends it (q, Ctrl-C, SIGINT,
// SIGTERM, an error while drawing, an uncaught exception) restores the
// terminal first, once.

import type { RunFile } from "../../types.ts";
import { type ExplorerOptions, type ExplorerState, explorerData, initialState, parseKeys, reduce, resize } from "./state.ts";
import { renderScreen } from "./screen.ts";

export interface ExplorerInput {
  isTTY?: boolean;
  setRawMode?(on: boolean): unknown;
  setEncoding?(encoding: BufferEncoding): unknown;
  on(event: "data", listener: (chunk: string | Buffer) => void): unknown;
  off(event: "data", listener: (chunk: string | Buffer) => void): unknown;
  resume(): unknown;
  pause(): unknown;
}

export interface ExplorerOutput {
  write(text: string): unknown;
  columns?: number;
  rows?: number;
  on?(event: "resize", listener: () => void): unknown;
  off?(event: "resize", listener: () => void): unknown;
}

/** The process hooks the explorer installs; injected in tests. */
export interface ExplorerProcess {
  on(event: string, listener: (...args: unknown[]) => void): unknown;
  off(event: string, listener: (...args: unknown[]) => void): unknown;
}

export interface ExplorerIo {
  stdin: ExplorerInput;
  stdout: ExplorerOutput;
  process?: ExplorerProcess;
  color: boolean;
  ascii: boolean;
  /** Called before each frame is drawn; tests use it to inject a failure. */
  beforeFrame?: (state: ExplorerState) => void;
}

export interface ExplorerResult {
  /** "quit" for q or Ctrl-C; the signal name when one ended it. */
  reason: "quit" | "SIGINT" | "SIGTERM";
  state: ExplorerState;
}

export const ENTER = "\x1b[?1049h\x1b[?25l\x1b[?7l\x1b[H\x1b[2J";
export const LEAVE = "\x1b[0m\x1b[?7h\x1b[?25h\x1b[?1049l";

/** One frame as terminal output: home the cursor, then every row placed explicitly. */
export function frameOutput(frame: string): string {
  return `\x1b[H${frame
    .split("\n")
    .map((line, i) => `\x1b[${i + 1};1H${line}\x1b[0m`)
    .join("")}`;
}

export function runExplorer(run: RunFile, io: ExplorerIo, options: ExplorerOptions & { tab?: number } = {}): Promise<ExplorerResult> {
  const data = explorerData(run, { ...options, ascii: io.ascii });
  const size = () => ({ columns: io.stdout.columns || 80, rows: io.stdout.rows || 24 });
  let state = initialState(data, size(), options.tab ?? 0);
  const proc = io.process ?? process;

  return new Promise<ExplorerResult>((resolve, reject) => {
    let done = false;
    const draw = () => {
      io.beforeFrame?.(state);
      const { columns, rows } = size();
      io.stdout.write(frameOutput(renderScreen(state, { columns, rows, color: io.color, ascii: io.ascii })));
    };
    const restore = () => {
      io.stdin.off("data", onData);
      io.stdout.off?.("resize", onResize);
      proc.off("SIGINT", onSigint);
      proc.off("SIGTERM", onSigterm);
      proc.off("uncaughtException", onFatal);
      proc.off("unhandledRejection", onFatal);
      try {
        io.stdin.setRawMode?.(false);
      } catch {
        // The terminal may already be gone; restoring the screen still matters.
      }
      io.stdin.pause();
      io.stdout.write(LEAVE);
    };
    const finish = (reason: ExplorerResult["reason"]) => {
      if (done) return;
      done = true;
      restore();
      resolve({ reason, state });
    };
    const fail = (error: unknown) => {
      if (done) return;
      done = true;
      restore();
      reject(error instanceof Error ? error : new Error(String(error)));
    };
    const guard = (fn: () => void) => {
      try {
        fn();
      } catch (error) {
        fail(error);
      }
    };
    function onData(chunk: string | Buffer) {
      guard(() => {
        for (const key of parseKeys(typeof chunk === "string" ? chunk : chunk.toString("utf8"))) {
          state = reduce(state, key);
          if (state.quit) return finish("quit");
        }
        draw();
      });
    }
    function onResize() {
      guard(() => {
        const { columns, rows } = size();
        state = resize(state, columns, rows);
        draw();
      });
    }
    function onSigint() {
      finish("SIGINT");
    }
    function onSigterm() {
      finish("SIGTERM");
    }
    function onFatal(error: unknown) {
      fail(error);
    }

    proc.on("SIGINT", onSigint);
    proc.on("SIGTERM", onSigterm);
    proc.on("uncaughtException", onFatal);
    proc.on("unhandledRejection", onFatal);
    guard(() => {
      io.stdin.setEncoding?.("utf8");
      io.stdin.setRawMode?.(true);
      io.stdin.on("data", onData);
      io.stdin.resume();
      io.stdout.on?.("resize", onResize);
      io.stdout.write(ENTER);
      draw();
    });
  });
}
