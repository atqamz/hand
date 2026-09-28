"use strict";
(() => {
	const body = document.body;
	const base = body.dataset.base || "";
	const fleet = body.dataset.fleet || document.title;
	const toast = document.getElementById("toast");
	const regions = new Map();
	for (const el of document.querySelectorAll("[data-region]")) regions.set(el.dataset.region, el);
	const held = new Map();
	const served = new Map();
	const chosen = new Map();
	const narrow = matchMedia("(max-width: 899px), (max-height: 599px)");
	const typing = (el) => el.contains(document.activeElement) && document.activeElement.matches("textarea, select, input:not([type=hidden])");

	const reversed = (el) => getComputedStyle(el).flexDirection === "column-reverse";
	const fromBottom = (el) => (reversed(el) ? -el.scrollTop : el.scrollHeight - el.scrollTop - el.clientHeight);
	const toBottom = (el) => {
		el.scrollTop = reversed(el) ? 0 : el.scrollHeight;
	};

	const local = (root) => {
		for (const t of root.querySelectorAll("time[datetime]")) {
			const d = new Date(t.dateTime);
			if (Number.isNaN(d.getTime())) continue;
			t.textContent = d.toLocaleString([], { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });
			t.title = d.toLocaleString();
		}
	};

	const waitingNow = () => {
		const el = regions.get("queue")?.querySelector("[data-waiting]");
		return el ? Number(el.dataset.waiting) || 0 : 0;
	};
	let waiting = waitingNow();
	const title = () => {
		document.title = waiting > 0 ? `(${waiting}) ${fleet}` : fleet;
		for (const n of document.querySelectorAll("[data-tab=needs] .n")) n.textContent = String(waiting);
	};

	const notify = (n) => {
		if (!("Notification" in window) || Notification.permission !== "granted" || document.visibilityState === "visible") return;
		new Notification(fleet, { body: `${n} waiting for you`, tag: base || fleet });
	};

	const count = (n) => {
		if (n > waiting) notify(n);
		waiting = n;
		title();
	};

	const apply = (name, html) => {
		const el = regions.get(name);
		if (!el) return;
		served.set(name, html);
		if (typing(el)) {
			held.set(name, html);
			const m = name === "queue" && /data-waiting="(\d+)"/.exec(html);
			if (m) count(Number(m[1]));
			return;
		}
		held.delete(name);
		const spot = (e) => (e && e !== el && el.contains(e) ? [e.closest("details[id]")?.id, e.tagName, e.closest("form")?.getAttribute("action"), e.getAttribute("href"), e.value].join("|") : "");
		const focused = spot(document.activeElement);
		const before = new Set([...el.querySelectorAll("details[id]")].map((d) => d.id));
		const fields = "textarea, select, input:not([type=hidden])";
		const key = (f) => `${f.form?.getAttribute("action")}|${f.name}`;
		const drafts = new Map([...el.querySelectorAll(fields)].filter((f) => f.value).map((f) => [key(f), f.value]));
		const pinned = name === "timeline" && fromBottom(el) <= 48;
		el.innerHTML = html;
		for (const f of el.querySelectorAll(fields)) if (drafts.has(key(f))) f.value = drafts.get(key(f));
		for (const d of el.querySelectorAll("details[id]")) {
			if (chosen.has(d.id)) d.open = chosen.get(d.id);
			if (!before.has(d.id) && d.classList.contains("wait")) {
				d.dataset.new = "";
				setTimeout(() => d.removeAttribute("data-new"), 1600);
			}
		}
		if (focused) [...el.querySelectorAll("summary, a, button")].find((c) => spot(c) === focused)?.focus();
		local(el);
		if (pinned) toBottom(el);
		if (name === "queue") count(waitingNow());
	};

	document.addEventListener("click", (e) => {
		const summary = e.target instanceof Element ? e.target.closest("details[id] > summary") : null;
		if (!summary) return;
		const d = summary.parentElement;
		setTimeout(() => chosen.set(d.id, d.open));
	});

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

	const tabs = [...document.querySelectorAll("[data-tab]")];
	let current = location.hash === "#chat" ? "chat" : "needs";
	const select = () => {
		for (const t of tabs) {
			const on = t.dataset.tab === current;
			if (on && narrow.matches) t.setAttribute("aria-current", "true");
			else t.removeAttribute("aria-current");
			const panel = document.getElementById(t.dataset.tab);
			if (panel) panel.hidden = narrow.matches && !on;
		}
		const timeline = regions.get("timeline");
		if (timeline && current === "chat") toBottom(timeline);
	};
	if (tabs.length > 0) {
		for (const t of tabs) {
			t.addEventListener("click", (e) => {
				e.preventDefault();
				current = t.dataset.tab;
				select();
			});
		}
		narrow.addEventListener("change", select);
		select();
	}

	for (const [name, el] of regions) served.set(name, el.innerHTML);
	local(document);
	if (regions.size === 0) return;
	const live = regions.has("queue");
	if (live) title();
	const timeline = regions.get("timeline");
	if (timeline) toBottom(timeline);
	let source = null;
	let poll = 0;
	const refresh = async () => {
		const res = await fetch(location.href, { credentials: "same-origin" }).catch(() => null);
		if (!res?.ok) return;
		const doc = new DOMParser().parseFromString(await res.text(), "text/html");
		for (const el of doc.querySelectorAll("[data-region]")) {
			if (served.get(el.dataset.region) !== el.innerHTML) apply(el.dataset.region, el.innerHTML);
		}
	};
	const follow = () => {
		if (!live || document.visibilityState === "hidden") {
			source?.close();
			source = null;
			poll ||= setInterval(refresh, live ? 20000 : 10000);
			return;
		}
		clearInterval(poll);
		poll = 0;
		if (source) return;
		source = new EventSource(`${base}/events${location.search}`);
		for (const name of regions.keys()) source.addEventListener(name, (e) => apply(name, e.data));
	};
	document.addEventListener("visibilitychange", follow);
	follow();
})();
