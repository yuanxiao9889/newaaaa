// New API Task Plugin API v1. Channel-bound driver for the shared wan3.0-video model.
const MODEL = "wan3.0-video";
const UPSTREAM_MODEL = "wan3.0-prime-720P";
const RATIOS = ["16:9", "9:16"];

export const meta = {
  apiVersion: 1, key: "xing-wan30-prime", name: "XingAPI Wan 3.0 Prime",
  version: "1.0.1", author: { name: "OOpii" },
  description: { en: "Wan 3.0 video generation through XingAPI", zh: "通过星光 API 生成 Wan 3.0 视频" },
  website: "https://xingapi.top", baseUrl: "https://xingapi.top",
  auth: "api_key", models: [MODEL], protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: ["720P"], description: { en: "Output video resolution", zh: "输出视频分辨率" } },
  },
  usageExamples: [{ label: "720p · 5s", facts: { seconds: 5, resolution: "720P" } }],
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}

function duration(value) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) throw new Error("seconds must be an integer from 5 to 30");
  const count = Number(value);
  // Preserve the existing XingAPI Wan model's stricter 5–30s provider bound.
  // This is below relaycommon.MaxTaskDurationSeconds, including on replay.
  if (!Number.isInteger(count) || count < 5 || count > 30) throw new Error("seconds must be an integer from 5 to 30");
  return count;
}

