// Channel-scoped synchronous image driver. The host owns durable tasks,
// reference file bytes, retries, result storage and single settlement.
export const meta = {
  apiVersion: 1, key: "duola-seedream55", name: "Duola Seedream 5.5 Official",
  version: "1.0.2", author: { name: "OOpii" }, auth: "api_key",
  models: ["dou-5-5", "doubao-seedream-5-5-261028"],
  fetchMode: "per_task", protocols: ["openai_image"],
  description: { en: "Seedream 5.5 file edits with watermark disabled", zh: "Seedream 5.5 文件图生图与关闭水印" },
  usageSchema: { image_count: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Image generation unit price", zh: "图片生成单价" } } },
  usageExamples: [{ label: "Image", facts: { image_count: 1 } }],
};

const UPSTREAM_MODEL = "doubao-seedream-5-5-261028";
const REFERENCE_FIELDS = ["image", "image[]", "image_url", "image_urls", "reference_images"];
const OMIT = ["model", "n", "watermark", "response_format", "prompt", "strength", "quality", "aspect_ratio", "oopii_reference_urls_v1"].concat(REFERENCE_FIELDS);
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function fieldsOf(request) {
  if (request.body.kind === "json") return request.body.value;
  const result = {};
  for (const name of Object.keys(request.body.fields || {})) {
    const values = request.body.fields[name];
    if (!Array.isArray(values) || values.length !== 1) throw new Error("Duplicate or invalid image form field: " + name);
    result[name] = values[0];
  }
  return result;
}
function validate(request) {
  const fields = fieldsOf(request);
  if (fields.n !== undefined && String(fields.n) !== "1") throw new Error("Seedream 5.5 requires n=1");
  if (typeof fields.prompt !== "string" || !fields.prompt.trim()) throw new Error("prompt is required");
  const files = request.body.files || [];
  if (files.length > 10) throw new Error("Seedream 5.5 supports at most 10 references");
  for (const file of files) {
    if (!["image", "image[]"].includes(file.field) || typeof file.ref !== "string" || !file.ref.startsWith("request_file:")) throw new Error("Only uploaded image reference files are supported");
  }
  if (files.length && REFERENCE_FIELDS.some(name => fields[name] !== undefined)) throw new Error("Use uploaded files or URL references, not both");
  return fields;
}
function withoutWatermark(prompt) {
  return prompt.trimEnd().endsWith("不要水印。") ? prompt : prompt.trimEnd() + "\n不要水印。";
}

export function buildSubmitRequest(ctx) {
  const request = ctx.requestBody;
  const fields = validate(request);
  const model = ctx.upstreamModel || ctx.model;
  if (model !== UPSTREAM_MODEL) throw new Error("Channel mapping must select " + UPSTREAM_MODEL);
  const files = request.body.files || [];
  const base = String(ctx.baseUrl || "").replace(/\/+$/, "").replace(/\/v1$/, "");
  const descriptor = { url: base + (files.length ? "/v1/images/edits" : "/v1/images/generations"), method: "POST", headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" } };
  const values = { model, n: 1, prompt: withoutWatermark(fields.prompt), watermark: false, response_format: "url" };
  for (const name of Object.keys(fields)) if (!OMIT.includes(name)) values[name] = fields[name];
  if (files.length) {
    descriptor.bodyType = "multipart";
    descriptor.parts = Object.keys(values).map(name => ({ name, value: typeof values[name] === "object" ? JSON.stringify(values[name]) : String(values[name]) }));
    for (const file of files) descriptor.parts.push({ name: "image", fileRef: file.ref, filename: file.filename || "reference.png" });
  } else {
    // Existing clients send URL references to generations. Keep that contract
    // usable; only file edits have verified visible-watermark suppression.
    const present = REFERENCE_FIELDS.filter(name => fields[name] !== undefined);
    if (present.length > 1) throw new Error("Use one reference image field");
    if (present.length) {
      const refs = Array.isArray(fields[present[0]]) ? fields[present[0]] : [fields[present[0]]];
      if (refs.length > 10 || refs.some(value => typeof value !== "string" || !/^https?:\/\//i.test(value))) throw new Error("Legacy references must be at most 10 HTTP(S) URLs; use file edits for local images");
      if (refs.length) values.image = refs;
    }
    descriptor.headers["Content-Type"] = "application/json";
    descriptor.body = values;
  }
  return descriptor;
}
export function parseTaskResult(ctx, body) {
  if (!object(body) || body.error || !Array.isArray(body.data) || body.data.length !== 1) return { status: "FAILURE", reason: "Upstream did not return one completed image" };
  const image = body.data[0];
  if (!object(image) || !(typeof image.url === "string" && /^https?:\/\//i.test(image.url)) && !(typeof image.b64_json === "string" && image.b64_json.length)) return { status: "FAILURE", reason: "Upstream returned no image content" };
  return { status: "SUCCESS", progress: "100%", url: image.url || "" };
}
export function parseSubmitResponse(ctx, resp) {
  if (!ctx.publicTaskId) throw new Error("Missing durable image task ID");
  const immediate = parseTaskResult(ctx, resp.body);
  return { taskId: ctx.publicTaskId, taskData: immediate.status === "SUCCESS" ? resp.body : { error: { message: immediate.reason } }, immediate };
}
export function buildQueryRequest() { throw new Error("This upstream completes image requests synchronously"); }
export function extractUsage(ctx) { validate(ctx.requestBody); return { image_count: 1 }; }
export function extractUsageOnSubmit(ctx, body) { return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? 1 : 0 }; }
export const protocols = {
  openai_image: {
    decodeRequest(ctx) {
      if (!["generate", "edit"].includes(ctx.operation) || !ctx.body || !["json", "multipart"].includes(ctx.body.kind)) throw new Error("JSON or multipart image request required");
      if (ctx.body.kind === "json" && !object(ctx.body.value)) throw new Error("JSON object required");
      const requestBody = { operation: ctx.operation, body: ctx.body };
      validate(requestBody);
      return { kind: "submit", model: ctx.model, action: ctx.operation === "edit" ? "image_edit" : "image_generate", requestBody };
    },
    render(ctx, task) {
      if (parseTaskResult(ctx, task.data).status !== "SUCCESS") throw new Error("No completed image available");
      return task.data;
    },
  },
};
