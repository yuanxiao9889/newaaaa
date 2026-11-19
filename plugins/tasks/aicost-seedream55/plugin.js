export const meta = {
  apiVersion: 1, key: "aicost-seedream55", name: "AICost Seedream 5.5",
  version: "1.0.2", author: { name: "OOpii" }, auth: "api_key",
  models: ["dou-5-5", "seedream-5.5"], protocols: ["openai_image"], fetchMode: "per_task",
  baseUrl: "https://www.aicost.me",
  description: { en: "Seedream 5.5 generation and reference image editing", zh: "Seedream 5.5 图片生成与参考图编辑" },
  usageSchema: { image_count: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Image generation unit price", zh: "图片生成单价" } } },
  usageExamples: [{ label: "Image", facts: { image_count: 1 } }],
};
const REFERENCES = ["image", "images", "image_url", "image_urls", "reference_images", "input_reference"];
const OUTPUT_FIELDS = ["prompt", "n", "size", "quality", "output_format", "output_compression", "moderation", "response_format", "background", "watermark", "seed", "aspect_ratio", "ratio", "strength", "image"];
const RATIO_SIZES = { "1:1": "2048x2048", "16:9": "2048x1152", "9:16": "1152x2048", "4:3": "2048x1536", "3:4": "1536x2048", "3:2": "2048x1365", "2:3": "1365x2048", "21:9": "2048x878", "7:3": "2048x878" };
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function normalize(ctx) {
  let fields;
  if (ctx.body.kind === "json" && object(ctx.body.value)) fields = { ...ctx.body.value };
  else if (ctx.body.kind === "multipart") {
    fields = {};
    for (const name of Object.keys(ctx.body.fields || {})) {
      const values = ctx.body.fields[name];
      if (!Array.isArray(values) || values.length !== 1) throw new Error("Duplicate image form field: " + name);
      fields[name] = values[0];
    }
  } else throw new Error("JSON or multipart image request required");
  if (typeof fields.prompt !== "string" || !fields.prompt.trim()) throw new Error("prompt is required");
  if (fields.n !== undefined && String(fields.n) !== "1") throw new Error("Seedream 5.5 requires n=1");
  let files = (ctx.body.files || []).map(file => ({ ref: file.ref, field: file.field, filename: file.filename }));
  if (files.length > 10 || files.some(file => !["image", "image[]"].includes(file.field) || typeof file.ref !== "string" || !file.ref.startsWith("request_file:"))) throw new Error("Seedream 5.5 supports at most 10 image files");
  const present = REFERENCES.filter(name => fields[name] !== undefined);
  if (present.length > 1 || files.length && present.length) throw new Error("Use one reference image source");
  if (files.length && fields.oopii_reference_urls_v1 !== undefined) {
    // Storyboard supplies URLs for the same ordered files. Prefer that verified
    // transport; clients without companion URLs retain multipart file support.
    let urls;
    try { urls = JSON.parse(fields.oopii_reference_urls_v1); }
    catch { throw new Error("Reference URLs must be a JSON array"); }
    if (!Array.isArray(urls) || urls.length !== files.length) throw new Error("Reference URL count must match uploaded files");
    if (urls.some(value => typeof value !== "string" || !/^https?:\/\/[^\s/@]+(?:\/|$)/i.test(value))) throw new Error("Reference URLs must be public HTTP(S) addresses");
    fields.image = urls;
    files = [];
  }
  if (present.length) {
    const refs = Array.isArray(fields[present[0]]) ? fields[present[0]] : [fields[present[0]]];
    if (refs.length > 10 || refs.some(value => typeof value !== "string" || !/^(https?:\/\/|data:image\/)/i.test(value))) throw new Error("Seedream 5.5 supports at most 10 URL or data URI references");
    for (const name of REFERENCES) delete fields[name];
    fields.image = refs;
  }
  if (!fields.size && (fields.aspect_ratio || fields.ratio)) {
    fields.size = RATIO_SIZES[fields.aspect_ratio || fields.ratio];
    if (!fields.size) throw new Error("Provide explicit pixel dimensions for this aspect ratio");
  }
  if (fields.watermark !== undefined) {
    if (ctx.body.kind === "multipart" && (fields.watermark === "true" || fields.watermark === "false")) fields.watermark = fields.watermark === "true";
    if (typeof fields.watermark !== "boolean") throw new Error("watermark must be true or false");
  }
  if (ctx.body.kind === "multipart" && !files.length) {
    // Switching from form fields to JSON must preserve scalar types and zeros.
    for (const name of ["seed", "output_compression"]) {
      if (fields[name] === undefined) continue;
      if (!/^-?(0|[1-9][0-9]*)$/.test(fields[name]) || !Number.isSafeInteger(Number(fields[name]))) throw new Error(name + " must be an integer");
      fields[name] = Number(fields[name]);
      if (name === "output_compression" && (fields[name] < 0 || fields[name] > 100)) throw new Error("output_compression must be between 0 and 100");
    }
  }
  fields.n = 1;
  fields.response_format = "url";
  // Forward only single-image fields; arbitrary nested provider parameters
  // must not reintroduce batch counts outside the validated n=1 contract.
  for (const name of Object.keys(fields)) if (!OUTPUT_FIELDS.includes(name)) delete fields[name];
  return { operation: ctx.operation, fields, files };
}
export function buildSubmitRequest(ctx) {
  const request = ctx.requestBody;
  if (!request || request.fields.n !== 1) throw new Error("Invalid normalized AICost image request");
  const values = { ...request.fields, model: ctx.upstreamModel || ctx.model };
  const base = ctx.baseUrl.replace(/\/+$/, "").replace(/\/v1$/, "");
  const descriptor = { url: base + (request.operation === "edit" || request.files.length ? "/v1/images/edits" : "/v1/images/generations"), method: "POST", headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" } };
  if (request.files.length) {
    descriptor.bodyType = "multipart";
    descriptor.parts = Object.keys(values).map(name => ({ name, value: typeof values[name] === "object" ? JSON.stringify(values[name]) : String(values[name]) }));
    for (const file of request.files) descriptor.parts.push({ name: "image", fileRef: file.ref, filename: file.filename || "reference.png" });
  } else {
    descriptor.headers["Content-Type"] = "application/json";
    descriptor.body = values;
  }
  return descriptor;
}
export function parseTaskResult(ctx, body) {
  const image = object(body) && !body.error && Array.isArray(body.data) && body.data.length === 1 && body.data[0];
  if (!object(image) || !(typeof image.url === "string" && /^https?:\/\//i.test(image.url)) && !(typeof image.b64_json === "string" && image.b64_json.length)) return { status: "FAILURE", reason: "AICost did not return one completed image" };
  return { status: "SUCCESS", progress: "100%", url: image.url || "", tokenUsage: body.usage };
}
export function parseSubmitResponse(ctx, response) {
  if (!ctx.publicTaskId) throw new Error("Missing durable image task ID");
  const immediate = parseTaskResult(ctx, response.body);
  const body = response.body;
  // AICost returns both URL and Base64. Keep the URL for durable queued tasks
  // to avoid retaining a redundant multi-megabyte copy in the task snapshot.
  const taskData = immediate.status === "SUCCESS" ? { created: body.created, usage: body.usage, data: body.data.map(item => item.url ? { url: item.url, revised_prompt: item.revised_prompt } : item) } : { error: { message: immediate.reason } };
  return { taskId: ctx.publicTaskId, taskData, immediate };
}
export function buildQueryRequest() { throw new Error("AICost completes image submissions synchronously"); }
export function extractUsage(ctx) {
  if (!ctx.requestBody || ctx.requestBody.fields.n !== 1) throw new Error("Invalid image quantity");
  return { image_count: 1 };
}
export function extractUsageOnSubmit(ctx, body) { return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? 1 : 0 }; }
export function extractUsageOnComplete(ctx, result) { return { image_count: result.status === "SUCCESS" ? 1 : 0 }; }
export const protocols = { openai_image: {
  decodeRequest(ctx) {
    if (!["generate", "edit"].includes(ctx.operation)) throw new Error("Unsupported image operation");
    const requestBody = normalize(ctx);
    return { kind: "submit", model: ctx.model, action: requestBody.files.length || requestBody.fields.image ? "image_edit" : "image_generate", requestBody };
  },
  render(ctx, task) {
    if (parseTaskResult(ctx, task.data).status !== "SUCCESS") throw new Error("No completed image available");
    return task.data;
  },
} };
