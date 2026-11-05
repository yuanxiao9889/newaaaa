// New API Task Plugin API v1. Standalone source; no embedded credentials.
const MODELS = { "全能视频2.5-301010-c": "seedance2.5-ae-720p" };
const RATIOS = ["16:9", "9:16", "4:3", "3:4", "1:1", "21:9"];
const MEDIA_FIELDS = {
  images: ["images", "image", "image_url", "image_urls", "reference_images", "reference_image_urls", "input_reference", "images_base64"],
  videos: ["videos", "video", "video_url", "video_urls", "reference_videos", "reference_video_urls"],
  audios: ["audios", "audio", "audio_url", "audio_urls", "reference_audios", "reference_audio_urls", "audios_base64"],
};

export const meta = {
  apiVersion: 1, key: "aicost-seedance25", name: "AICost Seedance 2.5",
  version: "1.0.0", author: { name: "OOpii" },
  description: { en: "Seedance 2.5 video generation through AICost", zh: "通过 AICost 生成 Seedance 2.5 视频" },
  website: "https://aicost.me/api-docs", baseUrl: "https://www.aicost.me",
  auth: "api_key", models: Object.keys(MODELS), protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: { seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } } },
  usageExamples: [{ label: "720p · 5s", facts: { seconds: 5 } }],
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}

function duration(value) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) throw new Error("seconds must be an integer from 1 to 30");
  const count = Number(value);
  // The vendor's 30-second bound is stricter than the host task-duration ceiling.
  if (!Number.isInteger(count) || count < 1 || count > 30) throw new Error("seconds must be an integer from 1 to 30");
  return count;
}

