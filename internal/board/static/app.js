"use strict";
(() => {
	const body = document.body;
	const base = body.dataset.base || "";
	const fleet = body.dataset.fleet || document.title;
	const toast = document.getElementById("toast");
	const home = location.href;
	const hosted = () => document.querySelector("dialog.viewer[open]") || body;
	const regions = new Map();
	for (const el of document.querySelectorAll("[data-region]")) regions.set(el.dataset.region, el);
	const held = new Map();
	const served = new Map();
	const chosen = new Map();
	const armFor = 4000;
	const arms = new Map();
	const disarm = (action) => {
		arms.delete(action);
		for (const f of document.querySelectorAll("form[data-confirm]")) {
			if (f.getAttribute("action") !== action || !("armed" in f.dataset)) continue;
			f.querySelector("button").textContent = f.dataset.armed;
			delete f.dataset.armed;
		}
	};
	const apple = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
	body.dataset.shell = "";
	const masthead = document.querySelector(".masthead");
	if (masthead && "ResizeObserver" in window) {
		new ResizeObserver(() => document.documentElement.style.setProperty("--mast", `${masthead.getBoundingClientRect().height}px`)).observe(masthead);
	}
	const draftPrefix = `hand.draft.${base.slice(1)}`;
	const draftFields = "#composer-text, form.answer textarea[name=answer]:not(dialog *)";
	const draftTtl = 30 * 24 * 3600 * 1000;
	const draftMax = 200000;
	const draftNote = document.querySelector(".draft-note");
	const seen = new Map();
	const fieldKey = (f) => {
		if (f.id === "composer-text") return draftPrefix;
		const m = f.matches("form.answer textarea[name=answer]") && /\/decision\/([^/]+)\/answer$/.exec(f.form.getAttribute("action") || "");
		return m ? `${draftPrefix}.${m[1]}` : "";
	};
	const filesPrefix = `hand.files.${base.slice(1)}`;
	const filesKey = (key) => filesPrefix + key.slice(draftPrefix.length);
	const ours = (key) => [draftPrefix, filesPrefix].some((p) => key === p || key.startsWith(`${p}.`));
	const parsed = (raw) => {
		try {
			const d = JSON.parse(raw);
			return typeof d?.text === "string" && d.text && Date.now() - d.at < draftTtl ? d : null;
		} catch {
			return null;
		}
	};
	const stash = (key, text) => {
		try {
			if (text && text.length <= draftMax) {
				localStorage.setItem(key, JSON.stringify({ text, at: Date.now() }));
				return true;
			}
			localStorage.removeItem(key);
		} catch {
			try {
				localStorage.removeItem(key);
			} catch {}
		}
		return false;
	};
	const clock = (at) => new Date(at).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
	const noteFor = (key, text) => {
		if (key === draftPrefix && draftNote) draftNote.textContent = text;
	};
	const settle = (f, key, text, note) => {
		f.value = text;
		f.setSelectionRange(text.length, text.length);
		seen.set(key, { text, mine: false });
		noteFor(key, note);
	};
	const restore = (root) => {
		for (const f of root.querySelectorAll(draftFields)) {
			const key = fieldKey(f);
			if (!key || f.value) continue;
			try {
				const raw = localStorage.getItem(key);
				const d = raw === null ? null : parsed(raw);
				if (raw !== null && !d) localStorage.removeItem(key);
				if (d) settle(f, key, d.text, "Draft restored");
			} catch {}
		}
	};
	const save = (f) => {
		const key = fieldKey(f);
		if (!key || f.value === seen.get(key)?.text || (!f.value && !seen.has(key))) return;
		const ok = stash(key, f.value);
		seen.set(key, { text: ok || !f.value ? f.value : null, mine: true });
		noteFor(key, ok ? `Draft saved ${clock(Date.now())}` : "");
	};
	const flush = () => {
		for (const f of document.querySelectorAll(draftFields)) save(f);
	};
	const forget = (f) => {
		const key = fieldKey(f);
		if (!key) return;
		stash(key, "");
		stash(filesKey(key), "");
		seen.set(key, { text: f.value, mine: false });
		noteFor(key, "");
	};
	let saving = 0;
	const typing = (el) => el.contains(document.activeElement) && document.activeElement.matches("textarea, select, input:not([type=hidden])");

	const reversed = (el) => getComputedStyle(el).flexDirection === "column-reverse";
	const fromBottom = (el) => (reversed(el) ? -el.scrollTop : el.scrollHeight - el.scrollTop - el.clientHeight);
	const toBottom = (el) => {
		el.scrollTop = reversed(el) ? 0 : el.scrollHeight;
	};
	const fills = matchMedia("(min-height: 600px), (min-width: 900px)");
	const page = () => document.scrollingElement || document.documentElement;
	const gap = (el) => (fills.matches ? fromBottom(el) : page().scrollHeight - page().scrollTop - page().clientHeight);
	const reading = (el) => {
		const box = el.getBoundingClientRect();
		const lo = Math.max(box.top, 0);
		const hi = Math.min(box.bottom, innerHeight);
		return [...el.children].find((c) => {
			const r = c.dataset.no && c.getBoundingClientRect();
			return r && r.bottom > lo && r.top < hi;
		});
	};
	const pin = (el) => {
		if (fills.matches) toBottom(el);
		else page().scrollTop = page().scrollHeight;
	};

	const dayName = (d) => d.toLocaleDateString([], { weekday: "short", day: "numeric", month: "short" });

	const days = () => {
		const tl = regions.get("timeline");
		if (!tl) return;
		for (const p of tl.querySelectorAll(":scope > .day")) p.remove();
		const items = [...tl.children].filter((c) => c.matches(".card, .hand-line"));
		const at = (c) => new Date(c.querySelector(c.matches(".wakes") ? "summary time:last-of-type" : "time[datetime]")?.dateTime);
		let under = false;
		for (let i = items.length - 1; i >= 0; i--) {
			const d = at(items[i]);
			if (i < items.length - 1 && !Number.isNaN(d.getTime()) && d.toDateString() !== at(items[i + 1]).toDateString()) {
				const p = document.createElement("p");
				p.className = "day";
				const label = document.createElement("span");
				label.className = "badge";
				label.textContent = dayName(d);
				p.append(label);
				items[i].after(p);
				under = true;
			}
			const stamps = items[i].matches(".wakes") ? items[i].querySelectorAll(":scope > p > time") : [items[i].querySelector("time[datetime]")];
			for (const t of stamps) t?.toggleAttribute("data-clock", under);
		}
	};

	const archive = () => {
		const list = document.querySelector("[data-days]");
		const rows = list ? [...list.querySelectorAll("li.task-row")] : [];
		const at = rows.map((li) => new Date(li.querySelector("time[datetime]")?.dateTime));
		if (rows.length === 0 || at.some((d) => Number.isNaN(d.getTime()))) return;
		list.replaceChildren();
		let key, head, count, ol;
		rows.forEach((li, i) => {
			if (at[i].toDateString() !== key) {
				key = at[i].toDateString();
				head = document.createElement("h3");
				head.className = "group";
				count = document.createElement("span");
				count.className = "badge";
				count.textContent = "0";
				head.append(dayName(at[i]), " ", count);
				ol = document.createElement("ol");
				ol.className = "checks";
				list.append(head, ol);
			}
			ol.append(li);
			count.textContent = Number(count.textContent) + 1;
		});
	};

	const svgNS = "http://www.w3.org/2000/svg";
	const glyphs = {
		copy: ["M5.5 7A1.5 1.5 0 0 1 7 5.5h6A1.5 1.5 0 0 1 14.5 7v6a1.5 1.5 0 0 1-1.5 1.5H7A1.5 1.5 0 0 1 5.5 13z", "M3.5 10.5H3A1.5 1.5 0 0 1 1.5 9V3A1.5 1.5 0 0 1 3 1.5h6A1.5 1.5 0 0 1 10.5 3v.5"],
		copied: ["M3 8.5l3.5 3.5L13 4.5"],
		failed: ["M4 4l8 8M12 4l-8 8"],
		down: ["M8 3v10", "M3.5 8.5L8 13l4.5-4.5"],
	};
	const glyph = (name) => {
		const svg = document.createElementNS(svgNS, "svg");
		for (const [k, v] of Object.entries({ class: "icon", viewBox: "0 0 16 16", width: "16", height: "16", "aria-hidden": "true", fill: "none", stroke: "currentColor", "stroke-width": "1.5", "stroke-linecap": "round", "stroke-linejoin": "round", "data-glyph": name })) svg.setAttribute(k, v);
		for (const d of glyphs[name]) {
			const path = document.createElementNS(svgNS, "path");
			path.setAttribute("d", d);
			svg.append(path);
		}
		return svg;
	};
	const copyLabel = "Copy code";
	const copyFor = 1500;
	const copyTimers = new WeakMap();
	let announce = null;
	let spoken = null;
	let spokenTimer = 0;
	const copies = (root) => [...root.querySelectorAll(".code > .copy")];
	const copyKey = (b, root) => {
		const msg = b.closest("[data-no]");
		return `${msg?.dataset.no ?? ""}|${copies(msg ?? root).indexOf(b)}`;
	};
	const pinKey = "hand.table.pin";
	const recallPin = () => {
		try {
			return localStorage.getItem(pinKey) === "on";
		} catch {
			return false;
		}
	};
	let pinFirst = recallPin();
	const fitTable = (box) => {
		const pin = box.previousElementSibling;
		if (!pin?.matches(".table-pin")) return;
		const over = box.scrollWidth > box.clientWidth;
		pin.hidden = !over;
		box.classList.toggle("pinned", over && pinFirst);
	};
	const tableSizes = "ResizeObserver" in window ? new ResizeObserver((entries) => {
		for (const e of entries) {
			if (e.target.isConnected) fitTable(e.target.closest(".table"));
			else tableSizes.unobserve(e.target);
		}
	}) : null;
	const enhance = (root) => {
		for (const box of tableSizes ? root.querySelectorAll(".md:not(.doc-table) .table") : []) {
			if (box.previousElementSibling?.matches(".table-pin")) continue;
			const pin = document.createElement("button");
			pin.type = "button";
			pin.className = "table-pin";
			pin.hidden = true;
			pin.textContent = "Pin first column";
			pin.setAttribute("aria-pressed", String(pinFirst));
			box.before(pin);
			tableSizes.observe(box);
			tableSizes.observe(box.firstElementChild);
		}
		for (const pre of root.querySelectorAll(".md pre")) {
			if (pre.parentElement.matches(".code")) continue;
			if (!announce) {
				announce = document.createElement("div");
				announce.className = "sr";
				announce.setAttribute("role", "status");
				hosted().append(announce);
			}
			const wrap = document.createElement("div");
			wrap.className = "code";
			const button = document.createElement("button");
			button.type = "button";
			button.className = "copy";
			button.setAttribute("aria-label", copyLabel);
			button.title = copyLabel;
			button.append(glyph("copy"), glyph("copied"), glyph("failed"));
			pre.replaceWith(wrap);
			wrap.append(pre, button);
		}
	};
	const writeText = async (text) => {
		if (navigator.clipboard?.writeText) {
			try {
				await navigator.clipboard.writeText(text);
				return true;
			} catch {}
		}
		const box = document.createElement("textarea");
		box.className = "sr";
		box.value = text;
		box.setAttribute("readonly", "");
		box.setAttribute("aria-hidden", "true");
		hosted().append(box);
		const from = document.activeElement;
		box.select();
		box.setSelectionRange(0, text.length);
		let ok = false;
		try {
			ok = document.execCommand("copy");
		} catch {}
		box.remove();
		if (from instanceof HTMLElement) from.focus({ preventScroll: true });
		return ok;
	};
	const say = (button, text) => {
		spoken = button;
		clearTimeout(spokenTimer);
		if (announce.parentElement !== hosted()) hosted().append(announce);
		announce.textContent = "";
		if (text) spokenTimer = setTimeout(() => (announce.textContent = text), 50);
	};
	const outcome = (button, state, label) => {
		button.dataset.state = state;
		button.setAttribute("aria-label", label);
		button.title = label;
		say(button, label);
		clearTimeout(copyTimers.get(button));
		copyTimers.set(button, setTimeout(() => {
			delete button.dataset.state;
			button.setAttribute("aria-label", copyLabel);
			button.title = copyLabel;
			if (spoken === button) say(null, "");
		}, copyFor));
	};
	document.addEventListener("click", async (e) => {
		const button = e.target instanceof Element ? e.target.closest(".code > .copy") : null;
		if (!button) return;
		const ok = await writeText(button.previousElementSibling.textContent);
		if (ok) outcome(button, "copied", "Copied");
		else outcome(button, "failed", "Copy failed");
	});

	const tail = (() => {
		const tl = regions.get("timeline");
		const host = document.querySelector(".console-box, .sup > .hint.read-only");
		if (!tl || !host) return null;
		const reduced = matchMedia("(prefers-reduced-motion: reduce)");
		const button = document.createElement("button");
		button.type = "button";
		button.className = "latest";
		const badge = document.createElement("span");
		badge.className = "badge";
		badge.hidden = true;
		badge.setAttribute("aria-hidden", "true");
		button.append(glyph("down"), badge);
		const live = document.createElement("div");
		live.className = "sr";
		live.setAttribute("role", "status");
		host.prepend(button, live);
		let unread = 0;
		let frame = 0;
		let spoken = 0;
		const news = () => `${unread} new ${unread === 1 ? "message" : "messages"}`;
		const sync = () => {
			frame = 0;
			const off = gap(tl) > 48;
			if (!off) unread = 0;
			const label = unread ? `Scroll to latest, ${news()}` : "Scroll to latest message";
			button.toggleAttribute("data-away", off);
			badge.hidden = !unread;
			badge.textContent = unread > 99 ? "99+" : unread;
			if (button.getAttribute("aria-label") === label) return;
			button.setAttribute("aria-label", label);
			button.title = label;
			clearTimeout(spoken);
			spoken = setTimeout(() => (live.textContent = unread ? news() : ""), 1000);
		};
		const queue = () => {
			frame ||= requestAnimationFrame(sync);
		};
		tl.addEventListener("scroll", queue, { passive: true });
		addEventListener("scroll", queue, { passive: true });
		addEventListener("resize", queue, { passive: true });
		addEventListener("load", queue, { once: true });
		fills.addEventListener("change", queue);
		button.addEventListener("mousedown", (e) => e.preventDefault());
		button.addEventListener("click", (e) => {
			const behavior = reduced.matches ? "instant" : "smooth";
			if (fills.matches) tl.scrollTo({ top: reversed(tl) ? 0 : tl.scrollHeight, behavior });
			else page().scrollTo({ top: page().scrollHeight, behavior });
			if (e.detail !== 0) return;
			const box = document.getElementById("composer-text");
			if (box) box.focus({ preventScroll: true });
			else {
				tl.tabIndex = -1;
				tl.focus({ preventScroll: true });
			}
		});
		sync();
		return {
			queue,
			bump: (n) => {
				unread += n;
				queue();
			},
		};
	})();

	const local = (root) => {
		days();
		archive();
		enhance(root);
		for (const b of root.querySelectorAll("#notify")) b.hidden = !("Notification" in window) || Notification.permission !== "default";
		for (const t of root.querySelectorAll("time[datetime]:not([data-since])")) {
			const d = new Date(t.dateTime);
			if (Number.isNaN(d.getTime())) continue;
			const clock = t.hasAttribute("data-clock") ? { hour: "2-digit", minute: "2-digit", hourCycle: "h23" } : { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hourCycle: "h23" };
			t.textContent = d.toLocaleString([], clock);
			t.title = d.toLocaleString();
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
	const blockedNow = () => ["blocked", "worker"].includes(worst().worst);
	const title = () => {
		const w = worst();
		document.title = blockedNow() ? `(!) ${(w.worstText || "").split(" ")[0]} blocked` : waiting > 0 ? `(${waiting}) ${fleet}` : agent === "working" ? `● ${fleet}` : fleet;
		for (const n of document.querySelectorAll("[data-tab=needs] .n, [data-needs]")) {
			n.textContent = String(waiting);
			n.dataset.waiting = String(waiting);
			n.dataset.worst = w.worst || "";
			if (n.matches("[data-needs]")) n.hidden = waiting === 0;
		}
		if (!icon || !icons) return;
		const href = document.body.dataset.link === "lost" ? plain : blockedNow() ? icons.iconBlocked : waiting > 0 || agent === "blocked" ? icons.iconAttention : agent === "working" ? icons.iconWorking : plain;
		if (href && icon.getAttribute("href") !== href) icon.setAttribute("href", href);
	};

	const reflect = () => {
		const line = regions.get("status")?.querySelector(".status-line");
		if (!line) return;
		agent = line.dataset.agent || "none";
		for (const d of document.querySelectorAll("[data-agent-dot]")) d.hidden = agent !== "working";
		const hint = document.querySelector("#composer .hint");
		if (hint) {
			if (agent === "blocked") {
				const a = document.createElement("a");
				a.href = "#needs";
				a.textContent = line.dataset.hint;
				hint.replaceChildren(a);
			} else hint.textContent = line.dataset.hint;
		}
		title();
	};

	const notify = (n, text) => {
		if (!("Notification" in window) || Notification.permission !== "granted" || document.visibilityState === "visible") return;
		new Notification(fleet, { body: text || `${n} waiting for you`, tag: base || fleet });
	};

	const heldWorst = (html) => new DOMParser().parseFromString(html, "text/html").querySelector("[data-worst]")?.dataset.worstText || "";
	const count = (n, text = worst().worstText) => {
		if (n > waiting) notify(n, text);
		waiting = n;
		title();
	};

	const narrow = (form) => {
		const model = form.querySelector("select[name=model]");
		const effort = form.querySelector("select[name=effort]");
		if (!model || !effort) return;
		const wrap = effort.closest("label") ?? effort;
		const typed = !!form.querySelector("input[name=other]")?.value.trim();
		const pick = typed ? null : model.selectedOptions[0];
		const has = pick?.dataset.efforts;
		const allowed = has === undefined ? null : has.split(" ").filter(Boolean);
		for (const o of effort.options) {
			if (!o.value) continue;
			o.disabled = o.hidden = allowed !== null && !allowed.includes(o.value);
		}
		if (!effort.selectedOptions[0] || effort.selectedOptions[0].disabled) effort.selectedIndex = [...effort.options].findIndex((o) => !o.disabled);
		wrap.hidden = effort.disabled = allowed?.length === 0;
		const mark = form.querySelector(".unverified");
		if (mark) mark.hidden = !(typed || pick?.hasAttribute("data-unverified"));
	};
	const narrowAll = (root) => {
		for (const f of root.querySelectorAll("form")) narrow(f);
	};
	document.addEventListener("input", (e) => {
		const form = e.target instanceof Element ? e.target.closest("form") : null;
		if (!form || !e.target.matches("select[name=model], input[name=other]")) return;
		if (e.target.matches("select")) {
			const other = form.querySelector("input[name=other]");
			if (other) other.value = "";
		}
		narrow(form);
	});
	narrowAll(document);

	const sent = new Map();
	const collapse = (item) => {
		const s = item && sent.get(item.id);
		if (!s) return;
		if ((item.querySelector('input[name="screen"]')?.value || "") !== s.screen) {
			sent.delete(item.id);
			return;
		}
		item.dataset.sent = s.label;
		const summary = item.querySelector("summary");
		if (summary) summary.dataset.sent = s.label;
		for (const b of item.querySelectorAll(".keys button")) b.disabled = true;
		item.open = false;
	};
	const apply = (name, html, el = regions.get(name)) => {
		if (!el) return;
		served.set(name, html);
		if (typing(el)) {
			held.set(name, html);
			const m = name === "queue" && /data-waiting="(\d+)"/.exec(html);
			if (m) count(Number(m[1]), heldWorst(html));
			return;
		}
		held.delete(name);
		const spot = (e) => (e && e !== el && el.contains(e) ? [e.closest("details[id]")?.id, e.tagName, e.closest("form")?.getAttribute("action"), e.getAttribute("href"), e.value].join("|") : "");
		const copyFocus = document.activeElement?.matches?.(".code > .copy") && el.contains(document.activeElement) ? copyKey(document.activeElement, el) : "";
		const focused = copyFocus ? "" : spot(document.activeElement);
		const before = new Set([...el.querySelectorAll("details[id]")].map((d) => d.id));
		const fed = new Set([...el.querySelectorAll("[data-no]")].map((d) => d.dataset.no));
		const fields = "textarea, select, input:not([type=hidden]):not([type=radio])";
		const key = (f) => `${f.form?.getAttribute("action")}|${[...(f.form?.querySelectorAll("input[type=hidden]:not([name=csrf])") ?? [])].map((h) => `${h.name}=${h.value}`)}|${f.name}`;
		const drafts = new Map([...el.querySelectorAll(fields)].filter((f) => f.value).map((f) => [key(f), f.value]));
		const picked = new Set([...el.querySelectorAll("input[type=radio]:checked")].map((f) => `${key(f)}=${f.value}`));
		const pinned = name === "timeline" && gap(el) <= 48;
		const read = name === "timeline" && !pinned ? reading(el) : null;
		const readTop = read?.getBoundingClientRect().top;
		el.innerHTML = html;
		for (const f of el.querySelectorAll("input[type=radio]")) if (picked.has(`${key(f)}=${f.value}`)) f.checked = true;
		for (const f of el.querySelectorAll(fields)) {
			if (!drafts.has(key(f))) continue;
			f.value = drafts.get(key(f));
			if (f instanceof HTMLSelectElement && (f.selectedIndex < 0 || f.selectedOptions[0].disabled)) f.selectedIndex = Math.max(0, [...f.options].findIndex((o) => o.defaultSelected));
		}
		narrowAll(el);
		restore(el);
		for (const f of el.querySelectorAll("form[data-choose]")) otherwise(f);
		for (const f of el.querySelectorAll("form[data-confirm]")) {
			if ((arms.get(f.getAttribute("action")) ?? 0) > Date.now()) {
				const b = f.querySelector("button");
				f.dataset.armed = b.textContent;
				b.textContent = f.dataset.confirm;
			}
		}
		for (const d of el.querySelectorAll("details[id]")) {
			if (chosen.has(d.id)) d.open = chosen.get(d.id);
			if (!before.has(d.id) && d.classList.contains("wait")) {
				d.dataset.new = "";
				setTimeout(() => d.removeAttribute("data-new"), 1600);
			}
		}
		if (fed.size > 0) {
			let fresh = 0;
			for (const d of el.querySelectorAll("[data-no]")) {
				if (fed.has(d.dataset.no)) continue;
				d.dataset.new = "";
				setTimeout(() => d.removeAttribute("data-new"), 600);
				if (d.matches("[data-role=supervisor]")) fresh++;
			}
			if (name === "timeline" && !pinned && fresh) tail?.bump(fresh);
		}
		if (focused) [...el.querySelectorAll("summary, a, button")].find((c) => spot(c) === focused)?.focus();
		local(el);
		if (copyFocus) {
			copies(el).find((c) => copyKey(c, el) === copyFocus)?.focus();
		}
		since();
		if (pinned) pin(el);
		else if (read) {
			const again = [...el.children].find((c) => c.dataset.no === read.dataset.no);
			if (again) (fills.matches ? el : page()).scrollTop += again.getBoundingClientRect().top - readTop;
		}
		if (name === "timeline") tail?.queue();
		if (name === "queue") {
			for (const id of [...sent.keys()]) {
				const item = document.getElementById(id);
				if (item) collapse(item);
				else sent.delete(id);
			}
			count(waitingNow());
		}
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

	const header = (res, name) => {
		const v = res.headers?.get(name);
		if (!v) return "";
		try {
			return decodeURIComponent(v);
		} catch {
			return v;
		}
	};
	const show = (msg, ms = 8000, kind = "error") => {
		if (!toast) return;
		toast.dataset.kind = kind;
		toast.textContent = msg;
		clearTimeout(show.timer);
		show.timer = setTimeout(() => {
			toast.textContent = "";
		}, ms);
	};

	const complain = (form, msg) => {
		const out = form.matches(".answer") && form.querySelector(".form-error");
		if (out) out.textContent = msg;
		else show(msg);
	};
	const choice = (form) => form.querySelector("input[name=choice]:checked");
	const otherwise = (form) => {
		const box = form.querySelector("textarea[name=answer]");
		if (!box || !form.querySelector("input[name=choice]")) return;
		const other = choice(form)?.hasAttribute("data-other");
		box.required = !!other;
		box.placeholder = other ? "Write your answer" : "Add a note (optional)";
	};

	document.addEventListener("submit", async (e) => {
		const form = e.target;
		if (!(form instanceof HTMLFormElement)) return;
		if (form.method === "post" && form.closest("dialog.viewer")) return e.preventDefault();
		if (!form.hasAttribute("data-fetch")) return;
		e.preventDefault();
		if (form.dataset.busy) return;
		const problem = form.querySelector(".form-error");
		if (problem) problem.textContent = "";
		if ("choose" in form.dataset && !choice(form) && !form.querySelector("textarea[name=answer]").value.trim()) {
			complain(form, "Pick an option or write an answer.");
			form.querySelector("input[name=choice]").focus();
			return;
		}
		if ("attaching" in form.dataset) {
			show("Wait for the files to finish attaching, then send.", 4000, "receipt");
			return;
		}
		const primary = form.querySelector("button");
		const action = form.getAttribute("action");
		if ("armed" in form.dataset && (arms.get(action) ?? 0) <= Date.now()) disarm(action);
		if (form.dataset.confirm && !("armed" in form.dataset)) {
			form.dataset.armed = primary.textContent;
			primary.textContent = form.dataset.confirm;
			const until = Date.now() + armFor;
			arms.set(action, until);
			setTimeout(() => {
				if (arms.get(action) === until) disarm(action);
			}, armFor);
			return;
		}
		if ("armed" in form.dataset) disarm(action);
		form.dataset.busy = "";
		const data = new URLSearchParams(new FormData(form, e.submitter));
		const box = form.matches("#composer, .answer") && form.querySelector("textarea");
		const typed = box?.value;
		form.setAttribute("aria-busy", "true");
		const buttons = [...form.querySelectorAll("button")].filter((b) => !b.disabled);
		for (const b of buttons) b.disabled = true;
		const label = form.dataset.starting ? primary.textContent : null;
		if (label) primary.textContent = form.dataset.starting;
		let res = null;
		try {
			res = await fetch(form.action, { method: "POST", body: data, redirect: "manual", credentials: "same-origin", headers: { "X-Hand-Fetch": "1" } }).catch(() => null);
		} finally {
			delete form.dataset.busy;
			form.removeAttribute("aria-busy");
			for (const b of buttons) b.disabled = false;
			if (label) primary.textContent = label;
		}
		for (const el of regions.values()) if (el.contains(document.activeElement)) document.activeElement.blur();
		if (!res) {
			complain(form, "The board did not answer; check that hand board is running.");
			return;
		}
		if (res.type === "opaqueredirect" || res.ok) {
			const edited = box && box.value !== typed;
			if (form.id === "composer" && !edited) form.reset();
			if (box && !edited) forget(box);
			if (form.id === "composer") form.dispatchEvent(new Event("sent"));
			const id = (form.action.endsWith("/keys") || form.action.endsWith("/answer")) && form.closest("details.wait")?.id;
			if (id) {
				const label = form.action.endsWith("/answer") ? data.get("choice")?.split(".")[0] || "answer" : (e.submitter?.textContent || data.get("key") || "").split(" ")[0];
				sent.set(id, { label, screen: data.get("screen") || "" });
				collapse(document.getElementById(id));
			}
			if (form.closest("details.menu")) closeMenus(null);
			if (regions.size === 0) location.reload();
			const receipt = header(res, "X-Hand-Receipt");
			const warn = header(res, "X-Hand-Warn");
			if (warn) show(receipt ? `${receipt} ${warn}` : warn, 12000, "warn");
			else if (receipt) show(receipt, 4000, "receipt");
			else show("", 0);
			return;
		}
		if (res.status === 409 && form.matches(".answer")) {
			const doc = new DOMParser().parseFromString(await res.text(), "text/html");
			const next = doc.querySelector(".answered-block");
			if (next) {
				form.replaceWith(next);
				const status = doc.querySelector(".thread-head .status");
				if (status) document.querySelector(".thread-head .status")?.replaceWith(status);
				local(next);
				return;
			}
		}
		complain(form, header(res, "X-Hand-Error") || `${res.status} ${res.statusText}`);
	});

	addEventListener("pageshow", () => {
		narrowAll(document);
		restore(document);
		for (const f of document.querySelectorAll("form[data-choose]")) otherwise(f);
	});
	document.addEventListener("change", (e) => {
		const form = e.target instanceof HTMLInputElement && e.target.name === "choice" ? e.target.form : null;
		if (form) otherwise(form);
	});
	document.addEventListener("click", (e) => {
		if (e.detail > 0 && e.target instanceof HTMLInputElement && e.target.hasAttribute("data-other")) e.target.form?.querySelector("textarea[name=answer]")?.focus();
	});
	document.addEventListener("keydown", (e) => {
		if (e.ctrlKey || e.metaKey || e.altKey || e.target instanceof HTMLTextAreaElement) return;
		const at = e.target instanceof Element && e.target !== body ? e.target : null;
		const form = at ? at.closest("form[data-choose]") ?? at.closest("details.wait[open]")?.querySelector("form[data-choose]") : regions.size === 0 ? document.querySelector("form[data-choose]") : null;
		if (!form) return;
		if (e.key === "Enter" && e.target instanceof HTMLInputElement && e.target.name === "choice") {
			e.preventDefault();
			if (!e.repeat) form.requestSubmit();
			return;
		}
		const radio = [...form.querySelectorAll("input[name=choice]")].find((r) => r.closest(".option").querySelector(".option-key").textContent.toLowerCase() === e.key.toLowerCase());
		if (!radio) return;
		e.preventDefault();
		radio.checked = true;
		radio.focus();
		otherwise(form);
		if (radio.hasAttribute("data-other")) form.querySelector("textarea[name=answer]").focus();
	});

	document.addEventListener("click", async (e) => {
		if (!(e.target instanceof Element) || !e.target.closest("#notify")) return;
		await Notification.requestPermission();
		local(document);
	});

	for (const k of document.querySelectorAll("[data-send-key]")) k.textContent = apple ? "⌘ Enter" : "Ctrl Enter";
	document.addEventListener("keydown", (e) => {
		if (e.key !== "Enter" || e.repeat || !(e.ctrlKey || e.metaKey) || !(e.target instanceof HTMLTextAreaElement)) return;
		const form = e.target.form;
		if (!form?.hasAttribute("data-fetch")) return;
		e.preventDefault();
		form.requestSubmit();
	});
	const carries = (e) => [...(e.dataTransfer?.types || [])].includes("Files");
	document.addEventListener("dragover", (e) => {
		if (!carries(e) || e.defaultPrevented) return;
		e.preventDefault();
		e.dataTransfer.dropEffect = "none";
		document.getElementById("chat")?.classList.remove("dropping");
	});
	document.addEventListener("drop", (e) => {
		if (carries(e)) e.preventDefault();
	});

	const tabs = [...document.querySelectorAll("[data-tab]")];
	const owner = (id) => tabs.find((t) => t.dataset.tab === id || document.getElementById(t.dataset.tab)?.contains(document.getElementById(id)))?.dataset.tab;
	let current = owner(location.hash.slice(1)) || owner(waiting > 0 ? "needs" : "chat") || tabs[0]?.dataset.tab;
	const wide = matchMedia("(min-width:900px)");
	const select = () => {
		for (const t of tabs) {
			const on = t.dataset.tab === current;
			if (on) t.setAttribute("aria-current", "true");
			else t.removeAttribute("aria-current");
			const panel = document.getElementById(t.dataset.tab);
			if (panel) panel.hidden = !on && !wide.matches;
		}
		const linked = document.getElementById(location.hash.slice(1));
		const nested = linked && !tabs.some((t) => t.dataset.tab === linked.id);
		const timeline = regions.get("timeline");
		if (timeline && current === "chat" && !(nested && timeline.contains(linked))) pin(timeline);
		if (!nested) document.querySelector(".work")?.scrollTo(0, 0);
		if (current !== "chat" && !fills.matches) {
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
		wide.addEventListener("change", select);
		addEventListener("hashchange", () => {
			const next = owner(location.hash.slice(1));
			if (!next || next === current) return;
			current = next;
			select();
		});
		addEventListener("load", () => {
			if (current !== "chat" && !fills.matches) scrollTo(0, 0);
		}, { once: true });
	}

	const pane = document.querySelector("body[data-page=fleet] .sup");
	if (pane) {
		const root = document.documentElement.style;
		const board = pane.closest(".board");
		const recall = () => {
			try {
				return Number(localStorage.getItem("hand.chat"));
			} catch {
				return 0;
			}
		};
		const remember = (px) => {
			try {
				if (px) localStorage.setItem("hand.chat", String(px));
				else localStorage.removeItem("hand.chat");
			} catch {
				return;
			}
		};
		const saved = recall();
		if (Number.isFinite(saved) && saved > 0) root.setProperty("--chat", `${saved}px`);
		const split = document.createElement("div");
		split.className = "split";
		split.tabIndex = 0;
		for (const [k, v] of Object.entries({ role: "separator", "aria-orientation": "vertical", "aria-controls": "chat", "aria-label": "Chat width", title: "Drag to resize the chat. Double-click resets it." })) split.setAttribute(k, v);
		pane.prepend(split);
		const bounds = () => [360, Math.max(360, board.clientWidth - 440)];
		const width = () => Math.round(pane.getBoundingClientRect().width);
		const mark = () => {
			const [lo, hi] = bounds();
			split.setAttribute("aria-valuemin", String(lo));
			split.setAttribute("aria-valuemax", String(hi));
			split.setAttribute("aria-valuenow", String(width()));
			split.setAttribute("aria-valuetext", `${width()} pixels`);
		};
		const size = (px) => {
			const [lo, hi] = bounds();
			root.setProperty("--chat", `${Math.round(Math.min(hi, Math.max(lo, px)))}px`);
			mark();
		};
		const reset = () => {
			root.removeProperty("--chat");
			remember(0);
			mark();
		};
		split.addEventListener("pointerdown", (e) => {
			if (e.button !== 0) return;
			e.preventDefault();
			split.setPointerCapture(e.pointerId);
			const from = width();
			let moved = false;
			body.dataset.drag = "";
			const move = (m) => {
				moved = true;
				size(from + e.clientX - m.clientX);
			};
			split.addEventListener("pointermove", move);
			split.addEventListener("lostpointercapture", () => {
				split.removeEventListener("pointermove", move);
				delete body.dataset.drag;
				if (moved) remember(width());
			}, { once: true });
		});
		split.addEventListener("keydown", (e) => {
			const [lo, hi] = bounds();
			const step = e.shiftKey ? 64 : 16;
			const next = { ArrowLeft: width() + step, ArrowRight: width() - step, Home: lo, End: hi }[e.key];
			if (e.key === "Enter") reset();
			else if (next !== undefined) {
				size(next);
				remember(width());
			} else return;
			e.preventDefault();
		});
		split.addEventListener("dblclick", reset);
		addEventListener("resize", mark);
		mark();
	}

	const rail = document.querySelector(".rail");
	if (rail) {
		const fleets = [...rail.querySelectorAll(".nav-fleets a")];
		const home = fleets.find((a) => a.hasAttribute("aria-current")) || fleets[0];
		for (const a of fleets) a.tabIndex = a === home ? 0 : -1;
		rail.querySelector(".nav-fleets")?.addEventListener("keydown", (e) => {
			const step = { ArrowDown: 1, ArrowUp: -1 }[e.key];
			const i = fleets.indexOf(document.activeElement);
			if (!step || i < 0) return;
			e.preventDefault();
			fleets[(i + step + fleets.length) % fleets.length].focus();
		});
		let tipped;
		const tip = () => {
			const label = tipped?.querySelector(".label");
			if (!label) return;
			const r = tipped.getBoundingClientRect();
			label.style.top = `${r.top + (r.height - label.offsetHeight) / 2}px`;
		};
		const aim = (e) => {
			tipped = (e.target instanceof Element && e.target.closest(".nav a")) || tipped;
			tip();
		};
		rail.addEventListener("mouseover", aim);
		rail.addEventListener("focusin", aim);
		rail.querySelector(".rail-scroll").addEventListener("scroll", tip);
		const toggle = rail.querySelector(".rail-toggle");
		const set = (open) => {
			rail.toggleAttribute("data-open", open);
			toggle.setAttribute("aria-expanded", String(open));
			toggle.setAttribute("aria-label", open ? "Hide labels" : "Show labels");
		};
		toggle.hidden = false;
		toggle.addEventListener("click", () => set(!rail.hasAttribute("data-open")));
		document.addEventListener("click", (e) => {
			if (rail.hasAttribute("data-open") && e.target instanceof Node && !rail.contains(e.target)) set(false);
		});
		document.addEventListener("keydown", (e) => {
			if (e.key !== "Escape" || !rail.hasAttribute("data-open")) return;
			set(false);
			toggle.focus();
		});
	}

	const textView = (view) => {
		const lines = view.querySelector(".lines");
		const marker = lines.querySelector(".text-end");
		const scroller = view.closest(".viewer-body") || document.scrollingElement;
		const frame = () => (scroller === document.scrollingElement ? { top: 0, bottom: innerHeight } : scroller.getBoundingClientRect());
		const lh = 18;
		const chunkLimit = 500;
		const findCap = 20000;
		const wrap = view.querySelector(".text-wrap");
		const setWrap = (on) => {
			wrap.setAttribute("aria-pressed", String(on));
			if (on) lines.dataset.wrap = "on";
			else delete lines.dataset.wrap;
		};
		wrap.hidden = false;
		wrap.addEventListener("click", () => setWrap(wrap.getAttribute("aria-pressed") !== "true"));
		const bottom = () => document.getElementById("text-end").scrollIntoView({ block: "end" });
		const band = document.createElement("div");
		band.className = "here";
		const chunkOf = (n) => {
			let hit = null;
			for (const c of lines.querySelectorAll(".chunk")) {
				if (Number(c.dataset.from) > n) break;
				hit = c;
			}
			return hit && n < Number(hit.dataset.from) + hit.querySelector(".gut").textContent.split("\n").length - 1 ? hit : null;
		};
		const mark = (n, scroll) => {
			band.remove();
			const c = chunkOf(n);
			if (!c) return;
			setWrap(false);
			band.style.top = `${(n - Number(c.dataset.from)) * lh}px`;
			c.prepend(band);
			if (scroll) band.scrollIntoView({ block: "center" });
		};
		const jump = () => {
			const m = /^#L(\d+)$/.exec(location.hash);
			if (m) mark(Number(m[1]), true);
		};
		jump();
		addEventListener("hashchange", jump);
		let ticking = 0;
		let timer = 0;
		const unview = () => {
			removeEventListener("hashchange", jump);
			clearInterval(ticking);
			ticking = 0;
			clearTimeout(timer);
			CSS.highlights?.delete("find");
			CSS.highlights?.delete("find-now");
		};
		lines.addEventListener("click", (e) => {
			const gut = e.target instanceof Element && e.target.closest(".gut");
			if (!gut) return;
			const n = Number(gut.parentElement.dataset.from) + Math.floor((e.clientY - gut.getBoundingClientRect().top) / lh);
			history.replaceState(history.state, "", `#L${n}`);
			mark(n, false);
		});
		const form = view.querySelector(".text-find");
		const input = form.querySelector("input[type=search]");
		const count = form.querySelector(".find-count");
		if (view.dataset.whole === "true" && "highlights" in CSS) {
			let found = [];
			let at = -1;
			const paint = () => {
				CSS.highlights.set("find", new Highlight(...found));
				CSS.highlights.set("find-now", new Highlight(...(at >= 0 ? [found[at]] : [])));
			};
			const reveal = (r) => {
				r.startContainer.parentElement.closest(".chunk").scrollIntoView({ block: "start" });
				const f = frame();
				scroller.scrollTop += r.getBoundingClientRect().top - (f.top + f.bottom) / 2;
			};
			const go = (step) => {
				if (!found.length) return;
				at = (at + step + found.length) % found.length;
				paint();
				reveal(found[at]);
				count.textContent = `${at + 1} of ${found.length}${found.length >= findCap ? "+" : ""}`;
			};
			const search = () => {
				const needle = input.value.trim();
				found = [];
				at = -1;
				if (needle) {
					const re = new RegExp(needle.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "gi");
					for (const pre of lines.querySelectorAll(".src")) {
						const nodes = [];
						const starts = [];
						let text = "";
						const walk = document.createTreeWalker(pre, NodeFilter.SHOW_TEXT);
						for (let t = walk.nextNode(); t; t = walk.nextNode()) {
							nodes.push(t);
							starts.push(text.length);
							text += t.data;
						}
						let a = 0;
						let z = 0;
						for (let m = re.exec(text); m && found.length < findCap; m = re.exec(text)) {
							const to = m.index + m[0].length;
							while (a + 1 < nodes.length && starts[a + 1] <= m.index) a++;
							while (z + 1 < nodes.length && starts[z + 1] < to) z++;
							const r = new Range();
							r.setStart(nodes[a], m.index - starts[a]);
							r.setEnd(nodes[z], to - starts[z]);
							found.push(r);
						}
						if (found.length >= findCap) break;
					}
				}
				count.textContent = needle && !found.length ? "not in this file" : "";
				paint();
				go(1);
			};
			form.addEventListener("submit", (e) => {
				e.preventDefault();
				if (found.length && input.value.trim().toLowerCase() === found[0].toString().toLowerCase()) go(1);
				else search();
			});
			input.addEventListener("input", () => {
				clearTimeout(timer);
				timer = setTimeout(search, 250);
			});
			input.addEventListener("keydown", (e) => {
				if (e.key === "Enter" && e.shiftKey) {
					e.preventDefault();
					go(-1);
				}
				if (e.key === "Escape" && input.value) {
					e.preventDefault();
					input.value = "";
					search();
				}
			});
			if (input.value) search();
		}
		const follow = view.querySelector(".text-follow");
		if (!follow) return unview;
		const stop = () => {
			clearInterval(ticking);
			ticking = 0;
			follow.setAttribute("aria-pressed", "false");
		};
		let busy = false;
		const pull = async () => {
			if (document.hidden || busy) return;
			busy = true;
			try {
				const res = await fetch(`${view.dataset.href}?from=${marker.dataset.next}&line=${marker.dataset.line}`, { credentials: "same-origin", cache: "no-store" }).catch(() => null);
				if (!res?.ok || !ticking) return;
				if (res.headers.get("X-Hand-Reset")) {
					show("The file was replaced or shortened; reload to read it again.", 6000);
					stop();
					return;
				}
				const html = await res.text();
				if (!html || !ticking) return;
				const near = marker.getBoundingClientRect().top - frame().bottom < 48;
				const last = marker.previousElementSibling;
				if (marker.dataset.partial === "true" && last) {
					last.querySelector(".partial")?.remove();
					const gut = last.querySelector(".gut");
					gut.textContent = gut.textContent.replace(/[^\n]*\n$/, "");
				}
				const tpl = document.createElement("template");
				tpl.innerHTML = html;
				const fresh = [...tpl.content.children];
				if (last && fresh.length && Number(marker.dataset.line) - Number(last.dataset.from) < chunkLimit) {
					const first = fresh.shift();
					last.querySelector(".src").append(...first.querySelector(".src").childNodes);
					last.querySelector(".gut").append(...first.querySelector(".gut").childNodes);
				}
				marker.before(...fresh);
				marker.dataset.next = res.headers.get("X-Hand-Next");
				marker.dataset.line = res.headers.get("X-Hand-Line");
				marker.dataset.partial = "false";
				if (near) bottom();
			} finally {
				busy = false;
			}
		};
		follow.hidden = false;
		follow.addEventListener("click", () => {
			if (ticking) return stop();
			follow.setAttribute("aria-pressed", "true");
			bottom();
			ticking = setInterval(pull, 2000);
			pull();
		});
		return unview;
	};
	const wideKey = "hand.doc.wide";
	const wideSaved = () => {
		try {
			return localStorage.getItem(wideKey);
		} catch {
			return null;
		}
	};
	const wideView = (root) => {
		const button = root.querySelector(".doc-wide");
		const scope = root === document ? document.querySelector("main.thread.doc") : root.closest("dialog.viewer");
		if (!button || !scope) return;
		if (!scope.matches("dialog")) scope.toggleAttribute("data-wide", wideSaved() === "on");
		button.setAttribute("aria-pressed", String(scope.hasAttribute("data-wide")));
		button.hidden = false;
		button.addEventListener("click", () => {
			const on = !scope.hasAttribute("data-wide");
			scope.toggleAttribute("data-wide", on);
			button.setAttribute("aria-pressed", String(on));
			try {
				localStorage.setItem(wideKey, on ? "on" : "off");
			} catch {
				return;
			}
		});
	};
	const sourceKey = "hand.doc.source";
	const sourceSaved = () => {
		try {
			return localStorage.getItem(sourceKey) === "on";
		} catch {
			return false;
		}
	};
	const sourceView = (root) => {
		const [first, second] = root.querySelectorAll(".doc-toggle[data-remember] > *");
		if (!second || first.tagName === "A" || !sourceSaved()) return;
		if (root === document) location.replace(second.href + (/^#L\d+$/.test(location.hash) ? location.hash : ""));
		else second.click();
	};
	const docView = (root) => {
		sourceView(root);
		wideView(root);
		const stops = [...root.querySelectorAll(".text-view")].map(textView);
		return () => {
			for (const stop of stops) stop();
		};
	};
	docView(document);
	document.addEventListener("click", (e) => {
		const a = e.target instanceof Element ? e.target.closest(".doc-toggle[data-remember] > a") : null;
		if (!a || e.button || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
		try {
			if (a.parentElement.lastElementChild === a) localStorage.setItem(sourceKey, "on");
			else localStorage.removeItem(sourceKey);
		} catch {
			return;
		}
	});
	document.addEventListener("click", (e) => {
		if (!(e.target instanceof Element) || !e.target.closest(".table-pin")) return;
		pinFirst = !pinFirst;
		try {
			if (pinFirst) localStorage.setItem(pinKey, "on");
			else localStorage.removeItem(pinKey);
		} catch {
			return;
		} finally {
			for (const pin of document.querySelectorAll(".table-pin")) {
				pin.setAttribute("aria-pressed", String(pinFirst));
				fitTable(pin.nextElementSibling);
			}
		}
	});
	document.addEventListener("click", async (e) => {
		const b = e.target instanceof Element ? e.target.closest(".doc-copy") : null;
		if (!b) return;
		let text = b.dataset.text;
		if (b.dataset.copy === "contents") {
			try {
				if (b.dataset.src) {
					const res = await fetch(b.dataset.src);
					text = res.ok ? await res.text() : null;
				} else text = [...document.querySelectorAll(".lines .src")].map((p) => p.textContent).join("");
			} catch {
				text = null;
			}
		}
		const what = { path: "Path", link: "Link" }[b.dataset.copy] ?? (b.textContent === "Copy window" ? "Window" : "Contents");
		if (text != null && (await writeText(text))) show(`${what} copied`, 3000, "receipt");
		else show(`${what} not copied; select the text and copy it yourself.`, 6000);
	});
	document.addEventListener("keydown", (e) => {
		const scope = document.querySelector("dialog.viewer[open]") || (body.dataset.page === "doc" ? document : null);
		if (!scope || e.defaultPrevented || e.repeat || e.altKey || e.ctrlKey || e.metaKey) return;
		const field = e.target instanceof Element && e.target.closest("input, textarea, select, [contenteditable]");
		const find = e.key === "/" && !field && scope.querySelector(".text-find input");
		if (find) {
			e.preventDefault();
			find.focus();
			return;
		}
		const step = { ArrowLeft: "prev", ArrowRight: "next" }[e.key];
		if (!step || e.shiftKey || field || (e.target instanceof Element && e.target.closest("video, audio, .lines, .doc-picture")) || !getSelection()?.isCollapsed) return;
		scope.querySelector(`.doc-nav a[rel=${step}]`)?.click();
	});
	const dlg = document.getElementById("viewer");
	if (dlg && body.dataset.page !== "doc") {
		const pane = dlg.querySelector(".viewer-body");
		dlg.toggleAttribute("data-wide", wideSaved() !== "off");
		const state = dlg.querySelector(".viewer-state");
		const tab = dlg.querySelector(".viewer-tab");
		const back = dlg.querySelector(".viewer-back");
		tab.target = "_blank";
		tab.rel = "noopener";
		const route = /^\/(?:doc\/.|(?:task\/t|attempt\/a|decision\/d)\d+$|ref\/[tdar]\d+$)/;
		const pic = /\.(png|jpe?g|webp|gif)$/i;
		const stackMax = 20;
		const pollEvery = 10000;
		let unview = () => {};
		let opener = null;
		let nth = 0;
		let at = null;
		let ours = false;
		let seq = 0;
		let polling = 0;
		let pulling = false;
		const session = Math.random().toString(36).slice(2);
		const mine = (s) => !!s?.viewer && s.token === session;
		const depth = () => (mine(history.state) ? history.state.depth : 0);
		const entry = (href, level) => ({ viewer: href, depth: level, token: session });
		const viewable = (href) => {
			const u = new URL(href, location.href);
			if (u.origin !== location.origin || !u.pathname.startsWith(`${base}/`)) return null;
			const sent = u.pathname.startsWith(`${base}/inbox/`) && u.pathname.slice(base.length + "/inbox/".length);
			if (sent && !sent.includes("/")) u.pathname = `${base}/doc/inbox/${sent}`;
			if (!route.test(u.pathname.slice(base.length)) || u.searchParams.has("raw") || u.searchParams.has("download")) return null;
			if (pic.test(u.pathname) && !u.search) u.search = "?view";
			return u;
		};
		const within = (id) => id && [...pane.querySelectorAll("[id]")].find((el) => el.id === id);
		const same = (a) => [...document.querySelectorAll("a[href]")].filter((x) => x.getAttribute("href") === a.getAttribute("href"));
		const retry = () => {
			const p = document.createElement("p");
			p.className = "doc-notice";
			const again = document.createElement("button");
			again.type = "button";
			again.textContent = "Try again";
			again.addEventListener("click", () => load(at, "none"));
			p.append("The Board did not answer; check that hand board is running. ", again);
			return p;
		};
		const freeze = () => {
			const forms = [...pane.querySelectorAll("form")].filter((f) => f.method === "post");
			for (const f of forms) for (const c of f.elements) c.disabled = true;
			if (!forms.length) return;
			const open = document.createElement("a");
			open.className = "viewer-tab";
			open.href = at.href;
			open.target = "_blank";
			open.rel = "noopener";
			open.textContent = "Open as a page";
			const line = document.createElement("p");
			line.className = "doc-notice";
			line.append(pane.querySelector("form.answer") ? "Answer this on the page. " : "Mark read on the page. ", open);
			pane.prepend(line);
		};
		const pull = async () => {
			if (pulling || document.visibilityState === "hidden") return;
			pulling = true;
			const n = seq;
			const res = await fetch(at.href, { credentials: "same-origin" }).catch(() => null);
			const html = res?.ok ? await res.text().catch(() => "") : "";
			pulling = false;
			const main = new DOMParser().parseFromString(html, "text/html").querySelector("main.thread");
			if (n !== seq || !main || main.innerHTML === served.get("viewer") || typing(pane)) return;
			const top = pane.scrollTop;
			const had = pane.contains(document.activeElement);
			apply("viewer", main.innerHTML, pane);
			freeze();
			if (had && !pane.contains(document.activeElement)) pane.focus({ preventScroll: true });
			pane.scrollTop = top;
		};
		const unwatch = () => {
			clearInterval(polling);
			polling = 0;
			served.delete("viewer");
		};
		document.addEventListener("visibilitychange", () => {
			if (polling && document.visibilityState === "visible") pull();
		});
		const load = async (u, how) => {
			const n = ++seq;
			unwatch();
			unview();
			unview = () => {};
			const here = dlg.open ? depth() : 0;
			const level = how === "none" ? depth() || 1 : how === "push" && here < stackMax ? here + 1 : Math.max(here, 1);
			if (how === "push" && level > here) history.pushState(entry(u.href, level), "", u.href);
			else if (how !== "none") history.replaceState(entry(u.href, level), "", u.href);
			back.disabled = level < 2;
			at = u;
			state.textContent = u.pathname.split("/").pop();
			pane.setAttribute("aria-busy", "true");
			tab.href = u.href;
			ours = true;
			if (!dlg.open) {
				dlg.showModal();
				dlg.append(toast);
			}
			const res = await fetch(u.href, { credentials: "same-origin", headers: { "X-Hand-Fetch": "1" } }).catch(() => null);
			const html = res ? await res.text().catch(() => "") : "";
			if (n !== seq) return;
			pane.removeAttribute("aria-busy");
			if (!res) {
				pane.replaceChildren(retry());
				return;
			}
			const to = res.redirected ? viewable(res.url) : null;
			if (to) {
				to.hash = u.hash || (/^\/ref\/r\d+$/.test(u.pathname.slice(base.length)) ? `#${u.pathname.split("/").pop()}` : "");
				at = to;
				tab.href = to.href;
				history.replaceState(entry(to.href, level), "", to.href);
			}
			const main = new DOMParser().parseFromString(html, "text/html").querySelector("main.thread");
			const shown = main?.innerHTML;
			pane.replaceChildren(...(main ? main.childNodes : []));
			const gone = pane.querySelector(".error .back");
			if (gone) {
				const done = document.createElement("button");
				done.type = "button";
				done.textContent = "Close";
				done.addEventListener("click", close);
				gone.replaceChildren(done);
			}
			if (res.ok) state.textContent = pane.querySelector(".thread-title")?.textContent || state.textContent;
			local(pane);
			unview = docView(pane);
			freeze();
			if (res.ok && ["task", "attempt"].includes(main?.dataset.region)) {
				served.set("viewer", shown);
				polling = setInterval(pull, pollEvery);
			}
			const spot = within(at.hash.slice(1));
			if (spot) spot.scrollIntoView({ block: "center" });
			else pane.scrollTop = 0;
			pane.focus({ preventScroll: true });
		};
		const close = () => {
			if (dlg.open) dlg.close();
		};
		dlg.addEventListener("close", () => {
			seq++;
			unwatch();
			unview();
			unview = () => {};
			pane.replaceChildren();
			dlg.before(toast);
			if (ours) {
				ours = false;
				if (depth()) history.go(-depth());
			}
			if (opener && !opener.isConnected) opener = same(opener)[nth] ?? same(opener)[0];
			opener?.focus();
			opener = null;
		});
		addEventListener("popstate", (e) => {
			if (mine(e.state)) load(new URL(e.state.viewer), "none");
			else if (dlg.open) {
				ours = false;
				close();
			}
		});
		back.addEventListener("click", () => history.back());
		dlg.querySelector(".viewer-close").addEventListener("click", close);
		let pressed = null;
		dlg.addEventListener("pointerdown", (e) => {
			pressed = e.target;
		});
		dlg.addEventListener("click", (e) => {
			if (e.target === dlg && pressed === dlg) close();
		});
		document.addEventListener("click", (e) => {
			if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
			const a = e.target instanceof Element ? e.target.closest("a[href]") : null;
			if (!a || a.hasAttribute("download") || a.matches(".viewer-tab")) return;
			if (a.getAttribute("href").startsWith("#")) {
				if (!dlg.contains(a)) return;
				e.preventDefault();
				history.replaceState(history.state, "", a.hash);
				dispatchEvent(new Event("hashchange"));
				within(a.hash.slice(1))?.scrollIntoView({ block: "start" });
				return;
			}
			const u = viewable(a.href);
			if (!u) return;
			e.preventDefault();
			if (dlg.open && u.href === at.href) return;
			if (!dlg.open) {
				opener = a;
				nth = same(a).indexOf(a);
			}
			load(u, "push");
		});
		dlg.addEventListener("submit", (e) => {
			const f = e.target;
			const u = f instanceof HTMLFormElement && !e.defaultPrevented && f.method === "get" ? viewable(f.action) : null;
			if (!u) return;
			e.preventDefault();
			u.search = new URLSearchParams(new FormData(f)).toString();
			load(u, "replace");
		});
	}

	for (const [name, el] of regions) served.set(name, el.innerHTML);
	local(document);
	const tick = () => {
		for (const c of document.querySelectorAll("[data-countdown]")) {
			if (!c.dataset.endsAt) {
				const [m, sec] = c.dataset.countdown.split(":").map(Number);
				c.dataset.endsAt = String(Date.now() + (m * 60 + sec) * 1000);
			}
			const left = Math.max(0, Math.round((Number(c.dataset.endsAt) - Date.now()) / 1000));
			c.textContent = left === 0 ? "denying…" : `${Math.floor(left / 60)}:${String(left % 60).padStart(2, "0")}`;
		}
	};
	try {
		for (let i = localStorage.length - 1; i >= 0; i--) {
			const k = localStorage.key(i);
			if (k && ours(k) && !parsed(localStorage.getItem(k))) localStorage.removeItem(k);
		}
	} catch {}
	restore(document);
	document.addEventListener("input", (e) => {
		if (!(e.target instanceof HTMLTextAreaElement) || !fieldKey(e.target)) return;
		clearTimeout(saving);
		saving = setTimeout(flush, 300);
	});
	document.addEventListener("visibilitychange", () => {
		if (document.visibilityState === "hidden") flush();
	});
	addEventListener("pagehide", flush);
	addEventListener("beforeunload", flush);
	addEventListener("storage", (e) => {
		try {
			if (e.storageArea !== localStorage || !e.key || !ours(e.key)) return;
		} catch {
			return;
		}
		const d = e.newValue === null ? null : parsed(e.newValue);
		const was = e.oldValue === null ? null : parsed(e.oldValue);
		for (const f of document.querySelectorAll(draftFields)) {
			if (fieldKey(f) !== e.key) continue;
			const s = seen.get(e.key);
			const take = d ? !f.value || (f.value === s?.text && !s.mine) : !!f.value && f.value === was?.text;
			if (take) settle(f, e.key, d ? d.text : "", d ? `Draft saved ${clock(d.at)}` : "");
			else if (!d && f.value) seen.set(e.key, { text: null, mine: true });
		}
	});
	if (body.hasAttribute("data-worker") && isSecureContext && "serviceWorker" in navigator) navigator.serviceWorker.register("/sw.js", { scope: "/" }).catch(() => {});
	const installCard = document.querySelector("[data-install]");
	if (installCard) {
		const offer = installCard.querySelector("[data-install-offer]");
		let offered = null;
		installCard.hidden = matchMedia("(display-mode: standalone)").matches;
		addEventListener("beforeinstallprompt", (e) => {
			e.preventDefault();
			offered = e;
			offer.hidden = false;
		});
		addEventListener("appinstalled", () => {
			offer.hidden = true;
		});
		offer.querySelector("button").addEventListener("click", () => {
			offer.hidden = true;
			offered?.prompt();
		});
	}
	const pairForm = document.querySelector("form[data-pair]");
	if (pairForm) {
		const code = pairForm.querySelector("#code");
		if (location.hash.length > 1) {
			try {
				code.value = decodeURIComponent(location.hash.slice(1));
			} catch {
				code.value = location.hash.slice(1);
			}
			history.replaceState(null, "", location.pathname);
		}
		(code.value ? pairForm.querySelector("#name") : code).focus();
	}
	const addressForm = document.querySelector("form[data-address]");
	if (addressForm) {
		const field = addressForm.querySelector("#base");
		const addressKey = `hand.phone.address${body.dataset.base}`;
		const detected = field.dataset.detected ?? "";
		try {
			if (!field.value || field.value === detected) field.value = localStorage.getItem(addressKey) || field.value;
		} catch {}
		addressForm.addEventListener("submit", () => {
			try {
				const typed = field.value.trim();
				if (typed && typed !== detected) localStorage.setItem(addressKey, typed);
				else localStorage.removeItem(addressKey);
			} catch {}
		});
	}
	since();
	tick();
	setInterval(since, 30000);
	setInterval(tick, 1000);
	if (regions.size === 0) return;
	const live = regions.has("queue");
	if (live) reflect();
	const timeline = regions.get("timeline");
	const aimed = !!timeline?.contains(document.getElementById(location.hash.slice(1)));
	if (timeline && current === "chat" && !aimed) pin(timeline);
	const composer = document.querySelector(".console-box");
	if (timeline && composer && "ResizeObserver" in window) {
		let near = !aimed;
		const track = () => {
			near = gap(timeline) <= 48;
		};
		timeline.addEventListener("scroll", track, { passive: true });
		addEventListener("scroll", track, { passive: true });
		new ResizeObserver(() => {
			if (near && composer.offsetHeight > 0) pin(timeline);
		}).observe(composer);
		fills.addEventListener("change", () => {
			if (near && composer.offsetHeight > 0) requestAnimationFrame(() => pin(timeline));
		});
	}
	let source = null;
	let poll = 0;
	const link = document.getElementById("link");
	let lost = 0;
	let heard = Date.now();
	const word = () => regions.get("status")?.querySelector(".pill .word");
	const linkState = (ok, status) => {
		if (!link) return;
		if (ok) {
			lost = 0;
			link.hidden = true;
			delete document.body.dataset.link;
			const back = word();
			if (back?.dataset.was) {
				back.textContent = back.dataset.was;
				delete back.dataset.was;
			}
			title();
			return;
		}
		lost ||= Date.now();
		link.hidden = false;
		document.body.dataset.link = "lost";
		const now = word();
		if (now && !now.dataset.was) {
			now.dataset.was = now.textContent;
			now.textContent = "unknown";
		}
		title();
		const at = new Date(lost).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
		link.textContent = status === 403 ? (body.dataset.session === "phone" ? "Signed out: pair again on your laptop" : "Log in again: hand open --print") : Date.now() - lost > 30000 ? `Connection lost since ${at}` : "Reconnecting…";
	};
	const refresh = async () => {
		const res = await fetch(home, { credentials: "same-origin" }).catch(() => null);
		linkState(res?.ok, res?.status);
		if (!res?.ok) return;
		const doc = new DOMParser().parseFromString(await res.text(), "text/html");
		for (const el of doc.querySelectorAll("[data-region]")) {
			if (served.get(el.dataset.region) !== el.innerHTML) apply(el.dataset.region, el.innerHTML);
		}
		follow();
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
		source = new EventSource(`${base}/events${new URL(home).search}`);
		heard = Date.now();
		source.addEventListener("open", () => {
			heard = Date.now();
			linkState(true);
		});
		source.addEventListener("beat", () => {
			heard = Date.now();
		});
		source.addEventListener("error", () => {
			linkState(false);
			if (source?.readyState === EventSource.CLOSED) {
				source = null;
				poll ||= setInterval(refresh, 10000);
			}
		});
		for (const name of regions.keys())
			source.addEventListener(name, (e) => {
				heard = Date.now();
				apply(name, e.data);
			});
	};
	setInterval(() => {
		if (lost && link?.textContent === "Reconnecting…") linkState(false);
		if (source?.readyState !== EventSource.OPEN || Date.now() - heard < 35000) return;
		source.close();
		source = null;
		lost ||= heard;
		linkState(false);
		follow();
	}, 5000);
	const draft = document.getElementById("composer");
	const picker = draft?.querySelector("[data-attach]");
	const chat = draft?.closest("#chat");
	const chips = draft?.querySelector(".attachments");
	const said = draft?.querySelector("[data-att-say]");
	const tpl = document.getElementById("att-chip");
	if (draft && picker && chat && chips && said && tpl) {
		const text = draft.querySelector("textarea");
		const csrf = draft.querySelector("input[name=csrf]")?.value || "";
		const note = draft.querySelector(".hint");
		const textKey = fieldKey(text);
		const listKey = filesKey(textKey);
		const line = /^\[(image|file): (.+[\\/]inbox[\\/]([0-9]{8}-[0-9]{6}-[0-9]+(?:\.(?:png|jpg|webp|gif)|-[A-Za-z0-9_][A-Za-z0-9._-]{0,79})))\]$/;
		const sizes = new Map();
		const jobs = new Set();
		let attached = [];
		let sending = [];
		let running = 0;
		const label = (name) => {
			const rest = name.slice("20060102-150405-".length);
			const i = rest.indexOf("-");
			return i < 0 ? name : rest.slice(i + 1);
		};
		const kind = (name) => (name.endsWith("/") ? "DIR" : /\.([^.]{1,4})$/.exec(name)?.[1].toUpperCase() || "FILE");
		const human = (n) => (n < 1024 ? `${n} B` : (n + 512) >> 10 < 1024 ? `${(n + 512) >> 10} KiB` : `${(n / (1 << 20)).toFixed(1)} MiB`);
		const keep = () => {
			const ok = stash(listKey, attached.join("\n"));
			if (ok) noteFor(textKey, `Draft saved ${clock(Date.now())}`);
			else if (attached.length || !text.value) noteFor(textKey, "");
		};
		const busy = (n) => {
			running += n;
			if (running > 0) draft.dataset.attaching = "";
			else delete draft.dataset.attaching;
			if (running > 0 && note) note.textContent = `Attaching ${running} file${running === 1 ? "" : "s"}…`;
			else reflect();
		};
		const chip = ({ title, meta, img, error, job, remove }) => {
			const li = tpl.content.firstElementChild.cloneNode(true);
			const [badge, name, sub, x] = li.children;
			if (img) {
				const pic = document.createElement("img");
				pic.src = img;
				pic.alt = "";
				badge.replaceWith(pic);
			} else badge.textContent = kind(title);
			name.textContent = title;
			name.title = title;
			sub.textContent = error || meta;
			x.setAttribute("aria-label", `${error ? "Dismiss" : "Remove"} ${title}`);
			x.addEventListener("click", () => {
				remove();
				text.focus();
			});
			if (error) li.dataset.state = "error";
			else if (job) {
				li.dataset.state = "busy";
				const bar = document.createElement("progress");
				bar.max = 1;
				bar.value = job.loaded / (job.size || 1);
				bar.setAttribute("aria-label", `Uploading ${title}`);
				li.append(bar);
				Object.assign(job, { bar, sub });
			}
			return li;
		};
		const render = () => {
			text.required = !attached.length;
			const focused = chips.contains(document.activeElement) && document.activeElement.getAttribute("aria-label");
			chips.replaceChildren(
				...attached.map((l) => {
					const [, k, , name] = line.exec(l);
					const title = label(name);
					const remove = () => {
						attached = attached.filter((a) => a !== l);
						keep();
						render();
					};
					return chip({ title, meta: [kind(title), sizes.get(l)].filter(Boolean).join(" · "), img: k === "image" && `${base}/inbox/${name}`, remove });
				}),
				...[...jobs].map((job) => {
					const remove = () => {
						jobs.delete(job);
						job.xhr?.abort();
						render();
					};
					return chip({ title: job.name, meta: `${human(job.loaded)} of ${human(job.size)}`, error: job.error, job, remove });
				}),
			);
			if (focused) [...chips.querySelectorAll("button")].find((b) => b.getAttribute("aria-label") === focused)?.focus();
		};
		const fail = (job, why) => {
			job.error = why;
			said.textContent = `${job.name} was not attached: ${why}`;
		};
		const upload = (file) => {
			const job = { name: file.name || "pasted file", size: file.size, loaded: 0 };
			jobs.add(job);
			if (file.size > 10 << 20) {
				fail(job, `${human(file.size)} is over the 10 MiB limit. Paste its path instead.`);
				render();
				return;
			}
			busy(1);
			const data = new FormData();
			data.append("csrf", csrf);
			data.append("image", file, job.name);
			const xhr = (job.xhr = new XMLHttpRequest());
			xhr.open("POST", `${base}/supervisor/image`);
			xhr.setRequestHeader("X-Hand-Fetch", "1");
			xhr.upload.addEventListener("progress", (e) => {
				job.loaded = Math.min(e.loaded, job.size);
				if (!job.bar) return;
				job.bar.value = e.loaded / (e.total || 1);
				job.sub.textContent = `${human(job.loaded)} of ${human(job.size)}`;
			});
			xhr.addEventListener("loadend", () => {
				busy(-1);
				if (!jobs.has(job)) return;
				let r = null;
				try {
					r = xhr.status === 200 ? JSON.parse(xhr.responseText) : null;
				} catch {}
				const l = r && `[${r.kind}: ${r.path}]`;
				if (l && line.test(l)) {
					jobs.delete(job);
					sizes.set(l, human(job.size));
					if (!attached.includes(l)) attached.push(l);
					keep();
					said.textContent = `Attached ${label(r.name)}`;
				} else if (xhr.status === 0) fail(job, "The board did not answer; check that hand board is running.");
				else if (xhr.status === 200) fail(job, "The file could not be attached; try again.");
				else fail(job, header({ headers: { get: (h) => xhr.getResponseHeader(h) } }, "X-Hand-Error") || `${xhr.status} ${xhr.statusText}`);
				render();
			});
			xhr.send(data);
			render();
		};
		const refuse = (name) => {
			const job = { name: `${name}/`, size: 0, loaded: 0 };
			jobs.add(job);
			fail(job, "A folder is not copied. Paste its path instead.");
			render();
		};
		const take = (items) => {
			for (const item of items) {
				if (item.kind !== "file") continue;
				const entry = item.webkitGetAsEntry?.();
				const file = item.getAsFile();
				if (entry?.isDirectory) refuse(entry.name);
				else if (file) upload(file);
			}
		};
		draft.addEventListener("formdata", (e) => {
			sending = [...attached];
			if (!sending.length) return;
			const prose = e.formData.get("text") || "";
			e.formData.set("text", (prose && !prose.endsWith("\n") ? `${prose}\n` : prose) + sending.map((l) => `${l}\n`).join(""));
		});
		draft.addEventListener("sent", () => {
			attached = attached.filter((l) => !sending.includes(l));
			for (const job of jobs) if (job.error) jobs.delete(job);
			keep();
			render();
		});
		let stored = [];
		try {
			stored = parsed(localStorage.getItem(listKey))?.text.split("\n").filter((l) => line.test(l)) ?? [];
		} catch {}
		if (stored.length) busy(stored.length);
		Promise.all(
			stored.map(async (l) => {
				const res = await fetch(`${base}/inbox/${line.exec(l)[3]}`, { method: "HEAD", credentials: "same-origin" }).catch(() => null);
				const n = res?.ok && res.headers.get("Content-Length");
				if (n) sizes.set(l, human(Number(n)));
				return res?.status !== 404;
			}),
		).then((here) => {
			if (stored.length) busy(-stored.length);
			const gone = stored.filter((_, i) => !here[i]);
			attached = [...new Set([...stored.filter((_, i) => here[i]), ...attached])];
			if (gone.length) {
				keep();
				said.textContent = `${gone.map((l) => label(line.exec(l)[3])).join(", ")} ${gone.length === 1 ? "is" : "are"} no longer in inbox/`;
			}
			if (stored.length) noteFor(textKey, gone.length ? `${gone.length} attached file${gone.length === 1 ? " is" : "s are"} no longer in inbox/` : "Draft restored");
			render();
		});
		picker.addEventListener("change", () => {
			const files = [...picker.files];
			picker.value = "";
			files.forEach(upload);
		});
		text.addEventListener("paste", (e) => {
			const items = [...(e.clipboardData?.items || [])];
			if (!items.some((i) => i.kind === "file")) return;
			if (!items.some((i) => i.type === "text/plain")) e.preventDefault();
			take(items);
		});
		chat.addEventListener("dragover", (e) => {
			if (!carries(e)) return;
			e.preventDefault();
			e.dataTransfer.dropEffect = "copy";
			chat.classList.add("dropping");
		});
		chat.addEventListener("dragleave", (e) => {
			if (!chat.contains(e.relatedTarget)) chat.classList.remove("dropping");
		});
		chat.addEventListener("drop", (e) => {
			chat.classList.remove("dropping");
			if (!carries(e)) return;
			e.preventDefault();
			take([...e.dataTransfer.items]);
		});
	}
	document.addEventListener("visibilitychange", follow);
	follow();
})();
