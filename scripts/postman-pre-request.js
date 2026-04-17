function safeHeaders() {
	const items = [];
	try {
		pm.request.headers.each(h => {
			items.push({
				key: h.key,
				value: h.value,
				disabled: !!h.disabled
			});
		});
	} catch (e) { }
	return items;
}

function safeAuth() {
	try {
		if (!pm.request.auth) return null;
		return {
			type: pm.request.auth.type || null
		};
	} catch (e) {
		return null;
	}
}

function safeBody() {
	try {
		const b = pm.request.body;
		if (!b) return null;

		const out = { mode: b.mode || null };

		if (b.mode === "raw") {
			out.raw = b.raw || "";
			return out;
		}

		if (b.mode === "graphql") {
			out.graphql = b.graphql || null;
			return out;
		}

		if (b.mode === "urlencoded") {
			out.urlencoded = (b.urlencoded || []).map(x => ({
				key: x.key,
				value: x.value,
				disabled: !!x.disabled
			}));
			return out;
		}

		if (b.mode === "formdata") {
			out.formdata = (b.formdata || []).map(x => ({
				key: x.key,
				value: x.value,
				type: x.type,
				src: x.src,
				disabled: !!x.disabled
			}));
			return out;
		}

		if (b.mode === "file") {
			out.file = b.file || null;
			return out;
		}

		return out;
	} catch (e) {
		return { error: String(e) };
	}
}

const location = Array.isArray(pm.execution.location) ? pm.execution.location : [];
const payload = {
	phase: "start",
	requestId: pm.info.requestId || null,
	requestName: pm.info.requestName || pm.execution.location?.current || "unknown",
	location,
	current: pm.execution.location?.current || "unknown",
	eventName: pm.info.eventName || null,
	method: pm.request.method || "GET",
	url: pm.request.url.toString(),
	headers: safeHeaders(),
	auth: safeAuth(),
	body: safeBody(),
	time: Date.now() / 1000,
	isWrite: false
};

pm.sendRequest({
	url: "http://127.0.0.1:8765/heartbeat",
	method: "POST",
	header: {
		"Content-Type": "application/json"
	},
	body: {
		mode: "raw",
		raw: JSON.stringify(payload)
	}
}, function (err) {
	if (err) {
		console.log("postman-wakatime collector error:", err);
	}
});
