package board

import "net/http"

const retireWorkerJS = `"use strict";
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (event) => {
	event.waitUntil(
		(async () => {
			for (const name of await caches.keys()) await caches.delete(name);
			await self.registration.unregister();
		})(),
	);
});
`

func retireWorker(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write([]byte(retireWorkerJS))
}
