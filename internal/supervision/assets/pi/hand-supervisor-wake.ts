// Hand-owned supervisor wake integration (hand supervision). Managed by hand
// init; edits are replaced on the next init. Placeholders __HAND_EXECUTABLE__
// and __HAND_HOME__ are substituted at install time.
//
// Converts one Hand wake into exactly one Pi reasoning turn: while the primary
// session is settled, this extension arms at most one `hand supervision wait
// --host pi` child; an eligible wake is delivered as a mechanism follow-up
// message that triggers a turn, never as a fabricated operator answer.
// session_shutdown covers ordinary same-process replacements (/new, /resume,
// /fork, reload), so a retired generation's callbacks are no-ops.

import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const HAND_EXE = "__HAND_EXECUTABLE__";
const HAND_HOME = "__HAND_HOME__";
const WAIT_TIMEOUT_MS = 30 * 60 * 1000;

export default function (pi: ExtensionAPI) {
  let generation = 0;
  let child: ReturnType<typeof spawn> | null = null;
  let timer: ReturnType<typeof setTimeout> | null = null;

  const stopChild = () => {
    if (timer) {
      clearTimeout(timer);
      timer = null;
    }
    if (child) {
      child.kill();
      child = null;
    }
  };

  const wakeText = (stdout: string): string => {
    for (const line of stdout.split("\n")) {
      if (line.startsWith("text: ")) return line.slice(6).trim();
    }
    return "";
  };

  const arm = () => {
    if (child) return;
    const current = generation;
    const started = spawn(
      HAND_EXE,
      ["supervision", "wait", "--host", "pi", "--timeout", "30m"],
      { cwd: HAND_HOME, env: { ...process.env, HAND_HOME: HAND_HOME }, stdio: ["ignore", "pipe", "pipe"] },
    );
    child = started;

    let stdout = "";
    started.stdout?.on("data", (chunk: Buffer) => {
      stdout += chunk;
    });

    started.on("close", (code) => {
      if (generation !== current || child !== started) return;
      child = null;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      // 0 eligible wake, 4 checkpoint with no wake: both re-arm via
      // session_start/turn-end arming. 8 interrupted and 9 replaced mean
      // another Hand process owns coordination now; do not fight it.
      if (code === 0) {
        void pi
          .sendUserMessage(
            wakeText(stdout) ||
              "Hand has current actionable work. Run `hand orient` before reasoning or acting.",
            { deliverAs: "followUp" },
          )
          .catch((err) => {
            process.stderr.write("hand supervisor wake: follow-up failed: " + err + "\n");
          });
      } else if (code === 5) {
        process.stderr.write(
          "hand supervisor wake: monitoring failed; run `hand doctor` in " + HAND_HOME + "\n",
        );
      }
    });

    timer = setTimeout(() => {
      if (child === started) started.kill();
    }, WAIT_TIMEOUT_MS);
  };

  pi.on?.("session_start", () => {
    generation += 1;
    arm();
  });

  pi.on?.("session_shutdown", () => {
    generation += 1;
    stopChild();
  });
}
