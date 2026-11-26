// Standalone New API task plugin. Channel configuration owns the key and model mapping.
const MODEL = "magi2-video";
const RESOLUTIONS = ["480p", "720p", "1080p"];
const RATIOS = ["16:9", "9:16", "4:3", "3:4", "1:1", "21:9"];
const MEDIA = [
  ["images", "reference_image_urls", 10],
  ["videos", "reference_video_urls", 5],
  ["audios", "reference_audio_urls", 3],
];
export const meta = {
  apiVersion: 1, key: "shuikule-magi2", name: "Shuikule MAGI 2", version: "1.1.0",
  author: { name: "OOpii" }, description: { zh: "MAGI 2 多素材视频生成", en: "MAGI 2 multimodal video generation" },
  website: "https://shuikule.top/api-doc", baseUrl: "https://shuikule.top",
  auth: "api_key", models: [MODEL], protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: {
    resolution: { enum: RESOLUTIONS, description: { zh: "输出分辨率", en: "Output resolution" } },
    video_count: { type: "number", unit: "count", unitLabel: { zh: "条", en: "video" },
      description: { zh: "视频生成单价", en: "Video generation unit price" } },
  },
  usageExamples: RESOLUTIONS.map(resolution => ({ label: resolution, facts: { resolution, video_count: 1 } })),
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}
function allowed(value, keys, label) {
  for (const key of Object.keys(value)) if (!keys.includes(key)) throw new Error("Unsupported " + label + ": " + key);
}
function duration(value) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) throw new Error("seconds must be an integer from 4 to 30");
  const count = Number(value);
  if (!Number.isInteger(count) || count < 4 || count > 30) throw new Error("seconds must be an integer from 4 to 30");
  return count;
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
  if (model !== MODEL || (req.model !== undefined && req.model !== MODEL)) throw new Error("Unsupported model");
  allowed(req, ["model", "prompt", "ratio", "aspect_ratio", "resolution", "seconds", "duration", "images", "videos", "audios"], "request field");
  if (typeof req.prompt !== "string" || !req.prompt.trim() || Array.from(req.prompt).length > 30000) {
    throw new Error("prompt must contain 1 to 30000 characters");
  }
  const seconds = duration(req.seconds ?? req.duration ?? 5);
  if (req.duration !== undefined && duration(req.duration) !== seconds) throw new Error("Conflicting seconds/duration");
  const ratio = alias(req.ratio, req.aspect_ratio, "aspect ratio") ?? "16:9";
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect ratio");
  const resolution = req.resolution ?? "1080p";
  if (!RESOLUTIONS.includes(resolution)) throw new Error("Unsupported resolution");

  const body = { model, prompt: req.prompt, duration: seconds, ratio, resolution };
  let count = 0;
  for (const [field, , limit] of MEDIA) {
    const refs = urls(req[field], field, limit);
    if (refs.length) body[field] = refs;
    count += refs.length;
  }
  if (count > 18) throw new Error("At most 18 references are allowed");
  return body;
}
function endpoint(baseUrl) {
  if (!/^https:\/\/shuikule\.top(?:\/v1)?\/?$/.test(baseUrl)) throw new Error("Invalid Shuikule channel URL");
  return "https://shuikule.top/v1/videos";
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "MAGI 2 generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const req = normalize(ctx.requestBody, ctx.model);
  if (!ctx.upstreamModel || ctx.upstreamModel === MODEL) throw new Error("Missing upstream model mapping");
  const metadata = { __workbench_passthrough: { aspect_ratio: req.ratio, resolution: req.resolution } };
  const refs = {};
  for (const [field, upstreamField] of MEDIA) {
    if (req[field]) refs[upstreamField] = req[field];
  }
  if (Object.keys(refs).length) metadata.__workbench_refs = refs;
  return {
    url: endpoint(ctx.baseUrl), method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" },
    body: { model: ctx.upstreamModel, prompt: req.prompt, seconds: req.duration, metadata },
  };
}
export function extractUsage(ctx) {
  return { resolution: normalize(ctx.requestBody, ctx.model).resolution, video_count: 1 };
}
export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("Shuikule rejected task submission (HTTP " + resp.statusCode + ")");
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
    // Shuikule documents authenticated /content after success, without requiring a result URL.
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
  const canonical = endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.upstreamTaskId)) + "/content";
  const headers = auth(ctx);
  return { url: canonical, method, headers };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    // Validate candidate capabilities before routing; retries decode the original request.
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.images || body.videos || body.audios ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) { try { result.seconds = String(duration(body.duration === undefined ? body.seconds : body.duration)); } catch (_) { /* Keep unknown upstream values private. */ } }
    if (RESOLUTIONS.includes(body.resolution)) result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // No protected upstream URL is exposed. Canvas uses its authenticated relay /content fallback.
    // Public ID/status/model/timestamps are supplied by the host.
    return result;
  },
} };