function publicUrl(value) {
  if (typeof value !== "string" || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  const match = /^https?:\/\/([a-z0-9.-]+)(?::([1-9][0-9]{0,4}))?(?:[/?#]|$)/i.exec(value);
  if (!match || (match[2] && Number(match[2]) > 65535)) return false;
  const host = match[1].toLowerCase(), labels = host.split(".");
  return labels.length > 1 && !/(?:^|\.)(localhost|local|internal)$/.test(host)
    && labels.every(function (label) { return /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label); })
    && /[a-z]/.test(labels[labels.length - 1]) && !/^(?:[0-9.]+|0x[0-9a-f.]+)$/i.test(host);
}

function normalize(req, model) {
  object(req, "Request");
  if (model !== MODEL || req.model !== model) throw new Error("Unsupported or mismatched channel model");
  const allowed = ["model", "prompt", "duration", "seconds", "ratio", "aspect_ratio", "resolution", "images", "videos", "audios"];
  for (const key of Object.keys(req)) if (!allowed.includes(key)) throw new Error("Unsupported request field: " + key);
  if (typeof req.prompt !== "string" || !req.prompt.trim() || Array.from(req.prompt).length > 20000) throw new Error("prompt requires 1 to 20000 characters");
  const count = duration(req.duration === undefined ? req.seconds : req.duration);
  if (req.seconds !== undefined && duration(req.seconds) !== count) throw new Error("Conflicting duration/seconds");
  const ratio = req.ratio === undefined ? (req.aspect_ratio === undefined ? "16:9" : req.aspect_ratio) : req.ratio;
  if (req.aspect_ratio !== undefined && req.aspect_ratio !== ratio) throw new Error("Conflicting ratio/aspect_ratio");
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect ratio");
  if (req.resolution !== undefined && String(req.resolution).toLowerCase() !== "720p") throw new Error("Resolution must be 720p");
  const body = { model, prompt: req.prompt, duration: count, ratio, resolution: "720p" };
  for (const kind of ["images", "videos", "audios"]) {
    const values = req[kind];
    if (values === undefined) continue;
    // Match the already deployed XingAPI Wan adapter's reference limits.
    const limit = kind === "images" ? 10 : 5;
    if (!Array.isArray(values) || values.length > limit) throw new Error(kind + " allows at most " + limit + " URLs");
    for (const value of values) if (!publicUrl(value)) throw new Error(kind + " requires public HTTP(S) domain URLs");
    if (values.length) body[kind] = values.slice();
  }
  return body;
}

function endpoint(baseUrl) {
  const base = String(baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(base)) throw new Error("Channel Base URL must be an origin, optionally ending in /v1");
  return base + (base.endsWith("/v1") ? "/videos" : "/v1/videos");
}

function taskId(value) {
  if (typeof value !== "string" || !/^[a-z0-9][a-z0-9_.:-]{0,255}$/i.test(value)) throw new Error("Invalid upstream task ID");
  return value;
}

function auth(ctx) {
  if (typeof ctx.apiKey !== "string" || !ctx.apiKey.trim()) throw new Error("Channel API key is required");
  return { Authorization: "Bearer " + ctx.apiKey };
}

function safeReason(body, ctx) {
  const error = body.error || {};
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "XingAPI Wan 3.0 generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}

export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model);
  if (ctx.upstreamModel !== UPSTREAM_MODEL) throw new Error("Channel mapping must select " + UPSTREAM_MODEL);
  body.model = ctx.upstreamModel;
  return { url: endpoint(ctx.baseUrl) + "/generations", method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
}

export function extractUsage(ctx) {
  const request = normalize(ctx.requestBody, ctx.model);
  // Keep one duration multiplier and the existing shared model's uppercase enum.
  if (ctx.usagePurpose === "billing_ratios") return { seconds: request.duration };
  return { seconds: request.duration, resolution: "720P" };
}

export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("XingAPI rejected task submission (HTTP " + resp.statusCode + ")");
  const body = object(resp.body, "Upstream response"), request = normalize(ctx.requestBody, ctx.model);
  if (body.id !== undefined && body.task_id !== undefined && body.id !== body.task_id) throw new Error("Conflicting upstream task IDs");
  const id = taskId(body.task_id === undefined ? body.id : body.task_id);
  const parsed = { taskId: id, taskData: { ...body, seconds: String(request.duration), resolution: request.resolution, aspect_ratio: request.ratio } };
  const result = parseTaskResult({ ...ctx, taskId: id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}

export function buildQueryRequest(ctx) {
  return { url: endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.taskId)), method: "GET", headers: auth(ctx) };
}

export function parseTaskResult(ctx, body, response) {
  if (response && (response.status < 200 || response.status >= 300)) return { status: "UNKNOWN" };
  if (!body || typeof body !== "object" || Array.isArray(body) || typeof body.status !== "string") return { status: "UNKNOWN" };
  const id = body.task_id === undefined ? body.id : body.task_id;
  if (id !== ctx.taskId || (body.id !== undefined && body.id !== id)) return { status: "UNKNOWN" };
  const statuses = { queued: "QUEUED", pending: "QUEUED", running: "IN_PROGRESS", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", done: "SUCCESS", finished: "SUCCESS", completed: "SUCCESS", succeeded: "SUCCESS", success: "SUCCESS", error: "FAILURE", canceled: "FAILURE", failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE" };
  const key = body.status.trim().toLowerCase();
  if (!Object.prototype.hasOwnProperty.call(statuses, key)) return { status: "UNKNOWN" };
  const status = statuses[key];
  if (status === "SUCCESS") return { status, progress: "100%" };
  if (status === "FAILURE") return { status, reason: safeReason(body, ctx) };
  const progress = Number(String(body.progress === undefined ? "0" : body.progress).replace(/%$/, ""));
  return { status, progress: Number.isFinite(progress) ? Math.max(0, Math.min(99, progress)) + "%" : "0%" };
}

export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const method = (ctx.clientRequest || {}).method;
  if (!["GET", "HEAD"].includes(method)) throw new Error("Unsupported content method");
  const canonical = endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.upstreamTaskId)) + "/content";
  const body = ctx.data || {};
  const origin = /^https?:\/\/[^/]+/i.exec(canonical)[0].toLowerCase();
  const result = [body.video_url, body.result_url, body.url, (body.metadata || {}).url].find(function (url) {
    return publicUrl(url) && !url.includes("#")
      && /^https?:\/\/[^/]+/i.exec(url)[0].toLowerCase() !== origin
      && (!ctx.apiKey || (!url.includes(ctx.apiKey) && !url.includes(encodeURIComponent(ctx.apiKey))));
  });
  // Retain the existing XingAPI adapter's OSS download fallback. Never send a
  // channel key to a result host; host SSRF/redirect/Range checks still apply.
  if (result) return { url: result, method: "GET", credentialless: true };
  return { url: canonical, method, headers: auth(ctx) };
}

export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    // Validate during candidate selection, so MAI remains eligible for requests
    // this 720p provider cannot handle. Retry replays the original client body.
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) {
      try { result.seconds = String(duration(body.seconds === undefined ? body.duration : body.seconds)); } catch (_) { /* Invalid upstream values remain private. */ }
    }
    if (String(body.resolution || "").toLowerCase() === "720p") result.resolution = "720p";
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // The host owns stable public task IDs and authenticated content URLs.
    return result;
  },
} };
