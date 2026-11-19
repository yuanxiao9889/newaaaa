// New API Task Plugin API v1. No embedded credentials or prices.
// Native upstream contract: https://doc.newtoken.club/api-docs/seedance-official.html
// Public OpenAI video requests retain their existing aliases and billing meters.
const MODELS = {
  "全能视频-2.0-933-720": "doubao-seedance-2-0-260128",
  "全能视频2.5-301010-720": "doubao-seedance-2-5-260628",
  "全能视频2.5-无视频参考-720": "doubao-seedance-2-5-260628",
  "全能视频2.0-无视频参考-720": "doubao-seedance-2-0-260128",
  "全能视频2.0-参考视频-720-token": "doubao-seedance-2-0-260128",
  "全能视频2.5-参考视频-720-token": "doubao-seedance-2-5-260628",
};
const SECOND_MODELS = ["全能视频-2.0-933-720", "全能视频2.5-301010-720", "全能视频2.0-无视频参考-720", "全能视频2.5-无视频参考-720"];
const TOKEN_MODELS = ["全能视频2.0-参考视频-720-token", "全能视频2.5-参考视频-720-token"];
const SECOND_SCHEMA = {
  seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
};
const TOKEN_SCHEMA = {
  tokens: { type: "number", unit: "token", description: { en: "Video generation token unit price", zh: "视频生成 Token 单价" } },
};
const RATIOS = ["16:9", "9:16", "1:1", "4:3", "3:4", "21:9"];
export const meta = {
  apiVersion: 1, key: "newtoken-seedance", name: "NewToken Seedance", version: "1.2.0",
  author: { name: "OOpii" },
  description: { en: "Seedance video generation through NewToken", zh: "通过 NewToken 生成 Seedance 视频" },
  website: "https://doc.newtoken.club/api-docs/seedance-official.html", baseUrl: "https://www.newtoken.club",
  auth: "api_key", models: Object.keys(MODELS), protocols: ["openai_video"], fetchMode: "per_task",
  // Shared model IDs accept no video reference on this provider; token IDs remain explicit.
  usageProfiles: [
    { models: SECOND_MODELS, schema: SECOND_SCHEMA, examples: [{ label: "720p · 5s", facts: { seconds: 5 } }] },
    { models: TOKEN_MODELS, schema: TOKEN_SCHEMA, examples: [{ label: "100,000 tokens", facts: { tokens: 100000 } }] },
  ],
  usageSchema: {
    seconds: { type: "number", unit: "second", description: { en: "Video generation unit price", zh: "视频生成单价" } },
    video_input: { enum: ["none", "video"], description: { en: "Reference video input", zh: "参考视频输入" },
      enumLabels: { none: { en: "No reference video", zh: "无参考视频" }, video: { en: "With reference video", zh: "有参考视频" } } },
  },
  usageExamples: [
    { label: "720p · 5s", facts: { seconds: 5, video_input: "none" } },
    { label: "720p · 5s · video reference", facts: { seconds: 5, video_input: "video" } },
  ],
};

