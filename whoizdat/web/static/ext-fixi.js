// ext-fixi.js: small extensions for fixi 0.9.4.
//
// ext-fx-poll="ms": send a "poll" event to the element every ms milliseconds.
// Use it with fx-trigger="poll". Polling stops when the element leaves the
// page, waits while the tab is hidden, and waits while focus is inside the
// element, so that a swap does not take away what the person is using.
// A poll swap does not use view transitions.
(() => {
	document.addEventListener("fx:inited", (evt) => {
		let elt = evt.target
		let ms = parseInt(elt.getAttribute("ext-fx-poll"), 10)
		if (!(ms > 0)) return
		let id = setInterval(() => {
			if (!elt.isConnected) return clearInterval(id)
			if (document.hidden || elt.contains(document.activeElement)) return
			elt.dispatchEvent(new CustomEvent("poll"))
		}, ms)
	}, true) // fx:inited does not bubble, so listen in the capture phase
	document.addEventListener("fx:config", (evt) => {
		if (evt.detail.cfg.trigger?.type === "poll") evt.detail.cfg.transition = null
	})
})()