function media(value, kind) {
  if (typeof value !== "string" || !value.trim()) throw new Error(kind + " requires a media URL or data URI");
  if (/^https?:\/\/[^\s/@?#]+(?:[/?#]|$)/i.test(value) && !/[\\\u0000-\u001f\u007f]/.test(value)) return value;
  const mime = { images: "image", videos: "video", audios: "audio" }[kind];
  if (new RegExp("^data:" + mime + "/[a-z0-9.+-]+;base64,[a-z0-9+/]+={0,2}$", "i").test(value)) return value;
  throw new Error(kind + " requires an HTTP(S) URL or matching base64 data URI");
}

function normalize(req, model) {
  object(req, "Request");
  if (!Object.prototype.hasOwnProperty.call(MODELS, model) || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported or mismatched channel model");
  const allowed = ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "resolution", "size", "n", "stream", ...Object.values(MEDIA_FIELDS).flat()];
  for (const key of Object.keys(req)) if (!allowed.includes(key)) throw new Error("Unsupported request field: " + key);
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  const count = duration(req.seconds === undefined ? (req.duration === undefined ? 5 : req.duration) : req.seconds);
  if (req.duration !== undefined && duration(req.duration) !== count) throw new Error("Conflicting seconds/duration");
  let ratio = req.aspect_ratio === undefined ? req.ratio : req.aspect_ratio;
  if (req.ratio !== undefined && req.ratio !== ratio) throw new Error("Conflicting aspect_ratio/ratio");
  if (req.size !== undefined) {
    const sizes = { "1280x720": "16:9", "720x1280": "9:16", "960x720": "4:3", "720x960": "3:4", "720x720": "1:1", "1680x720": "21:9" };
    const fromSize = sizes[req.size];
    if (!fromSize || (ratio !== undefined && ratio !== fromSize)) throw new Error("size must match a supported 720p aspect ratio");
    ratio = fromSize;
  }
  if (ratio === undefined) ratio = "16:9";
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect ratio");
  if (req.resolution !== undefined && String(req.resolution).toLowerCase() !== "720p") throw new Error("This channel supports 720p only");
  if (req.n !== undefined && req.n !== 1 && req.n !== "1") throw new Error("Only one video per task is supported");
  if (req.stream !== undefined && req.stream !== false && req.stream !== "false") throw new Error("Use asynchronous video creation");
  const body = { model, prompt: req.prompt, seconds: count, aspect_ratio: ratio, resolution: "720p" };
  for (const kind of Object.keys(MEDIA_FIELDS)) {
    const values = [];
    for (const field of MEDIA_FIELDS[kind]) {
      const input = req[field];
      if (input === undefined) continue;
      for (const value of Array.isArray(input) ? input : [input]) {
        const url = media(value, kind);
        if (!values.includes(url)) values.push(url);
      }
    }
    if (values.length > (kind === "images" ? 30 : 10)) throw new Error("Too many " + kind);
    if (values.length) body[kind] = values;
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message].find(function (v) { return typeof v === "string" && v.trim(); }) || "AICost video generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}

export function buildSubmitRequest(ctx) {
  const body = normalize(ctx.requestBody, ctx.model);
  const upstream = ctx.upstreamModel || MODELS[ctx.model];
  if (upstream !== MODELS[ctx.model]) throw new Error("Channel mapping must select seedance2.5-ae-720p");
  body.model = upstream;
  const files = ctx.files || [];
  if (!files.length) return { url: endpoint(ctx.baseUrl), method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
  const parts = [];
  for (const key of Object.keys(body)) {
    if (Array.isArray(body[key])) {
      for (const value of body[key]) parts.push({ name: key, value });
    } else parts.push({ name: key, value: String(body[key]) });
  }
  const counts = { images: (body.images || []).length, videos: (body.videos || []).length, audios: (body.audios || []).length };
  for (const file of files) {
    const field = String(file.field || "").replace(/\[\]$/, "");
    const kind = Object.keys(MEDIA_FIELDS).find(function (name) { return MEDIA_FIELDS[name].includes(field); });
    if (!kind || !file.ref) throw new Error("Unsupported uploaded media field: " + field);
    counts[kind]++;
    if (counts[kind] > (kind === "images" ? 30 : 10)) throw new Error("Too many " + kind);
    parts.push({ name: kind, fileRef: file.ref, filename: file.filename });
  }
  return { url: endpoint(ctx.baseUrl), method: "POST", headers: auth(ctx), bodyType: "multipart", parts };
}

export function extractUsage(ctx) {
  // Preserve the shared model's existing per-second pricing, without a second multiplier.
  return { seconds: normalize(ctx.requestBody, ctx.model).seconds };
}

export function parseSubmitResponse(ctx, resp) {
  if (resp.statusCode < 200 || resp.statusCode >= 300) throw new Error("AICost rejected task submission (HTTP " + resp.statusCode + ")");
  const body = object(resp.body, "Upstream response"), request = normalize(ctx.requestBody, ctx.model);
  if (body.id !== undefined && body.task_id !== undefined && body.id !== body.task_id) throw new Error("Conflicting upstream task IDs");
  const id = taskId(body.id || body.task_id);
  const parsed = { taskId: id, taskData: { ...body, seconds: String(request.seconds), resolution: request.resolution, aspect_ratio: request.aspect_ratio } };
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
  const statuses = { not_start: "NOT_START", queued: "QUEUED", pending: "QUEUED", submitted: "SUBMITTED", running: "IN_PROGRESS", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", completed: "SUCCESS", succeeded: "SUCCESS", success: "SUCCESS", failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE" };
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
  // Keep credentials on the channel origin; the host handles redirects and Range safely.
  return { url: endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.upstreamTaskId)) + "/content", method, headers: auth(ctx) };
}

export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.stream) throw new Error("Use asynchronous video creation");
    let input;
    if (ctx.body.kind === "json") input = ctx.body.value;
    else if (ctx.body.kind === "multipart" || ctx.body.kind === "form") {
      input = {};
      for (const field of Object.keys(ctx.body.fields || {})) {
        const key = field.replace(/\[\]$/, ""), values = ctx.body.fields[field];
        const isMedia = Object.values(MEDIA_FIELDS).some(function (names) { return names.includes(key); });
        if (!isMedia && values.length !== 1) throw new Error("Expected one value for " + key);
        if (Object.prototype.hasOwnProperty.call(input, key)) throw new Error("Duplicate form field: " + key);
        input[key] = isMedia ? values : values[0];
      }
    } else throw new Error("Use JSON or multipart video creation");
    const body = normalize(input, ctx.model);
    return { kind: "submit", model: ctx.model, requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined || body.duration !== undefined) {
      try { result.seconds = String(duration(body.seconds === undefined ? body.duration : body.seconds)); } catch (_) { /* Do not expose invalid upstream duration. */ }
    }
    if (body.resolution === "720p") result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // The host supplies public IDs/status/model/timestamps and the authenticated content route.
    return result;
  },
} };
