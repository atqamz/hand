"use strict";
(() => {
	const body = document.body;
	const base = body.dataset.base || "";
	const fleet = body.dataset.fleet || document.title;
	const toast = document.getElementById("toast");
	const regions = new Map();
	for (const el of document.querySelectorAll("[data-region]")) regions.set(el.dataset.region, el);
	const held = new Map();

	const reversed = (el) => getComputedStyle(el).flexDirection === "column-reverse";
	const fromBottom = (el) => (reversed(el) ? -el.scrollTop : el.scrollHeight - el.scrollTop - el.clientHeight);
	const toBottom = (el) => {
		el.scrollTop = reversed(el) ? 0 : el.scrollHeight;
	};

	const waitingNow = () => {
		const el = regions.get("queue")?.querySelector("[data-waiting]");
		return el ? Number(el.dataset.waiting) || 0 : 0;
	};
	let waiting = waitingNow();
	const title = () => {
		document.title = waiting > 0 ? `(${waiting}) ${fleet}` : fleet;
	};

	const notify = (n) => {
		if (!("Notification" in window) || Notification.permission !== "granted" || document.visibilityState === "visible") return;
		new Notification(fleet, { body: `${n} waiting for you`, tag: base || fleet });
	};

	const apply = (name, html) => {
		const el = regions.get(name);
		if (!el) return;
		if (el.contains(document.activeElement)) {
			held.set(name, html);
			return;
		}
		held.delete(name);
		const pinned = name === "timeline" && fromBottom(el) <= 48;
		el.innerHTML = html;
		if (pinned) toBottom(el);
		if (name === "queue") {
			const n = waitingNow();
			if (n > waiting) notify(n);
			waiting = n;
			title();
		}
	};

	document.addEventListener("focusout", () => {
		setTimeout(() => {
			for (const [name, html] of held) apply(name, html);
		});
	});

	const show = (msg) => {
		toast.textContent = msg;
		clearTimeout(show.timer);
		show.timer = setTimeout(() => {
			toast.textContent = "";
		}, 8000);
	};

	document.addEventListener("submit", async (e) => {
		const form = e.target;
		if (!(form instanceof HTMLFormElement) || !form.hasAttribute("data-fetch")) return;
		e.preventDefault();
		const data = new URLSearchParams(new FormData(form, e.submitter));
		const res = await fetch(form.action, { method: "POST", body: data, redirect: "manual", credentials: "same-origin" }).catch(() => null);
		for (const el of regions.values()) if (el.contains(document.activeElement)) document.activeElement.blur();
		if (!res) {
			show("The board did not answer; check that hand board is running.");
			return;
		}
		if (res.type === "opaqueredirect" || res.ok) {
			if (form.id === "composer") form.reset();
			toast.textContent = "";
			return;
		}
		show(res.headers.get("X-Hand-Error") || `${res.status} ${res.statusText}`);
	});

	const button = document.getElementById("notify");
	if (button && "Notification" in window && Notification.permission === "default") {
		button.hidden = false;
		button.addEventListener("click", async () => {
			await Notification.requestPermission();
			button.hidden = Notification.permission !== "default";
		});
	}

	if (regions.size === 0) return;
	title();
	const timeline = regions.get("timeline");
	if (timeline) toBottom(timeline);
	const source = new EventSource(`${base}/events${location.search}`);
	for (const name of regions.keys()) source.addEventListener(name, (e) => apply(name, e.data));
})();
