// JieAPI's synchronous Seed Audio API:
// https://jieapi.apifox.cn/525497211e0
// Native parameter rules: https://docs.volcengine.com/docs/6561/2550782
const MODEL = "seed-audio-1.0";
const MAX_SECONDS = 120;
const MAX_REFERENCE_BYTES = 10 * 1024 * 1024;
const SOURCE_FIELDS = ["speaker", "audio_data", "audio_url", "image_data", "image_url"];
const SAMPLE_RATES = {
  wav: [8000, 16000, 24000, 32000, 40000, 44100, 48000],
  pcm: [8000, 16000, 24000, 32000, 40000, 44100, 48000],
  mp3: [8000, 16000, 24000, 32000, 44100, 48000],
  ogg_opus: [48000],
};

export const meta = {
  apiVersion: 1,
  key: "jieapi-audio",
  name: "JieAPI Seed Audio",
  icon: "text:音频",
  version: "1.0.0",
  author: { name: "OOpii" },
  description: {
    en: "Synchronous Seed Audio generation through JieAPI",
    zh: "通过 JieAPI 同步生成 Seed Audio 音频",
  },
  website: "https://jieapi.apifox.cn/525497211e0",
  baseUrl: "https://jieapi.com",
  auth: "api_key",
  models: [MODEL],
  fetchMode: "per_task",
  routes: [{
    method: "POST", path: "/jieapi/v1/audio/speech",
    type: "submit", decode: "generate", render: "audioCreated", retainResult: false,
  }],
  usageSchema: {
    seconds: {
      type: "number", unit: "second",
      description: { en: "Audio generation unit price", zh: "音频生成单价" },
    },
  },
  usageExamples: [{ label: "30s", facts: { seconds: 30 } }],
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isHTTPURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value);
}

function validateRequest(req) {
  if (!isObject(req)) throw new Error("request body must be a JSON object");
  if (typeof req.model !== "string" || !req.model.trim()) throw new Error("model is required");
  if (typeof req.text_prompt !== "string" || !req.text_prompt.trim() || Array.from(req.text_prompt).length > 3000)
    throw new Error("text_prompt must contain 1 to 3000 characters");
  // Duration is controlled in text_prompt, not by a numeric request multiplier.
  for (const key of ["duration", "seconds", "n", "stream"]) {
    if (req[key] !== undefined) throw new Error(key + " is not supported; describe timing in text_prompt");
  }
  if (req.references !== undefined) {
    if (!Array.isArray(req.references) || req.references.length > 3) throw new Error("references must contain at most 3 items");
    let imageCount = 0;
    for (const reference of req.references) {
      if (!isObject(reference)) throw new Error("each reference must be an object");
      const fields = SOURCE_FIELDS.filter((key) => reference[key] !== undefined);
      if (fields.length !== 1 || Object.keys(reference).length !== 1)
        throw new Error("each reference must contain exactly one speaker, audio_data, audio_url, image_data or image_url");
      const key = fields[0];
      const value = reference[key];
      if (typeof value !== "string" || !value.trim()) throw new Error("reference source must be a non-empty string");
      if (key.endsWith("_url") && !isHTTPURL(value)) throw new Error("reference URL must use HTTP or HTTPS without credentials");
      if (key.endsWith("_data")) {
        if (value.length > Math.ceil(MAX_REFERENCE_BYTES / 3) * 4 || value.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(value))
          throw new Error("reference data must be raw Base64 of at most 10 MiB");
        const padding = value.endsWith("==") ? 2 : value.endsWith("=") ? 1 : 0;
        if (value.length / 4 * 3 - padding > MAX_REFERENCE_BYTES) throw new Error("reference data exceeds 10 MiB");
      }
      if (key.startsWith("image_")) imageCount++;
    }
    if (imageCount && (imageCount !== 1 || req.references.length !== 1))
      throw new Error("only one reference image is supported; image and audio references cannot be mixed");
  }
  if (req.audio_config !== undefined) {
    const config = req.audio_config;
    if (!isObject(config)) throw new Error("audio_config must be an object");
    const format = config.format === undefined ? "mp3" : config.format;
    if (typeof format !== "string" || !Object.prototype.hasOwnProperty.call(SAMPLE_RATES, format))
      throw new Error("audio format must be mp3, wav, pcm or ogg_opus");
    if (config.sample_rate !== undefined && !SAMPLE_RATES[format].includes(config.sample_rate))
      throw new Error("sample_rate is not supported for the selected audio format");
    for (const key of ["speech_rate", "loudness_rate", "pitch_rate"]) {
      if (config[key] === undefined) continue;
      const minimum = key === "pitch_rate" ? -12 : -50;
      const maximum = key === "pitch_rate" ? 12 : 100;
      if (!Number.isInteger(config[key]) || config[key] < minimum || config[key] > maximum)
        throw new Error(key + " must be an integer between " + minimum + " and " + maximum);
    }
    if (config.enable_subtitle !== undefined && typeof config.enable_subtitle !== "boolean")
      throw new Error("enable_subtitle must be a boolean");
  }
  if (req.watermark !== undefined && !isObject(req.watermark)) throw new Error("watermark must be an object");
}

