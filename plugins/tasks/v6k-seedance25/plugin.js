// New API Task Plugin API v1. Standalone source; no dependencies or embedded keys.
const MODELS = ["全能视频2.5-301010-720-聚合"];
const MEDIA = {
  images: ["images", "image", "image_url", "image_urls", "reference_image_urls", "referenceImages", "reference_images", "imageUrls"],
  videos: ["videos", "video", "video_url", "video_urls", "reference_video_urls", "referenceVideos", "reference_videos", "videoUrls", "video_refs", "reference_video", "reference_video_url"],
  audios: ["audios", "audio", "audio_url", "audio_urls", "reference_audio_urls", "referenceAudios", "reference_audios", "audioUrls"],
};
const MAX_REFS = { images: 30, videos: 0, audios: 10 };
const RATIOS = ["16:9", "9:16", "4:3", "1:1", "3:4", "21:9"];
export const meta = {
  apiVersion: 1, key: "v6k-seedance25", name: "V6K Seedance 2.5",
  version: "1.0.1", author: { name: "OOpii" },
  description: { en: "Video generation via V6K Seedance 2.5", zh: "通过 V6K Seedance 2.5 生成视频" },
  website: "https://www.v6k.top/docs", baseUrl: "https://www.v6k.top",
  // Do not claim channelTypes: type 1 is owned by the factory Sora plugin.
  auth: "api_key", models: MODELS, protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: { seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } } },
  usageExamples: [{ label: "5 seconds", facts: { seconds: 5 } }],
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
  if (typeof model !== "string" || !model.trim() || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported or mismatched channel model");
  allowed(req, ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "resolution", "n", "stream", ...Object.values(MEDIA).flat()], "request field");
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  // Keep the shared 720p model identity and ratio; use the documented duration default.
  const count = duration(req.seconds === undefined ? (req.duration === undefined ? 5 : req.duration) : req.seconds);
  if (req.duration !== undefined && duration(req.duration) !== count) throw new Error("Conflicting seconds/duration");
  const ratio = alias(req.aspect_ratio, req.ratio, "aspect_ratio/ratio") || "16:9";
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect ratio");
  if (req.resolution !== undefined && req.resolution !== "720p") throw new Error("Resolution must be 720p");
  if (req.n !== undefined && req.n !== 1) throw new Error("Only one video per task is supported");
  if (req.stream !== undefined && req.stream !== false) throw new Error("Use asynchronous JSON submission");
  const body = { model, prompt: req.prompt, duration: count, ratio, resolution: "720p" };
  for (const kind of Object.keys(MEDIA)) {
    let refs;
    for (const field of MEDIA[kind]) {
      if (req[field] === undefined) continue;
      const input = typeof req[field] === "string" ? [req[field]] : req[field];
      if (kind === "videos" && Array.isArray(input) && input.length) throw new Error("V6K Seedance 2.5 does not accept reference videos");
      refs = alias(refs, urls(input, kind, MAX_REFS[kind]), kind + " aliases");
    }
    if (refs && refs.length) body[kind] = refs;
  }
  return body;
}
function endpoint(baseUrl) {
  const base = String(baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(base)) throw new Error("Channel Base URL must be an upstream origin, optionally ending in /v1");
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "V6K Seedance 2.5 generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model), upstream = ctx.upstreamModel;
  if (typeof upstream !== "string" || !upstream.trim()) throw new Error("Missing upstream model ID");
  const { duration: seconds, ...fields } = body;
  return {
    url: endpoint(ctx.baseUrl), method: "POST",
    headers: { ...auth(ctx), "Content-Type": "application/json" },
    body: { ...fields, model: upstream, seconds },
  };
}
export function extractUsage(ctx) {
  // Keep the shared usage schema; the existing request-unit price is not multiplied by duration.
  return { seconds: normalize(ctx.requestBody, ctx.model).duration };
}
export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("V6K rejected task submission (HTTP " + resp.statusCode + ")");
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
    // V6K documents authenticated /content after success, without requiring a result URL.
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
  // V6K may serve HEAD directly but redirect GET to a URL signed for GET.
  let lookup;
  try {
    lookup = utils.fetch({ url: canonical, method: "GET", headers: { ...headers, Range: "bytes=0-0" }, responseType: "bytes" });
  } catch (_) {
    // Direct video responses may ignore Range and exceed the hook's body limit.
    lookup = utils.fetch({ url: canonical, method: "HEAD", headers });
  }
  if ([301, 302, 303, 307, 308].includes(lookup.status)) {
    const url = lookup.headers.Location;
    if (!publicUrl(url) || !url.startsWith("https://") || url.includes("#")) {
      throw new Error("Invalid upstream video download URL");
    }
    return { url, method: "GET", credentialless: true };
  }
  return { url: canonical, method, headers };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    // Validate candidate capabilities before routing; retries decode the original request.
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.images ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) { try { result.seconds = String(duration(body.duration === undefined ? body.seconds : body.duration)); } catch (_) { /* Keep unknown upstream values private. */ } }
    if (body.resolution === "720p") result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // No protected upstream URL is exposed. Canvas uses its authenticated relay /content fallback.
    // Public ID/status/model/timestamps are supplied by the host.
    return result;
  },
} };
