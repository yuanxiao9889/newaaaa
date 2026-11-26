// New API task plugin for explicitly bound TokenMei-compatible video channels.
const AGGREGATE_MODEL = "全能视频2.5-301010-720-聚合";
const MODELS = ["全能视频2.5-301010-sss", AGGREGATE_MODEL];
const RESOLUTIONS = ["480p", "720p", "1080p"];
const RATIOS = ["16:9", "9:16", "4:3", "3:4", "1:1", "21:9", "adaptive"];
const MEDIA = [
  ["reference_images", "images", 30], ["reference_videos", "videos", 10],
  ["reference_audio", "audios", 10], ["first_frame", "start_frame", 1], ["last_frame", "end_frame", 1],
];
function usageSchema(resolutions) {
  return {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    resolution: { enum: resolutions, description: { en: "Output resolution", zh: "输出分辨率" } },
  };
}
export const meta = {
  apiVersion: 1, key: "tokenmei-video", name: "TokenMei Video", version: "1.1.0",
  author: { name: "OOpii" }, description: { zh: "通过 TokenMei 生成异步视频", en: "Asynchronous video generation via TokenMei" },
  website: "https://tokenmei.com/zh/docs/videos", baseUrl: "https://tokenmei.com",
  auth: "api_key", models: MODELS, protocols: ["openai_video"], fetchMode: "per_task",
  usageSchema: usageSchema(RESOLUTIONS),
  usageExamples: RESOLUTIONS.map(resolution => ({ label: resolution + " / 5s", facts: { seconds: 5, resolution } })),
  usageProfiles: [{
    models: [AGGREGATE_MODEL], schema: usageSchema(["720p"]),
    examples: [{ label: "720p / 5s", facts: { seconds: 5, resolution: "720p" } }],
  }],
};