export function buildSubmitRequest(ctx) {
  validateRequest(ctx.requestBody);
  const model = ctx.upstreamModel || ctx.model;
  if (model !== MODEL) throw new Error("unsupported JieAPI audio model");
  const req = ctx.requestBody;
  const body = { model, text_prompt: req.text_prompt };
  if (req.references !== undefined) body.references = req.references;
  if (req.watermark !== undefined) body.watermark = req.watermark;
  // MP3 reduces inline Base64 size. The host still limits upstream JSON to
  // 1 MiB; long or uncompressed audio may exceed that limit before hooks run.
  body.audio_config = Object.assign({ format: "mp3" }, req.audio_config || {});
  return {
    method: "POST",
    url: ctx.baseUrl.replace(/\/+$/, "") + "/audios/v1/audio/speech",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body,
  };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid JieAPI audio response" };
  if (body.error) return { status: "FAILURE", reason: "JieAPI audio generation failed" };
  if (!isHTTPURL(body.url) || typeof body.audio !== "string" || !body.audio)
    return { status: "UNKNOWN", reason: "JieAPI returned no audio or download URL" };
  // Never substitute post-processed duration for original billing duration.
  if (typeof body.original_duration !== "number" || !Number.isFinite(body.original_duration) ||
      body.original_duration <= 0 || body.original_duration > MAX_SECONDS)
    return { status: "UNKNOWN", reason: "JieAPI original_duration must be greater than 0 and at most 120 seconds" };
  return { status: "SUCCESS", progress: "100%", url: body.url };
}

export function parseSubmitResponse(ctx, response) {
  const immediate = parseTaskResult(ctx, response.body);
  if (immediate.status === "UNKNOWN") throw new Error(immediate.reason);
  if (!ctx.publicTaskId) throw new Error("missing gateway task id");
  const body = response.body;
  const taskData = immediate.status === "SUCCESS"
    ? { audio: body.audio, url: body.url, duration: body.duration, original_duration: body.original_duration }
    : { error: { code: "audio_generation_failed", message: immediate.reason } };
  if (immediate.status === "SUCCESS" && body.subtitle !== undefined) taskData.subtitle = body.subtitle;
  return { taskId: ctx.publicTaskId, taskData, immediate };
}

export function buildQueryRequest() {
  throw new Error("JieAPI audio completes synchronously and does not provide task polling");
}

export function extractUsage(ctx) {
  validateRequest(ctx.requestBody);
  // The prompt's requested duration is not a reliable upper bound. Reserve
  // the documented maximum, then settle the measured original_duration.
  // Legacy per-call pricing has no seconds multiplier.
  return ctx.usagePurpose === "billing_ratios" ? {} : { seconds: MAX_SECONDS };
}

export function extractUsageOnComplete(ctx, result, body) {
  if (result.status !== "SUCCESS") return { seconds: 0 };
  const parsed = parseTaskResult(ctx, body);
  if (parsed.status !== "SUCCESS") throw new Error(parsed.reason);
  return { seconds: body.original_duration };
}

export const native = {
  generate: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const req = ctx.body.value;
    validateRequest(req);
    return { kind: "submit", model: req.model, action: "audio_generation", requestBody: req };
  },
  audioCreated: function (ctx, task) {
    return task.data;
  },
};
