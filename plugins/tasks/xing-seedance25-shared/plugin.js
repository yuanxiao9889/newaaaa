// New API Task Plugin API v1. Upstream model IDs come only from channel mapping.
export const meta = {
  apiVersion: 1, key: "xing-seedance25-shared", name: "XingAPI Seedance 2.5 Shared",
  version: "1.0.1", author: { name: "OOpii" },
  description: { en: "Seedance 2.5 mixed-reference video generation", zh: "Seedance 2.5 混合素材视频生成" },
  website: "https://xingapi.top", baseUrl: "https://xingapi.top",
  auth: "api_key", models: ["全能视频2.5-30-720"],
  fetchMode: "per_task", protocols: ["openai_video"],
  usageSchema: { seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } } },
  usageExamples: [{ label: "5s", facts: { seconds: 5 } }],
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9", "auto"];
const RESOLUTIONS = ["720p"];
const MEDIA = [
  { kind: "image", output: "images", limit: 30, keys: ["image", "images", "image_url", "image_urls", "reference_image_urls", "images_base64"] },
  { kind: "video", output: "videos", limit: 10, keys: ["video", "videos", "video_url", "video_urls", "reference_video_urls", "reference_videos"] },
  { kind: "audio", output: "audios", limit: 10, keys: ["audio", "audios", "audio_url", "audio_urls", "reference_audio_urls", "audios_base64", "reference_audios"] },
];
function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}
function alias(a, b, label) {
  if (a !== undefined && b !== undefined && JSON.stringify(a) !== JSON.stringify(b)) throw new Error("Conflicting " + label);
  return a === undefined ? b : a;
}
function seconds(value) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+$/.test(value)))) throw new Error("seconds must be an integer from 4 to 30");
  const count = Number(value);
  if (!Number.isInteger(count) || count < 4 || count > 30) throw new Error("seconds must be an integer from 4 to 30");
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
function media(value, kind, limit) {
  if (value === undefined) return [];
  const list = typeof value === "string" ? [value] : value;
  if (!Array.isArray(list) || list.length > limit) throw new Error(kind + " exceeds " + limit + " references");
  return list.map(function (url) {
    if (!publicUrl(url)) throw new Error(kind + " requires public HTTP(S) URLs");
    return url;
  });
}
function normalize(req, model) {
  object(req, "Request");
  if (req.metadata !== undefined) {
    const metadata = object(req.metadata, "metadata");
    const keys = ["ratio", "images", "reference_videos", "reference_audios"];
    for (const key of Object.keys(metadata)) if (!keys.includes(key)) throw new Error("Unsupported metadata field: " + key);
    req = { ...req };
    delete req.metadata;
    for (const key of keys) if (metadata[key] !== undefined) req[key] = alias(req[key], metadata[key], key + " metadata");
  }
  if (typeof model !== "string" || !model.trim() || (req.model !== undefined && req.model !== model)) throw new Error("Mismatched request model");
  const fields = ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "resolution", "n", "stream"].concat(...MEDIA.map(function (spec) { return spec.keys; }));
  for (const key of Object.keys(req)) if (!fields.includes(key)) throw new Error("Unsupported request field: " + key);
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  if (Array.from(req.prompt).length > 45000) throw new Error("prompt must not exceed 45000 characters");
  const count = seconds(req.seconds === undefined ? (req.duration === undefined ? 5 : req.duration) : req.seconds);
  if (req.duration !== undefined && seconds(req.duration) !== count) throw new Error("Conflicting seconds/duration");
  const ratio = alias(req.aspect_ratio, req.ratio, "aspect_ratio/ratio") || "16:9";
  if (!RATIOS.includes(ratio)) throw new Error("Unsupported aspect_ratio");
  const resolution = req.resolution === undefined ? "720p" : req.resolution;
  if (!RESOLUTIONS.includes(resolution)) throw new Error("Unsupported resolution");
  if (req.n !== undefined && req.n !== 1) throw new Error("Only one video per task");
  if (req.stream !== undefined && req.stream !== false) throw new Error("Use asynchronous JSON submission");
  const body = { model, prompt: req.prompt, seconds: count, aspect_ratio: ratio, resolution };
  for (const spec of MEDIA) {
    let refs;
    for (const key of spec.keys) {
      if (req[key] !== undefined) refs = alias(refs, media(req[key], spec.kind, spec.limit), spec.output + " aliases");
    }
    if (refs && refs.length) body[spec.output] = refs;
  }
  return body;
}
function endpoint(baseUrl) {
  const base = String(baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(base)) throw new Error("Invalid channel Base URL");
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
  let reason = [body.fail_reason, error.message, typeof body.error === "string" ? body.error : null, body.error_message, body.message].find(function (v) { return typeof v === "string" && v.trim(); }) || "XingAPI video generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Publish reference media as URLs before submitting");
  const body = normalize(ctx.requestBody, ctx.model);
  const upstream = ctx.upstreamModel || ctx.model;
  if (typeof upstream !== "string" || !upstream.trim() || upstream !== upstream.trim() || upstream === ctx.model || /[\u0000-\u001f\u007f]/.test(upstream)) throw new Error("Invalid mapped upstream model");
  // Preserve the shared model's 720p contract. The mapped ID can name any
  // compatible XingAPI variant; even seedance2.5-480P-M has a 720p price tier.
  const payload = { model: upstream, prompt: body.prompt, duration: body.seconds, ratio: body.aspect_ratio, resolution: body.resolution };
  for (const spec of MEDIA) if (body[spec.output]) payload[spec.output] = body[spec.output].slice();
  return { url: endpoint(ctx.baseUrl) + "/generations", method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body: payload };
}
export function extractUsage(ctx) {
  return { seconds: normalize(ctx.requestBody, ctx.model).seconds };
}
export function parseSubmitResponse(ctx, resp) {
  const status = resp.statusCode === undefined ? resp.status : resp.statusCode;
  if (!(status >= 200 && status < 300)) throw new Error("XingAPI submission HTTP " + status + ": " + safeReason(resp.body || {}, ctx));
  const envelope = object(resp.body, "Upstream response");
  const body = envelope.data && typeof envelope.data === "object" && !Array.isArray(envelope.data) ? envelope.data : envelope;
  const req = normalize(ctx.requestBody, ctx.model);
  const id = taskId(alias(alias(body.task_id, body.id, "upstream task ID"), body.request_id, "upstream task ID"));
  const parsed = { taskId: id, taskData: { ...body, seconds: req.seconds, resolution: req.resolution, aspect_ratio: req.aspect_ratio } };
  const result = parseTaskResult({ ...ctx, taskId: id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}
export function buildQueryRequest(ctx) {
  return { url: endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.taskId)), method: "GET", headers: auth(ctx) };
}
export function parseTaskResult(ctx, body, response) {
  if (response && !(response.status >= 200 && response.status < 300)) return { status: "UNKNOWN" };
  if (body && body.data && typeof body.data === "object" && !Array.isArray(body.data)) body = body.data;
  if (!body || typeof body !== "object" || Array.isArray(body) || typeof body.status !== "string") return { status: "UNKNOWN" };
  const id = body.task_id || body.id || body.request_id;
  if (id !== ctx.taskId || (body.task_id !== undefined && body.task_id !== id) || (body.id !== undefined && body.id !== id) || (body.request_id !== undefined && body.request_id !== id)) return { status: "UNKNOWN" };
  const statuses = { not_start: "QUEUED", preparing: "QUEUED", queued: "QUEUED", pending: "QUEUED", submitted: "SUBMITTED", processing: "IN_PROGRESS", in_progress: "IN_PROGRESS", running: "IN_PROGRESS", done: "SUCCESS", finished: "SUCCESS", completed: "SUCCESS", succeeded: "SUCCESS", success: "SUCCESS", error: "FAILURE", expired: "FAILURE", failed: "FAILURE", failure: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE" };
  const statusKey = body.status.trim().toLowerCase();
  const status = Object.prototype.hasOwnProperty.call(statuses, statusKey) ? statuses[statusKey] : null;
  if (!status) return { status: "UNKNOWN" };
  // The documented authenticated /content endpoint works without a public result URL.
  if (status === "SUCCESS") return { status, progress: "100%" };
  if (status === "FAILURE") return { status, reason: safeReason(body, ctx) };
  const progress = Number(String(body.progress === undefined ? body.progress_text || "0" : body.progress).replace(/%$/, ""));
  return { status, progress: Number.isFinite(progress) ? Math.max(0, Math.min(99, progress)) + "%" : "0%" };
}
export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const method = (ctx.clientRequest || {}).method;
  if (!["GET", "HEAD"].includes(method)) throw new Error("Unsupported content method");
  const queryUrl = endpoint(ctx.baseUrl) + "/" + encodeURIComponent(taskId(ctx.upstreamTaskId));
  const canonical = queryUrl + "/content";
  const data = ctx.data || {};
  let body = data.data && typeof data.data === "object" ? data.data : data;
  // Terminal task snapshots can predate the storage URL, and signed URLs expire.
  // Refresh only this existing task; never create another generation for a download.
  try {
    const response = utils.fetch({ url: queryUrl, method: "GET", headers: auth(ctx) });
    if (response.status === 200 && parseTaskResult({ ...ctx, taskId: ctx.upstreamTaskId }, response.body).status === "SUCCESS") {
      body = response.body.data && typeof response.body.data === "object" ? response.body.data : response.body;
    }
  } catch (_) { /* A transient query failure can still use the saved result. */ }
  const origin = /^https?:\/\/[^/]+/i.exec(canonical)[0].toLowerCase();
  const candidates = [body.download_url, body.video_url, body.result_url, body.content_url, body.url, (body.content || {}).video_url, (body.metadata || {}).url];
  // An authenticated /content request can redirect outside the credential host.
  // Inspect that response without following it, then let the host fetch the
  // storage URL credentiallessly under its existing SSRF and redirect checks.
  for (let attempt = 0; attempt < 2; attempt++) {
    for (const result of candidates) {
      if (publicUrl(result) && result.startsWith("https://") && !result.includes("#")
          && /^https?:\/\/[^/]+/i.exec(result)[0].toLowerCase() !== origin
          && (!ctx.apiKey || (!result.includes(ctx.apiKey) && !result.includes(encodeURIComponent(ctx.apiKey))))) {
        return { url: result, method: "GET", credentialless: true };
      }
    }
    if (attempt === 0) {
      try {
        const response = utils.fetch({ url: canonical, method: "GET", headers: { ...auth(ctx), Range: "bytes=0-0" } });
        if ([301, 302, 303, 307, 308].includes(response.status)) candidates.push((response.headers || {}).Location);
      } catch (_) { /* Keep direct-file upstreams on the normal streaming path. */ }
    }
  }
  // XingAPI and some storage endpoints reject HEAD. The host suppresses the
  // GET body for a client HEAD while retaining its Range and ownership checks.
  return { url: canonical, method: "GET", headers: auth(ctx) };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    const body = normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, action: body.images || body.videos ? "image_to_video" : "text_to_video", requestBody: body };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = {};
    if (body.seconds !== undefined) { try { result.seconds = String(seconds(body.seconds)); } catch (_) { /* Ignore malformed upstream metadata. */ } }
    if (RESOLUTIONS.includes(body.resolution)) result.resolution = body.resolution;
    if (RATIOS.includes(body.aspect_ratio)) result.aspect_ratio = body.aspect_ratio;
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    // The host exposes its authenticated content URL; do not leak upstream task URLs.
    return result;
  },
} };
