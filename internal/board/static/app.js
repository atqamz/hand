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
	const apple = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
	body.dataset.shell = "";
	const typing = (el) => el.contains(document.activeElement) && document.activeElement.matches("textarea, select, input:not([type=hidden])");

	const reversed = (el) => getComputedStyle(el).flexDirection === "column-reverse";
	const fromBottom = (el) => (reversed(el) ? -el.scrollTop : el.scrollHeight - el.scrollTop - el.clientHeight);
	const toBottom = (el) => {
		el.scrollTop = reversed(el) ? 0 : el.scrollHeight;
	};
	const tall = matchMedia("(min-height: 600px)");
	const page = () => document.scrollingElement || document.documentElement;
	const gap = (el) => (tall.matches ? fromBottom(el) : page().scrollHeight - page().scrollTop - page().clientHeight);
	const pin = (el) => {
		if (tall.matches) toBottom(el);
		else page().scrollTop = page().scrollHeight;
	};

	const local = (root) => {
		for (const t of root.querySelectorAll("time[datetime]:not([data-since]):not([data-clock-now])")) {
			const d = new Date(t.dateTime);
			if (Number.isNaN(d.getTime())) continue;
			const clock = t.hasAttribute("data-clock") ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" } : { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23", timeZoneName: "short" };
			t.textContent = d.toLocaleString([], clock);
			t.title = d.toLocaleString();
		}
	};

	const clock = () => {
		const now = new Date();
		for (const c of document.querySelectorAll("[data-clock-now]")) {
			c.textContent = now.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
			c.title = now.toLocaleTimeString([], { timeZoneName: "long" });
			c.dateTime = now.toISOString();
		}
	};

	const since = () => {
		for (const t of document.querySelectorAll("time[data-since]")) {
			const m = Math.floor((Date.now() - new Date(t.dateTime).getTime()) / 60000);
			if (Number.isNaN(m)) continue;
			t.textContent = m < 1 ? "<1m" : m < 60 ? `${m}m` : `${Math.floor(m / 60)}h ${m % 60}m`;
			t.title = new Date(t.dateTime).toLocaleString();
		}
	};

	const waitingNow = () => {
		const el = regions.get("queue")?.querySelector("[data-waiting]");
		return el ? Number(el.dataset.waiting) || 0 : 0;
	};
	let waiting = waitingNow();
	let agent = "none";
	const icon = document.querySelector("link[rel=icon]");
	const plain = icon?.getAttribute("href");
	const icons = document.querySelector("[data-icon-working]")?.dataset;
	const worst = () => regions.get("queue")?.querySelector("[data-worst]")?.dataset ?? {};
	const urgent = () => ["blocked", "worker"].includes(worst().worst);
	const title = () => {
		const w = worst();
		document.title = urgent() ? `(!) ${(w.worstText || "").split(" ")[0]} blocked` : waiting > 0 ? `(${waiting}) ${fleet}` : agent === "working" ? `● ${fleet}` : fleet;
		for (const n of document.querySelectorAll("[data-tab=needs] .n")) {
			n.textContent = String(waiting);
			n.dataset.waiting = String(waiting);
		}
		if (!icon || !icons) return;
		const href = urgent() ? icons.iconBlocked : waiting > 0 || agent === "blocked" ? icons.iconAttention : agent === "working" ? icons.iconWorking : plain;
		if (href && icon.getAttribute("href") !== href) icon.setAttribute("href", href);
	};

	const reflect = () => {
		const line = regions.get("status")?.querySelector(".status-line");
		if (!line) return;
		agent = line.dataset.agent || "none";
		for (const d of document.querySelectorAll("[data-agent-dot]")) d.hidden = agent !== "working";
		const hint = document.querySelector("#composer .hint");
		if (hint && line.dataset.hint) {
			if (agent === "blocked") {
				const a = document.createElement("a");
				a.href = "#needs";
				a.textContent = line.dataset.hint;
				hint.replaceChildren(a);
			} else hint.textContent = line.dataset.hint;
		}
		title();
	};

	const notify = (n) => {
		if (!("Notification" in window) || Notification.permission !== "granted" || document.visibilityState === "visible") return;
		new Notification(fleet, { body: worst().worstText || `${n} waiting for you`, tag: base || fleet });
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
		const fed = new Set([...el.querySelectorAll("[data-no]")].map((d) => d.dataset.no));
		const fields = "textarea, select, input:not([type=hidden])";
		const key = (f) => `${f.form?.getAttribute("action")}|${f.name}`;
		const drafts = new Map([...el.querySelectorAll(fields)].filter((f) => f.value).map((f) => [key(f), f.value]));
		const pinned = name === "timeline" && gap(el) <= 48;
		el.innerHTML = html;
		for (const f of el.querySelectorAll(fields)) if (drafts.has(key(f))) f.value = drafts.get(key(f));
		for (const d of el.querySelectorAll("details[id]")) {
			if (chosen.has(d.id)) d.open = chosen.get(d.id);
			if (!before.has(d.id) && d.classList.contains("wait")) {
				d.dataset.new = "";
				setTimeout(() => d.removeAttribute("data-new"), 1600);
			}
		}
		if (fed.size > 0) {
			for (const d of el.querySelectorAll("[data-no]")) {
				if (fed.has(d.dataset.no)) continue;
				d.dataset.new = "";
				setTimeout(() => d.removeAttribute("data-new"), 600);
			}
		}
		if (focused) [...el.querySelectorAll("summary, a, button")].find((c) => spot(c) === focused)?.focus();
		local(el);
		since();
		if (pinned) pin(el);
		if (name === "queue") count(waitingNow());
		if (name === "status") reflect();
	};

	document.addEventListener("click", (e) => {
		const summary = e.target instanceof Element ? e.target.closest("details[id] > summary") : null;
		if (!summary) return;
		const d = summary.parentElement;
		setTimeout(() => chosen.set(d.id, d.open));
	});

	const closeMenus = (keep) => {
		for (const m of document.querySelectorAll("details.menu[open]")) {
			if (keep && m.contains(keep)) continue;
			m.open = false;
			chosen.set(m.id, false);
		}
	};
	document.addEventListener("click", (e) => closeMenus(e.target instanceof Node ? e.target : null));
	document.addEventListener("keydown", (e) => {
		if (e.key !== "Escape") return;
		const open = document.querySelector("details.menu[open]");
		closeMenus(null);
		open?.querySelector("summary")?.focus();
	});

	document.addEventListener("focusout", () => {
		setTimeout(() => {
			for (const [name, html] of held) apply(name, html);
		});
	});

	const show = (msg, ms = 8000, kind = "error") => {
		toast.dataset.kind = kind;
		toast.textContent = msg;
		clearTimeout(show.timer);
		show.timer = setTimeout(() => {
			toast.textContent = "";
		}, ms);
	};

	document.addEventListener("submit", async (e) => {
		const form = e.target;
		if (!(form instanceof HTMLFormElement) || !form.hasAttribute("data-fetch")) return;
		e.preventDefault();
		if (form.dataset.busy) return;
		form.dataset.busy = "";
		const data = new URLSearchParams(new FormData(form, e.submitter));
		form.setAttribute("aria-busy", "true");
		const buttons = [...form.querySelectorAll("button")].filter((b) => !b.disabled);
		for (const b of buttons) b.disabled = true;
		let res = null;
		try {
			res = await fetch(form.action, { method: "POST", body: data, redirect: "manual", credentials: "same-origin", headers: { "X-Hand-Fetch": "1" } }).catch(() => null);
		} finally {
			delete form.dataset.busy;
			form.removeAttribute("aria-busy");
			for (const b of buttons) b.disabled = false;
		}
		for (const el of regions.values()) if (el.contains(document.activeElement)) document.activeElement.blur();
		if (!res) {
			show("The board did not answer; check that hand board is running.");
			return;
		}
		if (res.type === "opaqueredirect" || res.ok) {
			if (form.id === "composer") form.reset();
			if (form.closest("details.menu")) closeMenus(null);
			const receipt = res.headers?.get("X-Hand-Receipt");
			if (receipt) show(receipt, 4000, "receipt");
			else toast.textContent = "";
			return;
		}
		show(res.headers.get("X-Hand-Error") || `${res.status} ${res.statusText}`);
	});

	document.addEventListener("click", (e) => {
		const chip = e.target instanceof Element ? e.target.closest("button[data-fill]") : null;
		const box = chip?.form?.querySelector("textarea[name=answer]");
		if (!box) return;
		box.value = chip.dataset.fill;
		box.focus();
		box.dispatchEvent(new Event("input", { bubbles: true }));
	});

	const button = document.getElementById("notify");
	if (button && "Notification" in window && Notification.permission === "default") {
		button.hidden = false;
		button.addEventListener("click", async () => {
			await Notification.requestPermission();
			button.hidden = Notification.permission !== "default";
		});
	}

	for (const k of document.querySelectorAll("[data-send-key]")) k.textContent = apple ? "⌘ Enter" : "Ctrl Enter";
	document.addEventListener("keydown", (e) => {
		if (e.key !== "Enter" || e.repeat || !(e.ctrlKey || e.metaKey) || !(e.target instanceof HTMLTextAreaElement)) return;
		const form = e.target.form;
		if (!form?.hasAttribute("data-fetch")) return;
		e.preventDefault();
		form.requestSubmit();
	});

	const tabs = [...document.querySelectorAll("[data-tab]")];
	const hashed = location.hash.slice(1);
	let current = tabs.some((t) => t.dataset.tab === hashed) ? hashed : waiting > 0 ? "needs" : "chat";
	const select = () => {
		for (const t of tabs) {
			const on = t.dataset.tab === current;
			if (on) t.setAttribute("aria-current", "true");
			else t.removeAttribute("aria-current");
			const panel = document.getElementById(t.dataset.tab);
			if (panel) panel.hidden = !on;
		}
		const timeline = regions.get("timeline");
		if (timeline && current === "chat") pin(timeline);
		if (current === "needs" && !tall.matches) {
			scrollTo(0, 0);
			requestAnimationFrame(() => scrollTo(0, 0));
		}
	};
	if (tabs.length > 0) {
		for (const t of tabs) {
			t.addEventListener("click", (e) => {
				e.preventDefault();
				current = t.dataset.tab;
				history.replaceState(null, "", `#${current}`);
				select();
			});
		}
		select();
		addEventListener("hashchange", () => {
			const next = location.hash.slice(1);
			if (next === current || !tabs.some((t) => t.dataset.tab === next)) return;
			current = next;
			select();
		});
		addEventListener("load", () => {
			if (current === "needs" && !tall.matches) scrollTo(0, 0);
		}, { once: true });
	}

	for (const [name, el] of regions) served.set(name, el.innerHTML);
	local(document);
	since();
	clock();
	setInterval(() => {
		since();
		clock();
	}, 30000);
	if (regions.size === 0) return;
	const live = regions.has("queue");
	if (live) reflect();
	const timeline = regions.get("timeline");
	if (timeline && current === "chat") pin(timeline);
	const composer = document.querySelector(".console-box");
	if (timeline && composer && "ResizeObserver" in window) {
		let near = true;
		const track = () => {
			near = gap(timeline) <= 48;
		};
		timeline.addEventListener("scroll", track, { passive: true });
		addEventListener("scroll", track, { passive: true });
		new ResizeObserver(() => {
			if (near && composer.offsetHeight > 0) pin(timeline);
		}).observe(composer);
		tall.addEventListener("change", () => {
			if (near && composer.offsetHeight > 0) requestAnimationFrame(() => pin(timeline));
		});
	}
	let source = null;
	let poll = 0;
	const link = document.getElementById("link");
	let lost = 0;
	const linkState = (ok, status) => {
		if (!link) return;
		if (ok) {
			lost = 0;
			link.hidden = true;
			return;
		}
		lost ||= Date.now();
		link.hidden = false;
		const at = new Date(lost).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
		link.textContent = status === 403 ? "Log in again: hand open --print" : Date.now() - lost > 30000 ? `Board offline since ${at}` : "Reconnecting…";
	};
	const refresh = async () => {
		const res = await fetch(location.href, { credentials: "same-origin" }).catch(() => null);
		linkState(res?.ok, res?.status);
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
		source.addEventListener("open", () => linkState(true));
		source.addEventListener("error", () => {
			linkState(false);
			if (source?.readyState === EventSource.CLOSED) {
				source = null;
				poll ||= setInterval(refresh, 10000);
			}
		});
		for (const name of regions.keys()) source.addEventListener(name, (e) => apply(name, e.data));
	};
	document.addEventListener("visibilitychange", follow);
	follow();
})();
