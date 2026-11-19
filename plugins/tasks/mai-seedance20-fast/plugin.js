// New API Task Plugin API v1. Standalone source; no dependencies or embedded keys.
const MODELS = ["全能视频2.0-fast-720"];
const RATIOS = ["16:9"];
export const meta = {
  apiVersion: 1, key: "mai-seedance20-fast", name: "MAI Token Seedance 2.0 Fast Dynamic",
  version: "1.0.0", author: { name: "OOpii" },
  description: { en: "Video generation via MAI Token Seedance 2.0 Fast", zh: "通过MAI Token Seedance 2.0 Fast 生成视频" },
  website: "https://api.mai-token.com", baseUrl: "https://api.mai-token.com",
  // Do not claim channelTypes: type 1 is owned by the factory Sora plugin.
  auth: "api_key", models: MODELS, protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: { seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } } },
  usageExamples: [{ label: "15 seconds", facts: { seconds: 15 } }],
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}
function allowed(value, keys, label) {
  for (const key of Object.keys(value)) if (!keys.includes(key)) throw new Error("Unsupported " + label + ": " + key);
}
function duration(value) {
  if (value !== 15 && value !== "15") throw new Error("Only the verified 15-second profile is enabled");
  return 15;
}
function alias(a, b, label) {
  if (a !== undefined && b !== undefined && JSON.stringify(a) !== JSON.stringify(b)) throw new Error("Conflicting " + label);
  return a === undefined ? b : a;
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
function urls(value, label, limit) {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > limit) throw new Error(label + " allows at most " + limit + " URLs");
  for (const url of value) if (!publicUrl(url)) throw new Error(label + " requires public HTTP(S) domain URLs");
  return value.slice();
}
function normalize(req, model) {
  object(req, "Request");
  if (!MODELS.includes(model) || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported or mismatched model");
  allowed(req, ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "resolution", "n", "stream", "images", "videos", "audios", "watermark"], "request field");
  if (typeof req.prompt !== "string" || !req.prompt.trim() || Array.from(req.prompt).length > 2000) throw new Error("Prompt requires 1-2000 characters");
  const count = duration(req.seconds === undefined ? (req.duration === undefined ? 15 : req.duration) : req.seconds);
  if (req.duration !== undefined && duration(req.duration) !== count) throw new Error("Conflicting duration");
  const ratio = alias(req.aspect_ratio, req.ratio, "ratio") || "16:9";
  if (!RATIOS.includes(ratio) || (req.resolution !== undefined && req.resolution !== "720p")) throw new Error("Only 720p 16:9 is enabled");
  if (req.n !== undefined && req.n !== 1 || req.stream !== undefined && req.stream !== false) throw new Error("One asynchronous video per request");
  if (req.watermark !== undefined && req.watermark !== false) throw new Error("Watermark must be false");
  const body = {model, prompt: req.prompt, duration: count, ratio, resolution: "720p"};
  // Start with the mixed-reference profile under validation, not SD2.5's wider limits.
  for (const kind of ["images", "videos", "audios"]) {
    const values = urls(req[kind], kind, kind === "audios" ? 0 : 1);
    if (values.length) body[kind] = values;
  }
  return body;
}
function endpoint(baseUrl) {
  const base = String(baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(base)) throw new Error("Channel Base URL must be an upstream origin, optionally ending in /v1");
  return base.replace(/\/v1$/, "") + "/api/v3/contents/generations/tasks";
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "MAI Token Seedance 2.0 Fast generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
function videoUrl(body) {
  if (!body || typeof body !== "object") return null;
  return [body.video_url, (body.content || {}).video_url, body.result_url, body.content_url, body.url, (body.metadata || {}).url, (body.result || {}).url].find(function (url) { return publicUrl(url); }) || null;
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model), upstream = ctx.upstreamModel;
  // Public model identity stays stable; only the channel mapping selects the vendor ID.
  if (typeof upstream !== "string" || !upstream.trim() || upstream !== upstream.trim()
      || upstream === ctx.model || /[\u0000-\u001f\u007f]/.test(upstream)) {
    throw new Error("Configure a valid upstream model ID in channel model mapping");
  }
  const content = [{type: "text", text: body.prompt}];
  for (const kind of ["images", "videos"]) {
    const type = kind === "images" ? "image_url" : "video_url";
    const role = kind === "images" ? "reference_image" : "reference_video";
    for (const url of body[kind] || []) content.push({type, role, [type]: {url}});
  }
  const payload = {model: upstream, content, duration: body.duration, ratio: body.ratio, resolution: body.resolution, watermark: false};
  return {url: endpoint(ctx.baseUrl), method: "POST", headers: {...auth(ctx), "Content-Type": "application/json"}, body: payload};
}
export function extractUsage(ctx) {
  // Report requested output duration; keep the administrator's existing per-request price unchanged.
  return { seconds: normalize(ctx.requestBody, ctx.model).duration };
}
export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("MAI Token rejected task submission (HTTP " + resp.statusCode + ")");
  const body = object(resp.body, "Upstream response"), request = normalize(ctx.requestBody, ctx.model);
  const id = taskId(alias(body.task_id, body.id, "upstream task ID"));
  const parsed = { taskId: id, taskData: { ...body, duration: request.duration, seconds: String(request.duration), resolution: request.resolution, aspect_ratio: request.ratio } };
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
  const id = body.task_id || body.id;
  if (id !== ctx.taskId || (body.task_id !== undefined && body.task_id !== id) || (body.id !== undefined && body.id !== id)) return { status: "UNKNOWN" };
  const statuses = { queued: "QUEUED", pending: "QUEUED", running: "IN_PROGRESS", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", done: "SUCCESS", finished: "SUCCESS", completed: "SUCCESS", succeeded: "SUCCESS", success: "SUCCESS", error: "FAILURE", canceled: "FAILURE", failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE" };
  const key = body.status.trim().toLowerCase();
  if (!Object.prototype.hasOwnProperty.call(statuses, key)) return { status: "UNKNOWN" };
  const status = statuses[key];
  if (status === "SUCCESS") {
    if (body.billing_pending === true) return {status: "IN_PROGRESS", progress: "95%"};
    // Ark success exposes content.video_url; canonical /content remains a fallback.
    return { status, progress: "100%" };
  }
  if (status === "FAILURE") return { status, reason: safeReason(body, ctx) };
  const progress = Number(body.progress);
  return { status, progress: Number.isFinite(progress) ? Math.max(0, Math.min(99, progress)) + "%" : "0%" };
}
export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const method = (ctx.clientRequest || {}).method;
  if (!["GET", "HEAD"].includes(method)) throw new Error("Unsupported content method");
  const canonical = String(ctx.baseUrl).replace(/\/+$/, "").replace(/\/v1$/, "") + "/v1/videos/" + encodeURIComponent(taskId(ctx.upstreamTaskId)) + "/content";
  const result = videoUrl(ctx.data);
  // Completed results may use extensionless artifact URLs or MP4 URLs. Fetch without
  // credentials; the host retains SSRF, redirect, Range and task ownership checks.
  // Same-origin API URLs keep the authenticated canonical fallback below.
  const origin = function (url) { return /^https?:\/\/[^/]+/i.exec(url)[0].toLowerCase(); };
  if (result && !result.includes("#")
      && origin(result) !== origin(canonical)
      && (!ctx.apiKey || (!result.includes(ctx.apiKey) && !result.includes(encodeURIComponent(ctx.apiKey))))) {
    // Some OSS result URLs allow GET but reject HEAD (403). The host suppresses
    // the response body for a client HEAD and closes the upstream GET stream.
    return { url: result, method: "GET", credentialless: true };
  }
  return { url: canonical, method, headers: auth(ctx) };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    // Validate candidate capabilities before routing; retries decode the original request.
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.images || body.videos || body.first_image ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) { try { result.seconds = String(duration(body.duration === undefined ? body.seconds : body.duration)); } catch (_) { /* Keep unknown upstream values private. */ } }
    if (["480p", "720p"].includes(body.resolution)) result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // No protected upstream URL is exposed. Canvas uses its authenticated relay /content fallback.
    // Public ID/status/model/timestamps are supplied by the host.
    return result;
  },
} };
