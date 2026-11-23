// map.js: radios around a fixed sensor, on MapLibre. Data comes from /map.json.
// Each radio is a dot at its estimated distance. The direction is made up (fixed
// per radio), so the rings, not the direction, carry the information.
(() => {
	const el = document.getElementById("map")
	if (!el || !window.maplibregl) return
	maplibregl.setWorkerUrl("/static/maplibre/maplibre-gl-csp-worker.js")

	const site = [parseFloat(el.dataset.lon), parseFloat(el.dataset.lat)]
	const query = el.dataset.query || ""
	const dark = matchMedia("(prefers-color-scheme: dark)").matches
	const c = dark
		? { bg: "#1b1b1a", earth: "#222220", water: "#1d2b38", road: "#3a3a37", building: "#2c2c2a", ring: "#7a7a72", text: "#e8e8e3" }
		: { bg: "#efefea", earth: "#f4f4ef", water: "#bcd3e6", road: "#ffffff", building: "#dcdcd4", ring: "#8a8a80", text: "#1c1c1a" }
	const roleColor = ["match", ["get", "role"], "ap", "#1f6feb", "client", "#8250df", "ble", "#1a7f37", "#777"]

	const style = { version: 8, sources: {}, layers: [{ id: "bg", type: "background", paint: { "background-color": c.bg } }] }
	if (el.dataset.basemap && window.pmtiles) {
		const protocol = new pmtiles.Protocol()
		maplibregl.addProtocol("pmtiles", protocol.tile)
		// Protomaps basemap schema (v4). No labels, so no font files are needed.
		style.sources.base = { type: "vector", url: "pmtiles://" + location.origin + el.dataset.basemap, maxzoom: 15 }
		style.layers.push(
			{ id: "earth", type: "fill", source: "base", "source-layer": "earth", paint: { "fill-color": c.earth } },
			{ id: "water", type: "fill", source: "base", "source-layer": "water", paint: { "fill-color": c.water } },
			{ id: "roads", type: "line", source: "base", "source-layer": "roads",
			  paint: { "line-color": c.road, "line-width": ["interpolate", ["exponential", 1.6], ["zoom"], 14, 1, 19, 14] } },
			{ id: "buildings", type: "fill", source: "base", "source-layer": "buildings",
			  paint: { "fill-color": c.building, "fill-outline-color": c.ring } })
	}

	const map = new maplibregl.Map({ container: el, style, center: site, zoom: 18.2, maxZoom: 20, minZoom: 12, attributionControl: false })
	el.map = map // for tests and the browser console
	map.addControl(new maplibregl.NavigationControl({ showCompass: false }))
	map.addControl(new maplibregl.AttributionControl({ compact: true,
		customAttribution: el.dataset.basemap ? "© OpenStreetMap contributors, Protomaps" : "" }))

	const empty = { type: "FeatureCollection", features: [] }
	const status = document.getElementById("map-status")
	let ringLabels = []

	// A band (ring polygon) from near to far metres around the site.
	function band(near, far) {
		const ring = (m, rev) => {
			const pts = []
			for (let i = 0; i <= 72; i++) {
				const b = (rev ? 72 - i : i) * 5 * Math.PI / 180
				const dLat = m * Math.cos(b) / 6371008.8 * 180 / Math.PI
				const dLon = m * Math.sin(b) / (6371008.8 * Math.cos(site[1] * Math.PI / 180)) * 180 / Math.PI
				pts.push([site[0] + dLon, site[1] + dLat])
			}
			return pts
		}
		return { type: "Feature", geometry: { type: "Polygon", coordinates: [ring(far, false), ring(near, true)] }, properties: {} }
	}

	map.on("load", () => {
		map.addSource("rings", { type: "geojson", data: empty })
		map.addSource("band", { type: "geojson", data: empty })
		map.addSource("devices", { type: "geojson", data: empty })
		map.addSource("site", { type: "geojson", data: { type: "Feature", geometry: { type: "Point", coordinates: site }, properties: {} } })
		map.addLayer({ id: "band", type: "fill", source: "band", paint: { "fill-color": "#f3d34a", "fill-opacity": 0.35 } })
		map.addLayer({ id: "rings", type: "line", source: "rings", paint: { "line-color": c.ring, "line-width": 1, "line-dasharray": [2, 2] } })
		map.addLayer({ id: "devices", type: "circle", source: "devices", paint: {
			"circle-color": roleColor,
			"circle-radius": ["case", ["get", "new"], 8, 5],
			"circle-stroke-width": ["case", ["get", "attention"], 3, ["get", "new"], 2, 1],
			"circle-stroke-color": ["case", ["get", "attention"], "#d1242f", ["get", "new"], "#f3d34a", "#ffffff"],
			"circle-opacity": ["interpolate", ["linear"], ["get", "ago"], 0, 1, 600, 0.35],
		} })
		map.addLayer({ id: "site", type: "circle", source: "site", paint: {
			"circle-color": c.text, "circle-radius": 7, "circle-stroke-width": 3, "circle-stroke-color": c.bg } })

		map.on("click", "devices", (e) => {
			const p = e.features[0].properties
			map.getSource("band").setData(band(p.near, p.far))
			const box = document.createElement("div")
			const a = document.createElement("a")
			a.href = "/device?key=" + encodeURIComponent(p.key)
			a.textContent = p.name || p.mac
			const info = document.createElement("p")
			info.textContent = `${p.sigs ? p.sigs + ". " : ""}${p.rssi} dBm: about ${p.est} m, between ${p.near} and ${p.far} m. Direction not known.`
			box.append(a, info)
			new maplibregl.Popup({ closeButton: true }).setLngLat(e.lngLat).setDOMContent(box).addTo(map)
		})
		map.on("mouseenter", "devices", () => { map.getCanvas().style.cursor = "pointer" })
		map.on("mouseleave", "devices", () => { map.getCanvas().style.cursor = "" })
		refresh()
		setInterval(() => { if (!document.hidden) refresh() }, 3000)
	})

	async function refresh() {
		try {
			const r = await fetch("/map.json" + (query ? "?" + query : ""), { headers: { "FX-Request": "true" } })
			if (!r.ok) throw new Error(r.status + " " + r.statusText)
			const d = await r.json()
			map.getSource("devices").setData(d.devices)
			if (!ringLabels.length) {
				map.getSource("rings").setData(d.rings)
				ringLabels = d.rings.features.map((f) => {
					const span = document.createElement("span")
					span.className = "ring-label"
					span.textContent = f.properties.m + " m"
					return new maplibregl.Marker({ element: span }).setLngLat(f.geometry.coordinates[0]).addTo(map)
				})
			}
			status.textContent = `${d.devices.features.length} radios on the map.` +
				(d.no_distance ? ` ${d.no_distance} have no signal level in dBm and are not on it.` : "")
		} catch (err) {
			status.textContent = "Cannot get map data: " + err.message
		}
	}
})()
