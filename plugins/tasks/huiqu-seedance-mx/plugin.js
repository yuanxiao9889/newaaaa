// New API Task Plugin API v1. Upload this file; no npm dependencies.
const MODELS = {"全能视频2.0-933-720-tem": "sd2.0-mx720p", "全能视频2.5-301010-720-tem": "sd2.5-mx720p"};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
export const meta = {
  apiVersion: 1, key: "huiqu-seedance-mx", name: "Huiqu Seedance MX",
  version: "1.0.1", author: { name: "OOpii" },
  description: { en: "720p image-to-video with SD2.0 and SD2.5", zh: "SD2.0 与 SD2.5 的 720p 参考图视频生成" },
  website: "https://huiquyunapi.apifox.cn/521585917e0", baseUrl: "https://api.bjhuiqu.net",
  auth: "api_key", models: Object.keys(MODELS), fetchMode: "per_task", protocols: ["openai_video"],
  usageSchema: {}, usageExamples: [],
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}
function seconds(value) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) return null;
  const number = Number(value);
  return Number.isInteger(number) && [5, 10, 15, 30].includes(number) ? number : null;
}
function alias(a, b, label) {
  if (a !== undefined && b !== undefined && JSON.stringify(a) !== JSON.stringify(b)) throw new Error("Conflicting " + label);
  return a === undefined ? b : a;
}
function publicUrl(value) {
  if (typeof value !== "string" || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  const match = /^https?:\/\/([a-z0-9.-]+)(?::([1-9][0-9]{0,4}))?(?:[/?#]|$)/i.exec(value);
  if (!match) return false;
  if (match[2] && Number(match[2]) > 65535) return false;
  const host = match[1].toLowerCase(), labels = host.split(".");
  if (labels.length < 2 || /(?:^|\.)(localhost|local|internal)$/.test(host)) return false;
  if (labels.some(function (label) { return !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label); })) return false;
  return /[a-z]/.test(labels[labels.length - 1]) && !/^(?:[0-9.]+|0x[0-9a-f.]+)$/i.test(host);
}
function media(value, label, limit) {
  if (value === undefined) return [];
  if (!Array.isArray(value) || value.length > limit) throw new Error(label + " allows at most " + limit + " URLs");
  return value.map(function (url) {
    if (!publicUrl(url)) throw new Error(label + " requires public HTTP(S) domain URLs");
    return url;
  });
}
function normalize(req, model) {
  object(req, "Request");
  if (!Object.prototype.hasOwnProperty.call(MODELS, model) || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported or mismatched channel model");
  if (typeof req.prompt !== "string" || !req.prompt.trim() || Array.from(req.prompt).length > 2000) throw new Error("prompt requires 1 to 2000 characters");
  const allowed = ["model", "prompt", "seconds", "duration", "aspect_ratio", "ratio", "resolution", "images", "n", "stream"];
  for (const key of Object.keys(req)) if (!allowed.includes(key)) throw new Error("Unsupported request field: " + key);
  const fallback = MODELS[model] === "sd2.5-mx720p" ? 30 : 5;
  const count = seconds(req.duration === undefined ? (req.seconds === undefined ? fallback : req.seconds) : req.duration);
  if (!(fallback === 30 ? count === 30 : [5, 10, 15].includes(count))) throw new Error("Unsupported duration for this model");
  if (req.seconds !== undefined && seconds(req.seconds) !== count) throw new Error("Conflicting seconds/duration");
  const ratio = alias(req.aspect_ratio, req.ratio, "aspect_ratio/ratio") || "16:9";
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect ratio");
  if (req.resolution !== undefined && req.resolution !== "720p") throw new Error("Only 720p is supported");
  if (req.n !== undefined && req.n !== 1) throw new Error("Only one video per task is supported");
  if (req.stream !== undefined && req.stream !== false) throw new Error("Use asynchronous JSON submission");
  // Start with the documented single reference-image workflow. Do not infer
  // multi-media limits or video/audio field names from other Huiqu models.
  const images = media(req.images, "images", 1);
  if (!images.length) throw new Error("A reference image is required");
  return { model, prompt: req.prompt, duration: count, ratio, resolution: "720p", images };
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
function resultUrl(body) {
  if (!body || typeof body !== "object") return null;
  const result = body.result || {}, metadata = body.metadata || {};
  return [metadata.url, body.result_url, body.video_url, body.url, result.result_url, result.video_url, result.url].find(function (url) {
    return publicUrl(url) && !/\/v1\/videos\/[^/?#]+(?:\/content)?(?:[?#]|$)/i.test(url)
      && !/\.(?:png|jpe?g|webp|gif|mp3|wav|json|html)(?:[?#]|$)/i.test(url);
  }) || null;
}
function safeReason(body, ctx) {
  const error = body.error || {};
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "Huiqu Seedance MX generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}

export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const upstream = MODELS[ctx.model];
  if (!upstream || (ctx.upstreamModel && ctx.upstreamModel !== upstream)) throw new Error("Channel model mapping does not match the selected MX model");
  const body = { ...normalize(ctx.requestBody, ctx.model), model: upstream };
  // No automatic POST retries or undocumented idempotency guarantees in this plugin.
  return { url: endpoint(ctx.baseUrl) + "/generations", method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
}
export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("Huiqu rejected task submission (HTTP " + resp.statusCode + ")");
  const body = object(resp.body, "Upstream response");
  const id = taskId(alias(body.task_id, body.id, "upstream task ID"));
  // JS accepts numeric and string seconds; never deserialize into the Sora Go struct.
  const request = normalize(ctx.requestBody, ctx.model);
  const taskData = { ...body, resolution: "720p", ratio: request.ratio, seconds: String(request.duration) };
  const parsed = { taskId: id, taskData };
  const result = parseTaskResult({ taskId: id, apiKey: ctx.apiKey }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}
export function extractUsage(ctx) {
  // One video per request; do not multiply a per-video price by its duration.
  normalize(ctx.requestBody, ctx.model);
  return {};
}
export function buildQueryRequest(ctx) {
  return { url: endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.taskId)), method: "GET", headers: auth(ctx) };
}
export function parseTaskResult(ctx, body, response) {
  if (response && (response.status < 200 || response.status >= 300)) return { status: "UNKNOWN" };
  if (!body || typeof body !== "object" || Array.isArray(body) || typeof body.status !== "string") return { status: "UNKNOWN" };
  const id = body.task_id || body.id;
  if (id !== ctx.taskId || (body.task_id !== undefined && body.task_id !== id) || (body.id !== undefined && body.id !== id)) return { status: "UNKNOWN" };
  const statuses = { queued: "QUEUED", pending: "QUEUED", submitted: "SUBMITTED", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", running: "IN_PROGRESS", succeeded: "SUCCESS", success: "SUCCESS", completed: "SUCCESS", failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE" };
  const key = body.status.trim().toLowerCase();
  if (!Object.prototype.hasOwnProperty.call(statuses, key)) return { status: "UNKNOWN" };
  const status = statuses[key];
  if (status === "SUCCESS") {
    const url = resultUrl(body);
    return url ? { status, progress: "100%", url } : { status: "IN_PROGRESS", progress: "99%" };
  }
  if (status === "FAILURE") return { status, reason: safeReason(body, ctx) };
  const progress = Number(body.progress);
  return { status, progress: Number.isFinite(progress) ? Math.max(0, Math.min(99, progress)) + "%" : "0%" };
}
export function listArtifacts(task) {
  return task.status === "SUCCESS" && resultUrl(task.data) ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const url = resultUrl(ctx.data), method = (ctx.clientRequest || {}).method;
  if (!url) throw new Error("artifact_gone");
  if (!["GET", "HEAD"].includes(method)) throw new Error("Unsupported content method");
  // The upstream documents a public CDN link, not /content. Never attach any key.
  // The host supplies safe client Range headers and applies its SSRF policy.
  return { url, method, credentialless: true };
}
export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
      const body = normalize(ctx.body.value, ctx.model);
      return { kind: "submit", model: ctx.model, action: "image_to_video", requestBody: body };
    },
    render: function (_ctx, task) {
      const body = task.data || {}, result = {};
      const count = seconds(body.seconds) || seconds(body.duration);
      if (count) result.seconds = String(count);
      if (["480p", "720p"].includes(body.resolution)) result.resolution = body.resolution;
      const ratio = body.aspect_ratio || body.ratio;
      if (RATIOS.includes(ratio)) result.aspect_ratio = ratio;
      const url = task.status === "SUCCESS" && resultUrl(body);
      if (url) { result.url = url; result.video_url = url; result.result_url = url; result.metadata = { url }; }
      if (task.status === "FAILURE") {
        const reason = safeReason({ fail_reason: task.fail_reason }, {});
        result.fail_reason = reason;
        result.error = { code: "generation_failed", message: reason };
      }
      // The host owns IDs, status and timestamps; expose the persisted failure reason.
      return result;
    },
  },
};