function object(value, label) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error(label + " must be an object");
  return value;
}
function allowed(value, keys, label) {
  for (const key of Object.keys(value)) if (!keys.includes(key)) throw new Error("Unsupported " + label + ": " + key);
}
function seconds(value, maximum) {
  if (!(typeof value === "number" || (typeof value === "string" && /^\d+(?:\.0+)?s?$/.test(value)))) throw new Error("Duration must be an integer");
  const n = Number(String(value).replace(/s$/, ""));
  if (!Number.isInteger(n) || n < 4 || n > maximum) throw new Error("Duration must be between 4 and " + maximum + " seconds");
  return n;
}
function alias(a, b, label) {
  if (a !== undefined && b !== undefined && a !== b) throw new Error("Conflicting " + label);
  return a === undefined ? b : a;
}
function publicUrl(value) {
  if (typeof value !== "string" || /[\s\\\u0000-\u001f\u007f]/.test(value)) return false;
  const match = /^https:\/\/([a-z0-9.-]+)(?::([1-9][0-9]{0,4}))?(?:[/?#]|$)/i.exec(value);
  if (!match || (match[2] && Number(match[2]) > 65535)) return false;
  const host = match[1].toLowerCase(), labels = host.split(".");
  return labels.length > 1 && !/(?:^|\.)(localhost|local|internal)$/.test(host)
    && labels.every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))
    && /[a-z]/.test(labels[labels.length - 1]) && !/^(?:[0-9.]+|0x[0-9a-f.]+)$/i.test(host);
}
function normalize(req, model) {
  object(req, "Request");
  if (!Object.prototype.hasOwnProperty.call(MODELS, model) || req.model !== model) throw new Error("Unsupported or mismatched channel model");
  allowed(req, ["model", "prompt", "seconds", "duration", "ratio", "aspect_ratio", "resolution", "content", "input_reference",
    "images", "videos", "audios", "omni_reference_task_type", "generate_audio", "seed", "n", "stream"], "NewToken request field");
  if (req.generate_audio !== undefined && typeof req.generate_audio !== "boolean") throw new Error("generate_audio must be boolean");
  if (req.seed !== undefined && !Number.isSafeInteger(req.seed)) throw new Error("seed must be a safe integer");
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  const v25 = MODELS[model] === "doubao-seedance-2-5-260628", maximum = v25 ? 30 : 15;
  const duration = alias(req.seconds === undefined ? undefined : seconds(req.seconds, maximum), req.duration === undefined ? undefined : seconds(req.duration, maximum), "seconds/duration");
  const ratio = alias(req.ratio, req.aspect_ratio, "ratio/aspect_ratio");
  if (ratio !== undefined && !RATIOS.includes(ratio)) throw new Error("NewToken requires a fixed aspect ratio");
  if (req.resolution !== undefined && req.resolution !== "720p") throw new Error("Resolution must be 720p");
  if (req.omni_reference_task_type !== undefined && req.omni_reference_task_type !== "reference") throw new Error("NewToken supports ordinary reference generation only");
  if (req.n !== undefined && req.n !== 1) throw new Error("Only one video per task is supported");
  if (req.stream !== undefined && req.stream !== false) throw new Error("Use asynchronous JSON submission");
  const arrays = ["images", "videos", "audios"].some(key => req[key] !== undefined);
  if ([req.content !== undefined, req.input_reference !== undefined, arrays].filter(Boolean).length > 1) throw new Error("Do not mix content, input_reference and media arrays");
  let entries = req.content === undefined ? [] : req.content;
  if (arrays) {
    entries = [];
    for (const kind of ["image", "video", "audio"]) {
      const values = req[kind + "s"] === undefined ? [] : req[kind + "s"];
      if (!Array.isArray(values) || values.length > 30) throw new Error("Invalid " + kind + " reference array");
      for (const url of values) entries.push({ type: kind + "_url", role: "reference_" + kind, [kind + "_url"]: { url } });
    }
  }
  if (req.input_reference !== undefined) entries = [{ type: "image_url", role: "reference_image", image_url: { url: req.input_reference } }];
  if (!Array.isArray(entries) || entries.length > (v25 ? 51 : 16)) throw new Error("Too many content entries");
  const images = [], videos = [], audios = [];
  let first, last, textCount = 0;
  for (const entry of entries) {
    object(entry, "Content entry");
    if (entry.type === "text") {
      allowed(entry, ["type", "text"], "text field");
      if (++textCount > 1 || entry.text !== req.prompt) throw new Error("Content text must match prompt");
      continue;
    }
    const kind = { image_url: "image", video_url: "video", audio_url: "audio" }[entry.type];
    if (!["image", "video", "audio"].includes(kind)) throw new Error("Unsupported content type");
    allowed(entry, ["type", "role", entry.type], "content field");
    const source = object(entry[entry.type], "Media");
    allowed(source, ["url"], "media field");
    // Provider-specific asset IDs and inline media cannot safely fail over.
    if (!publicUrl(source.url)) throw new Error("NewToken fallback requires public HTTPS media URLs");
    if (kind === "image" && entry.role === "first_frame") {
      if (first !== undefined) throw new Error("Only one first frame is supported");
      first = source.url;
    } else if (kind === "image" && entry.role === "last_frame") {
      if (last !== undefined) throw new Error("Only one last frame is supported");
      last = source.url;
    } else {
      if (entry.role !== "reference_" + kind) throw new Error("Unsupported reference role");
      ({ image: images, video: videos, audio: audios })[kind].push(source.url);
    }
  }
  const imageCount = images.length + (first === undefined ? 0 : 1) + (last === undefined ? 0 : 1);
  // Reference permissions follow the client model, not the shared upstream
  // mapping. Normalize runs before selection and again before submit.
  const tokenBilled = TOKEN_MODELS.includes(model);
  if (tokenBilled && videos.length === 0) throw new Error("按 Token 计费模型必须提供参考视频 / Token-priced models require a reference video");
  if (!tokenBilled && videos.length) throw new Error("按秒计费模型不接受参考视频 / Per-second models do not accept reference videos");
  // Image/audio caps preserve the current public model contract; the provider
  // document specifies only the 3/10 video caps and the two-frame restriction.
  if (imageCount > (v25 ? 30 : 9) || videos.length > (v25 ? 10 : 3) || audios.length > (v25 ? 10 : 3)) throw new Error("Reference count exceeds this model's limits");
  if ((first !== undefined || last !== undefined) && (imageCount > 2 || videos.length)) throw new Error("Frame references allow at most two images and no reference video");
  const content = [{ type: "text", text: req.prompt.replace(/@(image|video|audio)(\d+)\b/gi, (_all, kind, index) => "@" + kind[0].toUpperCase() + kind.slice(1).toLowerCase() + index) }];
  for (const [kind, urls] of [["image", images], ["video", videos], ["audio", audios]]) {
    for (const url of urls) content.push({ type: kind + "_url", role: "reference_" + kind, [kind + "_url"]: { url } });
  }
  if (first !== undefined) content.push({ type: "image_url", role: "first_frame", image_url: { url: first } });
  if (last !== undefined) content.push({ type: "image_url", role: "last_frame", image_url: { url: last } });
  const body = { model: MODELS[model], content, duration: duration === undefined ? 4 : duration,
    ratio: ratio === undefined ? "16:9" : ratio, resolution: "720p" };
  if (v25 && videos.length) body.omni_reference_task_type = "reference";
  if (req.generate_audio !== undefined) body.generate_audio = req.generate_audio;
  if (req.seed !== undefined) body.seed = req.seed;
  // Reserve 1.5 times the 720p output-token estimate per requested second.
  // Reference duration is unknown here; this may still require a supplement.
  // Final usage.total_tokens replaces the estimate without the 1.5 multiplier.
  const usage = tokenBilled
    ? { tokens: Math.ceil(body.duration * 1280 * 720 * 24 / 1024 * 1.5) }
    : { seconds: body.duration, video_input: videos.length ? "video" : "none" };
  return { body, usage };
}
function endpoint(baseUrl, legacy = false) {
  const base = String(baseUrl || "").replace(/\/+$/, "");
  if (!/^https?:\/\/[^/?#@\s]+(?:\/v1)?$/.test(base)) throw new Error("Channel Base URL must be an origin, optionally ending in /v1");
  const origin = base.replace(/\/v1$/, "");
  return origin + (legacy ? "/v1/videos" : "/api/v3/contents/generations/tasks");
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
  let reason = [body.fail_reason, (body.error || {}).message, typeof body.error === "string" ? body.error : null, body.message]
    .find(value => typeof value === "string" && value.trim()) || "NewToken generation failed";
  if (ctx.apiKey) reason = reason.split(ctx.apiKey).join("[redacted]");
  return reason.replace(/https?:\/\/\S+/gi, "[upstream URL]").replace(/[\u0000-\u001f\u007f]/g, " ").slice(0, 500);
}
function videoUrl(body) {
  if (!body || typeof body !== "object") return null;
  const result = body.result || {}, output = Array.isArray(body.output) ? body.output : [];
  return [(body.content || {}).video_url, body.video_url, body.url, result.video_url, result.url, ...output.filter(item => item && item.type === "video").map(item => item.url)]
    .find(url => publicUrl(url) && !url.includes("#")) || null;
}
export function buildSubmitRequest(ctx) {
  if ((ctx.files || []).length) throw new Error("Use JSON public HTTPS media URLs");
  const { body } = normalize(ctx.requestBody, ctx.model);
  // Channel mappings choose the upstream ID; the public model keeps its
  // reference limits and billing meter. No mapping retains the legacy default.
  if (ctx.upstreamModel && ctx.upstreamModel !== ctx.model) body.model = ctx.upstreamModel;
  return { url: endpoint(ctx.baseUrl), method: "POST", headers: { ...auth(ctx), "Content-Type": "application/json" }, body };
}
export function extractUsage(ctx) {
  // Each public model has one meter; the host applies its configured price.
  return normalize(ctx.requestBody, ctx.model).usage;
}
export function extractUsageOnComplete(ctx, result, body) {
  if (!result || result.status !== "SUCCESS") return {};
  if (!TOKEN_MODELS.includes(ctx.model)) {
    const duration = body && body.duration;
    // Omitted completion duration retains the frozen requested duration.
    return duration === undefined ? {} : { seconds: seconds(duration, MODELS[ctx.model] === "doubao-seedance-2-5-260628" ? 30 : 15) };
  }
  const tokens = body && body.usage && body.usage.total_tokens;
  // NewToken bills total_tokens, which can differ from completion_tokens.
  // Never replace a reservation with a guessed or malformed actual usage.
  if (typeof tokens !== "number" || !Number.isInteger(tokens) || tokens < 0 || tokens > 2147483647) {
    console.warn("NewToken token settlement: missing or invalid usage.total_tokens; retaining reserved usage");
    return {};
  }
  return { tokens };
}
export function parseSubmitResponse(ctx, response) {
  if (response.statusCode < 200 || response.statusCode >= 300) throw new Error("NewToken rejected submission (HTTP " + response.statusCode + ")");
  const body = object(response.body, "Upstream response"), normalized = normalize(ctx.requestBody, ctx.model);
  // id is NewToken's public ID; task_id can be a DIFFERENT provider ID.
  const id = taskId(body.id === undefined ? body.task_id : body.id);
  const parsed = { taskId: id, taskData: body, state: { ...normalized.usage, protocol: "official" } };
  const result = parseTaskResult({ ...ctx, taskId: id }, body);
  if (result.status === "SUCCESS" || result.status === "FAILURE") parsed.immediate = result;
  return parsed;
}
export function buildQueryRequest(ctx) {
  return { url: endpoint(ctx.baseUrl, !ctx.state || ctx.state.protocol !== "official") + "/" + encodeURIComponent(taskId(ctx.taskId)), method: "GET", headers: auth(ctx) };
}
export function parseTaskResult(ctx, body, response) {
  if (response && (response.status < 200 || response.status >= 300)) return { status: "UNKNOWN" };
  if (!body || typeof body !== "object" || Array.isArray(body) || (body.id === undefined ? body.task_id : body.id) !== ctx.taskId) return { status: "UNKNOWN" };
  const states = { queued: "QUEUED", pending: "QUEUED", in_progress: "IN_PROGRESS", processing: "IN_PROGRESS", succeeded: "SUCCESS", completed: "SUCCESS", failed: "FAILURE", cancelled: "FAILURE", canceled: "FAILURE" };
  const key = String(body.status || "").trim().toLowerCase();
  if (!Object.prototype.hasOwnProperty.call(states, key)) return { status: "UNKNOWN" };
  const status = states[key];
  if (status === "FAILURE") return { status, reason: safeReason(body, ctx) };
  if (status === "SUCCESS") {
    // Keep polling if a completed snapshot has no downloadable result yet.
    if (!videoUrl(body)) return { status: "IN_PROGRESS", progress: "99%" };
    const result = { status, progress: "100%" }, usage = body.usage;
    // A token-priced task must not finalize at its estimate when usage is missing.
    if (TOKEN_MODELS.includes(ctx.model) && (!usage || !Number.isInteger(usage.total_tokens) || usage.total_tokens < 0 || usage.total_tokens > 2147483647)) return { status: "UNKNOWN" };
    if (usage && typeof usage === "object") {
      const tokens = {};
      for (const field of ["prompt_tokens", "completion_tokens", "total_tokens"]) {
        if (usage[field] !== undefined) {
          if (!Number.isInteger(usage[field]) || usage[field] < 0 || usage[field] > 2147483647) return result;
          tokens[field] = usage[field];
        }
      }
      if ((tokens.prompt_tokens || 0) + (tokens.completion_tokens || 0) <= 2147483647 && Object.keys(tokens).length) result.tokenUsage = tokens;
    }
    return result;
  }
  const progress = Number(String(body.progress === undefined ? 0 : body.progress).replace(/%$/, ""));
  return { status, progress: Number.isFinite(progress) ? Math.max(0, Math.min(99, progress)) + "%" : "0%" };
}
export function listArtifacts(task) {
  return task.status === "SUCCESS" ? [{ key: "video", type: "video", mimeType: "video/mp4" }] : [];
}
export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  if (!["GET", "HEAD"].includes((ctx.clientRequest || {}).method)) throw new Error("Unsupported content method");
  const url = videoUrl(ctx.data);
  if (!url || (ctx.apiKey && (url.includes(ctx.apiKey) || url.includes(encodeURIComponent(ctx.apiKey))))) throw new Error("No safe video download URL is available");
  // The host validates public media destinations and follows redirects without credentials.
  return { url, method: "GET", credentialless: true };
}
export const protocols = { openai_video: {
  decodeRequest: function (ctx) {
    if (ctx.operation !== "create" || !ctx.body || ctx.body.kind !== "json" || ctx.stream) throw new Error("Use JSON asynchronous video creation");
    normalize(ctx.body.value, ctx.model);
    return { kind: "submit", model: ctx.model, requestBody: ctx.body.value };
  },
  render: function (_ctx, task) {
    const body = task.data || {}, result = { resolution: "720p" };
    const duration = body.seconds === undefined ? body.duration : body.seconds;
    if (duration !== undefined) { try { result.seconds = String(seconds(duration, 30)); } catch (_) { /* Omit invalid upstream duration. */ } }
    if (RATIOS.includes(body.ratio || body.aspect_ratio)) result.aspect_ratio = body.ratio || body.aspect_ratio;
    if (task.status === "SUCCESS" && body.usage && Number.isInteger(body.usage.total_tokens) && body.usage.total_tokens >= 0 && body.usage.total_tokens <= 2147483647) result.usage = { total_tokens: body.usage.total_tokens };
    if (task.status === "FAILURE") result.error = { code: "generation_failed", message: safeReason({ fail_reason: task.fail_reason }, {}) };
    return result;
  },
} };
