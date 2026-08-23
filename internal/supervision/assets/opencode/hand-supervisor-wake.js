// Hand-owned supervisor wake integration (hand supervision). Managed by hand
// init; edits are replaced on the next init. Placeholders __HAND_EXECUTABLE__
// and __HAND_HOME__ are substituted at install time.
//
// Converts one Hand wake into exactly one OpenCode reasoning turn: after the
// primary session goes idle, this plugin arms at most one `hand supervision
// wait --host opencode` child; an eligible wake is delivered through the
// synchronous session prompt, so acceptance means real assistant activity,
// never a fire-and-forget enqueue. Stale session generations become no-ops.

import { spawn } from "node:child_process";

const HAND_EXE = "__HAND_EXECUTABLE__";
const HAND_HOME = "__HAND_HOME__";
const COORDINATOR_KEY = "__handSupervisorWake";
const WAIT_TIMEOUT_MS = 30 * 60 * 1000;

export const HandSupervisorWake = async ({ client }) => {
  const coordinator = {
    generation: 0,
    armedFor: null,
    child: null,
    timer: null,
  };
  globalThis[COORDINATOR_KEY] = coordinator;

  const stopChild = () => {
    if (coordinator.timer) {
      clearTimeout(coordinator.timer);
      coordinator.timer = null;
    }
    if (coordinator.child) {
      coordinator.child.kill();
      coordinator.child = null;
    }
    coordinator.armedFor = null;
  };

  const wakeText = (stdout) => {
    for (const line of stdout.split("\n")) {
      if (line.startsWith("text: ")) return line.slice(6).trim();
    }
    return "";
  };

  const deliver = async (sessionID, text) => {
    const prompt =
      text ||
      "Hand has current actionable work. Run `hand orient` before reasoning or acting.";
    // Synchronous prompt semantics only: awaiting the response is what makes
    // delivery progressed evidence. promptAsync/204 is explicitly not proof a
    // model turn started.
    await client.session.prompt({
      path: { id: sessionID },
      body: { parts: [{ type: "text", text: prompt }] },
    });
  };

  const arm = (sessionID) => {
    if (coordinator.armedFor === sessionID && coordinator.child) return;
    if (coordinator.armedFor !== null) stopChild();
    coordinator.armedFor = sessionID;

    const child = spawn(
      HAND_EXE,
      ["supervision", "wait", "--host", "opencode", "--timeout", "30m"],
      { cwd: HAND_HOME, env: { ...process.env, HAND_HOME: HAND_HOME }, stdio: ["ignore", "pipe", "pipe"] },
    );
    coordinator.child = child;

    let stdout = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });

    const generation = coordinator.generation;
    child.on("close", (code, signal) => {
      if (coordinator.generation !== generation || coordinator.child !== child) return;
      coordinator.child = null;
      if (coordinator.timer) {
        clearTimeout(coordinator.timer);
        coordinator.timer = null;
      }
      // 0 eligible wake, 4 checkpoint with no wake: both re-arm on the next
      // idle event. 8 interrupted and 9 replaced mean another Hand process now
      // owns coordination; arming again here would fight it.
      if (code === 0 || code === 4) {
        if (code === 0) {
          deliver(sessionID, wakeText(stdout)).catch((err) => {
            process.stderr.write("hand supervisor wake: prompt failed: " + err + "\n");
          });
        }
        coordinator.armedFor = null;
        return;
      }
      if (code === 5) {
        process.stderr.write(
          "hand supervisor wake: monitoring failed; run `hand doctor` in " + HAND_HOME + "\n",
        );
      }
      coordinator.armedFor = null;
    });

    coordinator.timer = setTimeout(() => {
      if (coordinator.child === child) child.kill();
    }, WAIT_TIMEOUT_MS);
  };

  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;
      const sessionID = event.properties?.sessionID;
      if (!sessionID) return;
      // A different session ID is a successor generation (/new, switch,
      // replacement): its callbacks retire the previous arm instead of
      // targeting it.
      if (coordinator.armedFor !== null && coordinator.armedFor !== sessionID) {
        coordinator.generation += 1;
        stopChild();
      }
      if (coordinator.child) return;
      arm(sessionID);
    },
  };
};
