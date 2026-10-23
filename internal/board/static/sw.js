"use strict";
const PREFIX = "hand-offline-";
const CACHE = PREFIX + BUILD;
const OFFLINE = "/offline";
const PATIENCE = 8000;
const GATEWAY = new Set([502, 503, 504]);
self.addEventListener("install", (event) => {
	event.waitUntil(
		(async () => {
			const page = await fetch(OFFLINE, { cache: "no-store", credentials: "omit" });
			if (!page.ok) throw new Error(`offline page: ${page.status}`);
			const html = await page.clone().text();
			const files = [...html.matchAll(/(?:href|src)="(\/static\/[A-Za-z0-9._-]+)"/g)].map((m) => m[1]);
			const cache = await caches.open(CACHE);
			await cache.put(OFFLINE, page);
			await cache.addAll(files.map((url) => new Request(url, { credentials: "omit" })));
			await self.skipWaiting();
		})(),
	);
});
self.addEventListener("activate", (event) => {
	event.waitUntil(
		(async () => {
			for (const name of await caches.keys()) if (name.startsWith(PREFIX) && name !== CACHE) await caches.delete(name);
			await self.clients.claim();
		})(),
	);
});
const unreachable = async () => (await caches.open(CACHE).then((cache) => cache.match(OFFLINE))) || Response.error();
const navigate = async (request) => {
	let timer;
	const late = new Promise((resolve) => {
		timer = setTimeout(resolve, PATIENCE, null);
	});
	const answer = await Promise.race([fetch(request).catch(() => null), late]);
	clearTimeout(timer);
	return answer && !GATEWAY.has(answer.status) ? answer : unreachable();
};
const shipped = async (request) => (await caches.open(CACHE).then((cache) => cache.match(request))) || fetch(request);
self.addEventListener("fetch", (event) => {
	const { request } = event;
	const url = new URL(request.url);
	if (url.origin !== self.location.origin || request.method !== "GET") return;
	if (request.mode === "navigate") event.respondWith(navigate(request));
	else if (url.pathname.startsWith("/static/")) event.respondWith(shipped(request));
});
