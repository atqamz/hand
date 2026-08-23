// Hand-owned supervisor wake integration (hand supervision). Managed by hand
// init; edits are replaced on the next init. Placeholders __HAND_EXECUTABLE__
// and __HAND_HOME__ are substituted as JSON string literals at install time.
//
// Converts one Hand wake into exactly one OpenCode reasoning turn for the
// primary Fleet supervisor session: after it goes idle this plugin arms at
// most one `hand supervision wait` child; an eligible wake is delivered
// through the synchronous session prompt, so acceptance means real assistant
// activity. Subagent sessions (parentID set) and sessions outside the Fleet
// home never own the bridge, and a live wait owned by another runtime is not
// stolen.

import { spawn } from "node:child_process";

const HAND_EXE = __HAND_EXECUTABLE__;
const HAND_HOME = __HAND_HOME__;
const WAKE_SCHEMA = "hand.supervision.wake.v1";

export const HandSupervisorWake = async ({ client }) => {
  let generation = 0;
  let primary = null;
  let child = null;
  let timer = null;

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

  // parseWake accepts only the versioned machine protocol. Rendered human
  // output is never a contract: an unexpected schema fails closed.
  const parseWake = (stdout) => {
    let parsed;
    try {
      parsed = JSON.parse(stdout);
    } catch {
      return null;
    }
    if (!parsed || parsed.schema !== WAKE_SCHEMA || typeof parsed.fleet_id !== "string") {
      return null;
    }
    return parsed;
  };

  const receipt = (stage, episodes, detail) => {
    const argv = ["supervision", "receipt", "--host", "opencode", "--stage", stage];
    for (const episode of episodes || []) {
      argv.push("--episode", episode);
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

  const deliver = async (sessionID, payload) => {
    const text =
      payload && typeof payload.message === "string" && payload.message.trim() !== ""
        ? payload.message
        : "Hand has current actionable work. Run `hand orient` before reasoning or acting.";
    // Synchronous prompt semantics: awaiting the response is what makes
    // delivery progressed evidence. promptAsync/204 proves nothing.
    await client.session.prompt({
      path: { id: sessionID },
      body: { parts: [{ type: "text", text }] },
    });
    receipt(
      "accepted",
      payload && Array.isArray(payload.episodes) ? payload.episodes.map((e) => e.key) : [],
    );
  };

  const sessionInfo = async (sessionID) => {
    try {
      const res = await client.session.get({ path: { id: sessionID } });
      return (res && res.data) || res;
    } catch {
      return null;
    }
  };

  const arm = (sessionID) => {
    if (child) return;
    const started = generation;
    child = spawn(
      HAND_EXE,
      [
        "supervision",
        "wait",
        "--host",
        "opencode",
        "--format",
        "json",
        "--runtime-session",
        sessionID,
        "--timeout",
        "30m",
      ],
      {
        cwd: HAND_HOME,
        env: { ...process.env, HAND_HOME: HAND_HOME },
        stdio: ["ignore", "pipe", "pipe"],
      },
    );

    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk;
    });

    child.on("close", (code) => {
      if (generation !== started) return;
      child = null;
      if (timer) {
        clearTimeout(timer);
        timer = null;
      }
      // 0 eligible wake, 4 checkpoint without one: both re-arm on the next
      // idle. 3 means another live runtime owns the bridge; never steal it.
      // 8 interrupted and 9 replaced mean another Hand process took over.
      if (code === 0) {
        const payload = parseWake(stdout);
        if (!payload) {
          process.stderr.write(
            "hand supervisor wake: unexpected wait output schema; run `hand init " +
              HAND_HOME +
              "` to refresh this integration\n",
          );
        } else {
          deliver(sessionID, payload).catch((err) => {
            process.stderr.write("hand supervisor wake: prompt failed: " + err + "\n");
            receipt("delivery-failed", [], err);
          });
        }
      } else if (code === 5 && stderr.trim() !== "") {
        process.stderr.write("hand supervisor wake: " + stderr.trim().split("\n")[0] + "\n");
      }
    });

    timer = setTimeout(() => {
      if (child) child.kill();
    }, 31 * 60 * 1000);
  };

  return {
    event: async ({ event }) => {
      if (event.type !== "session.idle") return;
      const sessionID = event.properties && event.properties.sessionID;
      if (!sessionID) return;

      // Ownership: only a top-level session rooted in the Fleet home may hold
      // the bridge. Subagent sessions carry parentID and are never candidates;
      // sessions in any other directory are not this Fleet's supervisor.
      const info = await sessionInfo(sessionID);
      if (!qualifiesSession(info, HAND_HOME)) return;

      if (primary !== null && primary !== sessionID) {
        // A successor top-level Fleet-home session (/new, switch) retires the
        // previous generation; stale callbacks become no-ops.
        generation += 1;
        stopChild();
      }
      primary = sessionID;
      if (child) return;
      arm(sessionID);
    },
  };
};

// qualifiesSession is the exact primary-supervisor predicate, exported so the
// generated artifact itself can be executed by tests.
export function qualifiesSession(info, fleetHome) {
  if (!info || typeof info !== "object") return false;
  if (typeof info.parentID === "string" && info.parentID !== "") return false;
  return info.directory === fleetHome;
}