function publicUrl(value) {
  if (typeof value !== "string" || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  const match = /^https?:\/\/([a-z\d.-]+)(?::([1-9]\d{0,4}))?(?:[/?]|$)/i.exec(value);
  if (!match || Number(match[2]) > 65535) return false;
  const host = match[1].toLowerCase(), labels = host.split(".");
  return labels.length > 1 && /^[a-z][a-z0-9-]*$/.test(labels[labels.length - 1])
    && !/(?:^|\.)(?:localhost|local|internal)$/.test(host)
    && labels.every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label));
}
function normalize(req, model) {
  if (!req || typeof req !== "object" || Array.isArray(req) || !MODELS.includes(model) || (req.model !== undefined && req.model !== model)) throw new Error("Unsupported video request or model");
  const fields = ["model", "prompt", "duration", "seconds", "ratio", "aspect_ratio", "resolution", "generate_audio", "watermark", "seed", "camera_fixed", "n", "stream"];
  for (const [key, alias] of MEDIA) fields.push(key, alias);
  for (const key of Object.keys(req)) if (!fields.includes(key)) throw new Error("Unsupported video request field: " + key);
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("A prompt is required");
  if (req.n !== undefined && req.n !== 1 || req.stream !== undefined && req.stream !== false) throw new Error("Only single asynchronous videos are supported");
  const value = req.duration !== undefined ? req.duration : req.seconds !== undefined ? req.seconds : 5;
  const duration = Number(value);
  if (!/^[0-9]+$/.test(String(value)) || !["number","string"].includes(typeof value) || !Number.isSafeInteger(duration) || duration < 5 || duration > 30) throw new Error("Duration must be 5 to 30 seconds");
  const resolution = req.resolution === undefined ? "720p" : req.resolution;
  if (model === AGGREGATE_MODEL && resolution !== "720p") throw new Error("Resolution must be 720p");
  const ratio = req.ratio !== undefined ? req.ratio : req.aspect_ratio !== undefined ? req.aspect_ratio : "16:9";
  if (!RESOLUTIONS.includes(resolution) || !RATIOS.includes(ratio)) throw new Error("Unsupported resolution or aspect ratio");
  const body = { model, prompt: req.prompt, duration, ratio, resolution };
  for (const key of ["generate_audio", "watermark", "camera_fixed"]) {
    if (req[key] === undefined) continue;
    if (typeof req[key] !== "boolean") throw new Error(key + " requires a boolean");
    body[key] = req[key];
  }
  if (req.seed !== undefined) {
    if (!Number.isSafeInteger(req.seed)) throw new Error("Invalid seed");
    body.seed = req.seed;
  }
  for (const [key, alias, limit] of MEDIA) {
    if (req[key] !== undefined && req[alias] !== undefined) throw new Error("Duplicate media field: " + key);
    const input = req[key] !== undefined ? req[key] : req[alias];
    if (input === undefined) continue;
    const urls = Array.isArray(input) ? input : [input];
    if (urls.length > limit || !urls.every(publicUrl)) throw new Error("Invalid public media URLs for " + key);
    if (urls.length) body[key] = limit === 1 ? urls[0] : urls;
  }
  if (body.last_frame && !body.first_frame || body.first_frame && (body.reference_images || body.reference_videos || body.reference_audio)) {
    throw new Error("Use first/last frames or reference media separately");
  }
  return body;
}
function base(ctx) {
  const root = String(ctx.baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(root)) throw new Error("Expected a channel origin or /v1 base URL");
  return root.endsWith("/v1") ? root : root + "/v1";
}
function auth(ctx) {
  if (typeof ctx.apiKey !== "string" || !ctx.apiKey.trim()) throw new Error("Missing channel key");
  return { Authorization: "Bearer " + ctx.apiKey };
}
function taskId(value) {
  if (typeof value !== "string" || !/^video_[a-z0-9_-]{1,240}$/i.test(value)) throw new Error("Invalid upstream video ID");
  return value;
}
function reason(error, ctx) {
  let message = typeof error === "string" ? error : error && error.message;
  if (typeof message !== "string" || !message.trim()) message = "Video generation failed";
  if (ctx.apiKey) message = message.split(ctx.apiKey).join("[redacted]");
  return message.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model);
  if (typeof ctx.upstreamModel !== "string" || !ctx.upstreamModel.trim()) throw new Error("Missing mapped model ID");
  body.model = ctx.upstreamModel;
  return { url: base(ctx) + "/videos", method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
}
export function extractUsage(ctx) {
  const body = normalize(ctx.requestBody, ctx.model);
  return { seconds: body.duration, resolution: body.resolution };
}
export function parseSubmitResponse(ctx, response) {
  if (response.statusCode < 200 || response.statusCode >= 300) throw new Error("TokenMei HTTP " + response.statusCode + ": " + reason((response.body || {}).error, ctx));
  const body = response.body || {}, id = taskId(body.id);
  const request = normalize(ctx.requestBody, ctx.model);
  const parsed = { taskId: id, taskData: { seconds: String(request.duration), resolution: request.resolution, aspect_ratio: request.ratio } };
  const result = parseTaskResult({ ...ctx, taskId: id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}
export function buildQueryRequest(ctx) {
  return { url: base(ctx) + "/tasks/" + taskId(ctx.taskId), method: "GET", headers: auth(ctx) };
}
export function parseTaskResult(ctx, body, response) {
  if (response && response.status !== 200 || !body || !body.id || body.id !== ctx.taskId) return { status: "UNKNOWN" };
  if (body.status === "failed") return { status: "FAILURE", reason: reason(body.error, ctx) };
  if (body.status === "completed") return publicUrl(body.url) ? { status: "SUCCESS", progress: "100%" } : { status: "UNKNOWN" };
  if (body.status !== "in_progress" && body.status !== "queued") return { status: "UNKNOWN" };
  const progress = Number.isFinite(body.progress) ? Math.max(0, Math.min(99, body.progress)) : 0;
  return { status: body.status === "queued" ? "QUEUED" : "IN_PROGRESS", progress: progress + "%" };
}
export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video" || !["GET", "HEAD"].includes((ctx.clientRequest || {}).method)) throw new Error("artifact_not_found");
  const url = base(ctx) + "/videos/" + taskId(ctx.upstreamTaskId) + "/content";
  const lookup = utils.fetch({ url, method: "GET", headers: { ...auth(ctx), Accept: "application/json" } });
  const target = lookup.body && lookup.body.url;
  if (lookup.status !== 200 || !publicUrl(target) || target.includes("#") || (url.startsWith("https://") && !target.startsWith("https://"))
      || target.includes(ctx.apiKey) || target.includes(encodeURIComponent(ctx.apiKey))) throw new Error("Invalid upstream video download URL");
  return { url: target, method: "GET", credentialless: true };
}
export const protocols = { openai_video: {
  decodeRequest(ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use asynchronous JSON video creation");
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.first_frame || body.reference_images || body.reference_videos || body.reference_audio ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render(_ctx, task) {
    const result = {}, body = task.data || {};
    if (body.seconds !== undefined && /^\d+$/.test(String(body.seconds))) result.seconds = String(body.seconds);
    if (RESOLUTIONS.includes(body.resolution)) result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: reason(task.fail_reason, {}) };
    return result;
  },
} };
