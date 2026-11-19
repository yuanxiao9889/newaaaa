export const meta = {
  apiVersion: 1, key: "mai-seedream55", name: "MAI Token Seedream 5.5",
  version: "1.0.2", author: { name: "OOpii" }, auth: "api_key",
  models: ["dou-5-5", "seedream-5.5"], protocols: ["openai_image"], fetchMode: "per_task",
  baseUrl: "https://api.mai-token.com",
  description: { en: "Seedream 5.5 image tasks with supported aspect ratios", zh: "Seedream 5.5 图片任务与可用画幅适配" },
  usageSchema: { image_count: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Image generation unit price", zh: "图片生成单价" } } },
  usageExamples: [{ label: "Image", facts: { image_count: 1 } }],
};

// Existing channel presets are preserved; unsupported ultrawide requests are
// declined during candidate decoding, before channel selection and charging.
const SIZES = {
  "1024x1024": {
    "quality": "1080P",
    "aspect_ratio": "1:1"
  },
  "1280x720": {
    "quality": "1080P",
    "aspect_ratio": "16:9"
  },
  "720x1280": {
    "quality": "1080P",
    "aspect_ratio": "9:16"
  },
  "1152x864": {
    "quality": "1080P",
    "aspect_ratio": "4:3"
  },
  "864x1152": {
    "quality": "1080P",
    "aspect_ratio": "3:4"
  },
  "1248x832": {
    "quality": "1080P",
    "aspect_ratio": "3:2"
  },
  "832x1248": {
    "quality": "1080P",
    "aspect_ratio": "2:3"
  },
  "1440x720": {
    "quality": "1080P",
    "aspect_ratio": "2:1"
  },
  "720x1440": {
    "quality": "1080P",
    "aspect_ratio": "1:2"
  },
  "1456x624": {
    "quality": "1080P",
    "aspect_ratio": "21:9"
  },
  "2048x2048": {
    "quality": "2K",
    "aspect_ratio": "1:1"
  },
  "2048x1152": {
    "quality": "2K",
    "aspect_ratio": "16:9"
  },
  "1152x2048": {
    "quality": "2K",
    "aspect_ratio": "9:16"
  },
  "2304x1728": {
    "quality": "2K",
    "aspect_ratio": "4:3"
  },
  "1728x2304": {
    "quality": "2K",
    "aspect_ratio": "3:4"
  },
  "2496x1664": {
    "quality": "2K",
    "aspect_ratio": "3:2"
  },
  "1664x2496": {
    "quality": "2K",
    "aspect_ratio": "2:3"
  },
  "2048x1024": {
    "quality": "2K",
    "aspect_ratio": "2:1"
  },
  "1024x2048": {
    "quality": "2K",
    "aspect_ratio": "1:2"
  },
  "3024x1296": {
    "quality": "2K",
    "aspect_ratio": "21:9"
  },
  "848x1264": {
    "quality": "1080p",
    "aspect_ratio": "2:3"
  },
  "1264x848": {
    "quality": "1080p",
    "aspect_ratio": "3:2"
  },
  "896x1200": {
    "quality": "1080p",
    "aspect_ratio": "3:4"
  },
  "1200x896": {
    "quality": "1080p",
    "aspect_ratio": "4:3"
  },
  "928x1152": {
    "quality": "1080p",
    "aspect_ratio": "4:5"
  },
  "1152x928": {
    "quality": "1080p",
    "aspect_ratio": "5:4"
  },
  "768x1376": {
    "quality": "1080p",
    "aspect_ratio": "9:16"
  },
  "1376x768": {
    "quality": "1080p",
    "aspect_ratio": "16:9"
  },
  "1792x768": {
    "quality": "1080p",
    "aspect_ratio": "21:9"
  },
  "1365x2048": {
    "quality": "1440p",
    "aspect_ratio": "2:3"
  },
  "2048x1365": {
    "quality": "1440p",
    "aspect_ratio": "3:2"
  },
  "1536x2048": {
    "quality": "1440p",
    "aspect_ratio": "3:4"
  },
  "2048x1536": {
    "quality": "1440p",
    "aspect_ratio": "4:3"
  },
  "1638x2048": {
    "quality": "1440p",
    "aspect_ratio": "4:5"
  },
  "2048x1638": {
    "quality": "1440p",
    "aspect_ratio": "5:4"
  },
  "2048x878": {
    "quality": "1440p",
    "aspect_ratio": "21:9"
  }
};
const REFERENCES = ["image", "images", "image_url", "image_urls", "reference_images", "input_reference"];
const DETAIL = { low: "Low", medium: "Medium", high: "High", xhigh: "Extra High", max: "Max" };
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function publicURL(value) { return typeof value === "string" && /^https?:\/\/[^\s/@]+(?:\/|$)/i.test(value); }
function normalize(body) {
  let input;
  if (body.kind === "json" && object(body.value)) input = body.value;
  else if (body.kind === "multipart") {
    input = {};
    for (const key of Object.keys(body.fields || {})) {
      const values = body.fields[key];
      if (!Array.isArray(values) || values.length !== 1) throw new Error("Duplicate image form field: " + key);
      input[key] = values[0];
    }
  } else throw new Error("JSON or multipart image request required");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  if (input.n !== undefined && String(input.n) !== "1") throw new Error("Seedream 5.5 requires n=1");
  const ratios = [input.aspect_ratio, input.ratio].filter(value => value !== undefined);
  if (input.size !== undefined) {
    const match = /^([1-9][0-9]*)x([1-9][0-9]*)$/.exec(input.size);
    if (match) {
      const w = Number(match[1]), h = Number(match[2]);
      const distance = Math.abs(w * 3 - h * 7);
      if (Number.isSafeInteger(w) && Number.isSafeInteger(h) && distance <= 7 && distance <= h * 7 * 0.005) ratios.push("21:9");
    }
  }
  if (ratios.some(value => typeof value === "string" && /^(21:9|7:3)$/.test(value))) throw new Error("MAI Token Seedream 5.5 does not currently accept 21:9; use another channel");
  if (input.mask !== undefined || (input.background && !["auto", "opaque"].includes(input.background))) throw new Error("MAI Token Seedream 5.5 does not support masks or transparent backgrounds");
  if (input.aspect_ratio && input.ratio && input.aspect_ratio !== input.ratio) throw new Error("Conflicting aspect ratios");
  const preset = input.size ? SIZES[input.size] : null;
  if (input.size && !preset) throw new Error("Unsupported MAI Token Seedream 5.5 size");
  const ratio = preset ? preset.aspect_ratio : input.aspect_ratio || input.ratio || "1:1";
  if (ratios.some(value => value !== ratio)) throw new Error("Size and aspect ratio do not match");
  if (!["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "2:1", "1:2", "4:5", "5:4"].includes(ratio)) throw new Error("Unsupported MAI Token Seedream 5.5 aspect ratio");
  const output = { prompt: input.prompt, n: 1, "async": true, stream: false, aspect_ratio: ratio, quality: preset ? preset.quality : "2K" };
  if (input.resolution !== undefined) {
    if (preset && String(input.resolution).toLowerCase() !== preset.quality.toLowerCase()) throw new Error("Size and resolution do not match");
    if (!["1080p", "1440p", "2k"].includes(String(input.resolution).toLowerCase())) throw new Error("Unsupported MAI Token Seedream 5.5 resolution");
    output.quality = input.resolution;
  }
  if (input.quality !== undefined && input.quality !== "auto") {
    if (!DETAIL[input.quality]) throw new Error("Unsupported image quality");
    output.detail_level = DETAIL[input.quality];
  }
  const present = REFERENCES.filter(key => input[key] !== undefined);
  const files = body.files || [];
  if (input.content !== undefined && (present.length || files.length)) throw new Error("Use one reference image source");
  let references = [];
  if (files.length) {
    if (files.some(file => !["image", "image[]"].includes(file.field)) || present.length) throw new Error("Use one reference image source");
    // Storyboard supplies public URLs alongside file uploads. Other file-only
    // requests remain eligible for the AICost plugin's multipart endpoint.
    if (typeof input.oopii_reference_urls_v1 !== "string") throw new Error("MAI Token requires public reference URLs; use a file-compatible channel");
    references = JSON.parse(input.oopii_reference_urls_v1);
    if (!Array.isArray(references) || references.length !== files.length) throw new Error("Reference URL count must match uploaded files");
  } else if (present.length) {
    if (present.length > 1) throw new Error("Use one reference image field");
    references = Array.isArray(input[present[0]]) ? input[present[0]] : [input[present[0]]];
  } else if (input.content !== undefined) {
    if (!Array.isArray(input.content)) throw new Error("Image content must be an array");
    references = input.content.map(item => object(item) && item.type === "image_url" && object(item.image_url) ? item.image_url.url : null);
  }
  if (references.length > 16 || references.some(value => !publicURL(value))) throw new Error("MAI Token accepts at most 16 public reference image URLs");
  if (references.length) output.content = references.map(url => ({ type: "image_url", role: "reference_image", image_url: { url } }));
  if (input.seed !== undefined) output.seed = input.seed;
  if (input.watermark !== undefined) {
    // Multipart fields are text; the upstream JSON field must stay boolean,
    // including an explicit false. Reject ambiguous values before submission.
    let watermark = input.watermark;
    if (body.kind === "multipart" && (watermark === "true" || watermark === "false")) watermark = watermark === "true";
    if (typeof watermark !== "boolean") throw new Error("watermark must be true or false");
    output.watermark = watermark;
  }
  return output;
}
function base(ctx) { return ctx.baseUrl.replace(/\/+$/, "").replace(/\/v1$/, ""); }
function imageResult(body) {
  if (!object(body) || !Array.isArray(body.data) || body.data.length !== 1) return null;
  const item = body.data[0];
  return object(item) && (publicURL(item.url) || typeof item.b64_json === "string" && item.b64_json.length) ? item : null;
}
export function buildSubmitRequest(ctx) {
  if (!ctx.requestBody || ctx.requestBody.n !== 1 || ctx.requestBody.aspect_ratio === "21:9") throw new Error("Invalid normalized MAI Token image request");
  return { url: base(ctx) + "/v1/images/generations", method: "POST", headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" }, body: { ...ctx.requestBody, model: ctx.upstreamModel || ctx.model } };
}
export function parseTaskResult(ctx, body) {
  if (!object(body)) return { status: "UNKNOWN" };
  if (body.error || ["failed", "cancelled", "canceled", "expired"].includes(body.status)) return { status: "FAILURE", reason: "MAI Token image generation failed" };
  const image = imageResult(body);
  if (image) return { status: "SUCCESS", progress: "100%", url: image.url || "", tokenUsage: body.usage };
  if (["queued", "pending", "submitted"].includes(body.status)) return { status: "SUBMITTED", progress: "0%" };
  if (["in_progress", "running", "processing"].includes(body.status)) return { status: "IN_PROGRESS" };
  return { status: "UNKNOWN" };
}
export function parseSubmitResponse(ctx, response) {
  const result = parseTaskResult(ctx, response.body);
  const id = response.body && (response.body.id || response.body.task_id);
  if (result.status === "SUCCESS" || result.status === "FAILURE") return { taskId: id || ctx.publicTaskId, taskData: response.body, immediate: result };
  if (typeof id !== "string" || !/^[a-zA-Z0-9_-]+$/.test(id) || !["SUBMITTED", "IN_PROGRESS"].includes(result.status)) throw new Error("MAI Token returned no recognized image task");
  return { taskId: id, taskData: response.body };
}
export function buildQueryRequest(ctx) {
  if (typeof ctx.taskId !== "string" || !/^[a-zA-Z0-9_-]+$/.test(ctx.taskId)) throw new Error("Invalid image task ID");
  return { url: base(ctx) + "/v1/images/generations/" + encodeURIComponent(ctx.taskId), method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}
export function extractUsage(ctx) {
  if (!ctx.requestBody || ctx.requestBody.n !== 1) throw new Error("Invalid image quantity");
  return { image_count: 1 };
}
export function extractUsageOnComplete(ctx, result, data) { return { image_count: result.status === "SUCCESS" && imageResult(data) ? 1 : 0 }; }
export const protocols = { openai_image: {
  decodeRequest(ctx) {
    const requestBody = normalize(ctx.body);
    return { kind: "submit", model: ctx.model, action: requestBody.content ? "image_edit" : "image_generate", requestBody };
  },
  render(ctx, task) {
    const image = imageResult(task.data);
    if (!image) throw new Error("No completed image available");
    return { created: task.data.created, data: [image] };
  },
} };
