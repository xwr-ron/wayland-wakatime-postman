const location = Array.isArray(pm.execution.location) ? pm.execution.location : [];

const payload = {
    phase: "finish",
    requestId: pm.info.requestId || null,
    requestName: pm.info.requestName || pm.execution.location?.current || "unknown",
    location,
    current: pm.execution.location?.current || "unknown",
    eventName: pm.info.eventName || null,
    method: pm.request.method || "GET",
    url: pm.request.url.toString(),
    responseCode: pm.response.code,
    responseStatus: pm.response.status,
    responseTimeMs: pm.response.responseTime || 0,
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
