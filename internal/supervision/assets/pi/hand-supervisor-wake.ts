// Hand-owned supervisor wake integration (hand supervision). Managed by hand
// init; edits are replaced on the next init. Placeholders __HAND_EXECUTABLE__
// and __HAND_HOME__ are substituted as JSON string literals at install time.
//
// Converts one Hand wake into exactly one Pi reasoning turn, forever while the
// session lives: the extension arms one `hand supervision wait` child whenever
// the agent is settled, delivers an eligible wake as a mechanism message that
// triggers a turn (never a fabricated operator/user message), and the settled
// event after the resulting turn re-arms the next cycle. session_start and
// session_shutdown carry generation ownership, so /new, /resume, /fork, reload,
// and quit retire stale callbacks instead of letting a retired wait deliver
// into a successor session.

import { spawn } from "node:child_process";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const HAND_EXE = __HAND_EXECUTABLE__;
const HAND_HOME = __HAND_HOME__;
const WAKE_SCHEMA = "hand.supervision.wake.v1";

// parseWake accepts only the versioned machine protocol; anything else fails
// closed rather than improvising on rendered human output.
export function parseWake(
  stdout: string,
): { schema: string; fleet_id: string; message?: string; episodes?: Array<{ key: string }> } | null {
  let parsed: unknown;
  try {
    parsed = JSON.parse(stdout);
  } catch {
    return null;
  }
  const record = parsed as { schema?: unknown; fleet_id?: unknown };
  if (!record || typeof record !== "object" || record.schema !== WAKE_SCHEMA || typeof record.fleet_id !== "string") {
    return null;
  }
  return parsed as { schema: string; fleet_id: string; message?: string; episodes?: Array<{ key: string }> };
}

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

  const receipt = (stage: string, episodes: Array<{ key: string }>, detail?: string) => {
    const argv = ["supervision", "receipt", "--host", "pi", "--stage", stage];
    for (const episode of episodes || []) {
      argv.push("--episode", episode.key);
    }
    if (detail) {
      argv.push("--detail", String(detail).slice(0, 200));
    }
    try {
      spawn(HAND_EXE, argv, {
        cwd: HAND_HOME,
        env: { ...process.env, HAND_HOME: HAND_HOME },
        stdio: "ignore",
      });
    } catch {}
  };

  const arm = () => {
    if (child) return;
    const started = generation;
    const startedChild = spawn(
      HAND_EXE,
      ["supervision", "wait", "--host", "pi", "--format", "json", "--timeout", "30m"],
      {
        cwd: HAND_HOME,
        env: { ...process.env, HAND_HOME: HAND_HOME },
        stdio: ["ignore", "pipe", "pipe"],
      },
    );
    child = startedChild;

    let stdout = "";
    let stderr = "";
    startedChild.stdout?.on("data", (chunk: Buffer) => {
      stdout += chunk;
    });
    startedChild.stderr?.on("data", (chunk: Buffer) => {
      stderr += chunk;
    });

    startedChild.on("close", (code) => {
      if (generation !== started || child !== startedChild) return;
      child = null;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      // 0 eligible wake, 4 checkpoint without one: the next agent_settled
      // re-arms either way. 3 means another live runtime owns the bridge.
      // 8 interrupted / 9 replaced mean another Hand process took over.
      if (code === 0) {
        const payload = parseWake(stdout);
        if (!payload) {
          process.stderr.write(
            "hand supervisor wake: unexpected wait output schema; run `hand init " +
              HAND_HOME +
              "` to refresh this integration\n",
          );
          return;
        }
        void pi
          .sendMessage(
            {
              customType: "hand-supervisor-wake",
              content:
                payload.message && payload.message.trim() !== ""
                  ? payload.message
                  : "Hand has current actionable work. Run `hand orient` before reasoning or acting.",
              display: true,
            },
            { triggerTurn: true, deliverAs: "followUp" },
          )
          .then(() => receipt("accepted", payload.episodes ?? []))
          .catch((err) => {
            process.stderr.write("hand supervisor wake: mechanism message failed: " + err + "\n");
            receipt("delivery-failed", [], err);
          });
      } else if (code === 5 && stderr.trim() !== "") {
        process.stderr.write("hand supervisor wake: " + stderr.trim().split("\n")[0] + "\n");
      }
    });

    timer = setTimeout(() => {
      if (child === startedChild) startedChild.kill();
    }, 31 * 60 * 1000);
  };

  pi.on?.("session_start", () => {
    generation += 1;
    arm();
  });

  pi.on?.("session_shutdown", () => {
    generation += 1;
    stopChild();
  });

  // The settled signal is what makes supervision continuous instead of
  // one-shot: after every turn - including the turn a wake triggered - the
  // next wait is armed here without model memory.
  pi.on?.("agent_settled", () => {
    arm();
  });
}
