// New API Task Plugin API v1. Standalone source; no dependencies or embedded keys.
const MODELS = ["Minimax-H3-768p-5-15s", "Minimax-H3-1080p-5-15s", "Minimax-H3-1440p-5-15s", "Minimax-H3-768p-933-10s-15s", "MiniMax-H3-933-1080p-10-15s", "MiniMax-H3-933-1440p-10-15s", "MiniMax-H3-933-1080p-15s"];
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "3:2", "2:3"];
export const meta = {
  apiVersion: 1, key: "huiqu-h3-range", name: "Huiqu MiniMax H3 930 / 933",
  version: "1.0.2", author: { name: "OOpii" },
  description: { en: "Huiqu H3 930 / 933 video generation, billed per second", zh: "汇取云 H3 930 / 933，按秒计费" },
  website: "https://huiquyunapi.apifox.cn/531227406e0", baseUrl: "https://api.bjhuiqu.net",
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
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) throw new Error("seconds must be an integer from 5 to 15");
  const count = Number(value);
  if (!Number.isInteger(count) || count < 5 || count > 15) throw new Error("seconds must be an integer from 5 to 15");
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
  if (!MODELS.includes(model) || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported or mismatched channel model");
  allowed(req, ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "size", "images", "audio_urls", "video_urls", "metadata", "n", "stream"], "request field");
  const family933 = model.includes("933"), resolution = /(?:768|1080|1440)p/.exec(model)[0];
  const legacy = model === MODELS[6], defaultSeconds = family933 ? 15 : 5;
  const seconds = duration(alias(req.seconds === undefined ? undefined : duration(req.seconds), req.duration === undefined ? undefined : duration(req.duration), "seconds/duration") ?? defaultSeconds);
  if ((family933 && seconds < 10) || (legacy && seconds !== 15)) throw new Error("933 requires 10–15 seconds; legacy fixed model requires 15");
  if (typeof req.prompt !== "string" || !req.prompt.trim() || [...req.prompt].length > (family933 && resolution === "1080p" ? 2500 : 7000)) throw new Error("Invalid prompt length");
  const ratio = alias(req.aspect_ratio, req.ratio, "aspect ratio") || "16:9";
  if (!RATIOS.includes(ratio) || (!family933 && !["16:9", "9:16"].includes(ratio)) || (family933 && resolution === "768p" && ratio !== "16:9")) throw new Error("Unsupported aspect ratio");
  if (req.size !== undefined && req.size !== resolution) throw new Error("Size must match model resolution");
  if (req.n !== undefined && req.n !== 1) throw new Error("Only one video per task is supported");
  if (req.stream !== undefined && req.stream !== false) throw new Error("Use asynchronous JSON submission");
  const metadata = req.metadata === undefined ? {} : object(req.metadata, "metadata");
  allowed(metadata, ["audio_urls", "video_urls"], "metadata field");
  const images = urls(req.images, "images", 9);
  const audios = urls(alias(req.audio_urls, metadata.audio_urls, "audio URLs"), "audio_urls", 3);
  const videos = urls(alias(req.video_urls, metadata.video_urls, "video URLs"), "video_urls", 3);
  if ((!family933 || resolution !== "1080p") && !images.length) throw new Error("At least one reference image is required");
  if ((!family933 || resolution === "768p") && videos.length) throw new Error("This model does not support reference video");
  if (images.length + audios.length + videos.length > 12) throw new Error("At most 12 reference media items");
  const body = { model, prompt: req.prompt, images };
  if (!legacy) body.seconds = seconds;
  if (!family933) body.ratio = ratio;
  else if (resolution !== "768p") body.aspect_ratio = ratio;
  if (family933 && resolution !== "1080p") body.size = resolution;
  if (family933 && resolution === "1080p") {
    if (audios.length || videos.length) { body.metadata = {}; if (audios.length) body.metadata.audio_urls = audios; if (videos.length) body.metadata.video_urls = videos; }
  } else { if (audios.length) body.audio_urls = audios; if (videos.length) body.video_urls = videos; }
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "Huiqu H3 generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model), upstream = ctx.upstreamModel || ctx.model;
  if (typeof upstream !== "string" || !upstream.trim() || upstream !== upstream.trim() || /[\u0000-\u001f\u007f]/.test(upstream)) throw new Error("Invalid upstream model mapping");
  body.model = upstream;
  return { url: endpoint(ctx.baseUrl), method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
}
export function extractUsage(ctx) {
  const body = normalize(ctx.requestBody, ctx.model);
  return { seconds: body.seconds === undefined ? 15 : body.seconds };
}
export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("Huiqu rejected task submission (HTTP " + resp.statusCode + ")");
  const body = object(resp.body, "Upstream response"), request = normalize(ctx.requestBody, ctx.model);
  const id = taskId(alias(body.task_id, body.id, "upstream task ID"));
  const parsed = { taskId: id, taskData: { ...body, seconds: String(request.seconds === undefined ? 15 : request.seconds), resolution: /(?:768|1080|1440)p/.exec(ctx.model)[0], aspect_ratio: request.ratio || request.aspect_ratio || "16:9" } };
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
    // Huiqu promises authenticated /content after success, without requiring a result URL.
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
  const url = ((ctx.data || {}).metadata || {}).url;
  const origin = /^https?:\/\/[^/]+/i.exec(canonical)[0].toLowerCase();
  // The documented metadata.url is public media, not necessarily a .mp4 path. Never send channel keys to it.
  if (publicUrl(url) && !url.includes("#")
      && /^https?:\/\/[^/]+/i.exec(url)[0].toLowerCase() !== origin
      && (!ctx.apiKey || (!url.includes(ctx.apiKey) && !url.includes(encodeURIComponent(ctx.apiKey))))) {
    return { url, method: "GET", credentialless: true };
  }
  // Resolve Huiqu's authenticated endpoint without following its redirect. Its
  // public media host must never receive the channel Authorization header.
  const probe = utils.fetch({ url: canonical, method: "HEAD", headers: auth(ctx) });
  if ([301, 302, 303, 307, 308].includes(probe.status)) {
    const headers = probe.headers || {};
    const key = Object.keys(headers).find(function (name) { return name.toLowerCase() === "location"; });
    const target = key ? headers[key] : "";
    if (!publicUrl(target) || !target.startsWith("https://") || target.includes("#")
        || (ctx.apiKey && (target.includes(ctx.apiKey) || target.includes(encodeURIComponent(ctx.apiKey))))) {
      throw new Error("Invalid public artifact redirect");
    }
    // The host still validates the target and every redirect against SSRF rules.
    return { url: target, method: "GET", credentialless: true };
  }
  return { url: canonical, method, headers: auth(ctx) };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    // Validate candidate capabilities before routing; retries decode the original request.
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.images.length ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) { try { result.seconds = String(duration(body.duration === undefined ? body.seconds : body.duration)); } catch (_) { /* Keep unknown upstream values private. */ } }
    if (["768p", "1080p", "1440p"].includes(body.resolution)) result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // No protected upstream URL is exposed. Canvas uses its authenticated relay /content fallback.
    // Public ID/status/model/timestamps are supplied by the host.
    return result;
  },
} };
