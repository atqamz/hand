"use strict";
(() => {
	const state = document.querySelector("[data-offline-state]");
	const retry = document.querySelector("[data-retry]");
	const clock = (d) => d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
	const since = new Date();
	const open = () => (location.pathname === "/offline" ? location.replace("/") : location.reload());
	let busy = false;
	const say = () => {
		state.textContent = `Unreachable since ${clock(since)}. Last tried ${clock(new Date())}. It tries again by itself and opens the Board when it answers.`;
	};
	const probe = async () => {
		if (busy) return;
		busy = true;
		const res = await fetch("/offline", { cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(8000) }).catch(() => null);
		busy = false;
		if (res?.ok) open();
		else say();
	};
	retry.addEventListener("click", open);
	addEventListener("online", probe);
	setInterval(probe, 10000);
	say();
})();
