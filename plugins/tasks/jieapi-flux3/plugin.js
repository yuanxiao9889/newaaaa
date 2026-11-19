// JieAPI contract: https://jieapi.apifox.cn/522306876e0
// The host owns durable async admission, idempotency, image storage and billing.
const MODEL = "flux-3-image";
const SIZES = ["auto", "1024x1024", "1536x1024", "1024x1536", "2560x1440", "3840x2160"];
const MAX_BYTES = 20 * 1024 * 1024; // Gateway operational cap, not a vendor claim.
const OPTIONAL = ["quality", "input_fidelity", "moderation", "output_compression", "user"];

export const meta = {
  apiVersion: 1, key: "jieapi-flux3", name: "JieAPI FLUX 3 Image", version: "1.0.1",
  icon: "text:FLUX", author: { name: "OOpii" }, baseUrl: "https://jieapi.com",
  website: "https://jieapi.apifox.cn/522306876e0",
  description: { zh: "FLUX 3 Image 生图与参考图编辑", en: "FLUX 3 Image generation and reference editing" },
  auth: "api_key", models: [MODEL], fetchMode: "per_task", protocols: ["openai_image"],
  usageSchema: { image_count: { type: "number", unit: "count",
    unitLabel: { zh: "张", en: "image" },
    description: { zh: "图片生成单价", en: "Image generation unit price" } } },
  usageExamples: [{ label: "FLUX 3 Image", facts: { image_count: 1 } }],
};

function object(v) { return v !== null && typeof v === "object" && !Array.isArray(v); }
function httpURL(v) { return typeof v === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(v); }
function reference(v) {
  if (object(v) && typeof v.__fileRef === "string" && v.encoding === "dataUrl" && v.maxBytes === MAX_BYTES) return;
  if (httpURL(v)) return;
  const m = typeof v === "string" && /^data:image\/(?:png|jpeg|jpg|webp);base64,([A-Za-z0-9+/]+={0,2})$/.exec(v);
  if (!m || m[1].length % 4 !== 0 || m[1].length / 4 * 3 > MAX_BYTES + 2)
    throw new Error("image must be an HTTP(S) URL or PNG/JPEG/WebP data URL within 20 MiB");
}
function validate(req) {
  if (!object(req) || typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
  if (req.model !== MODEL) throw new Error("unsupported FLUX model");
  if (req.n !== undefined && req.n !== 1) throw new Error("FLUX tasks support n=1; submit separate tasks for multiple images");
  if (req.size !== undefined && !SIZES.includes(req.size)) throw new Error("unsupported FLUX output size");
  for (const [key, values] of [
    ["quality", ["auto", "low", "medium", "high"]], ["input_fidelity", ["low", "high"]],
    ["moderation", ["auto", "low"]], ["output_format", ["png", "jpeg", "webp"]],
    ["response_format", ["url", "b64_json"]],
  ]) if (req[key] !== undefined && !values.includes(req[key])) throw new Error("invalid " + key);
  if (req.output_compression !== undefined && (!Number.isInteger(req.output_compression) || req.output_compression < 0 || req.output_compression > 100))
    throw new Error("output_compression must be an integer from 0 to 100");
  if (req.user !== undefined && (typeof req.user !== "string" || req.user.length > 256)) throw new Error("invalid user");
  if (req.image !== undefined && (!Array.isArray(req.image) || req.image.length > 16)) throw new Error("at most 16 reference images are supported");
  for (const image of req.image || []) reference(image);
  for (const key of ["mask", "images", "background", "strength", "aspect_ratio", "resolution", "grounding"])
    if (req[key] !== undefined) throw new Error(key + " is not documented by this JieAPI endpoint");
  if (req.stream !== undefined && req.stream !== false) throw new Error("stream is not supported");
}

export function buildSubmitRequest(ctx) {
  validate(ctx.requestBody);
  if ((ctx.upstreamModel || ctx.model) !== MODEL) throw new Error("unsupported mapped FLUX model");
  const req = ctx.requestBody;
  const body = { model: MODEL, prompt: req.prompt, size: req.size || "auto", n: 1,
    output_format: req.output_format || "png", response_format: "url" };
  if (req.image && req.image.length) body.image = req.image;
  for (const key of OPTIONAL) if (req[key] !== undefined) body[key] = req[key];
  return { method: "POST", url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/images/generations",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + ctx.apiKey }, body };
}

export function parseTaskResult(ctx, body) {
  if (!object(body)) return { status: "UNKNOWN", reason: "Invalid FLUX response" };
  if (body.error) return { status: "FAILURE", reason: "JieAPI FLUX image generation failed" };
  if (!Array.isArray(body.data) || body.data.length !== 1) return { status: "UNKNOWN", reason: "FLUX returned no single final image" };
  const image = body.data[0];
  if (!object(image) || (!httpURL(image.url) && !(typeof image.b64_json === "string" && /^[A-Za-z0-9+/]+={0,2}$/.test(image.b64_json) && image.b64_json.length % 4 === 0)))
    return { status: "UNKNOWN", reason: "FLUX returned an invalid image result" };
  return { status: "SUCCESS", progress: "100%", ...(httpURL(image.url) ? { url: image.url } : {}) };
}
export function parseSubmitResponse(ctx, resp) {
  const immediate = parseTaskResult(ctx, resp.body);
  if (immediate.status === "UNKNOWN") throw new Error(immediate.reason);
  if (!ctx.publicTaskId) throw new Error("missing gateway task ID");
  // Persist only deliverable fields; arbitrary upstream metadata is not a public API.
  const taskData = immediate.status === "FAILURE" ? { error: { message: immediate.reason } } : {
    data: resp.body.data.map(image => ({
      ...(httpURL(image.url) ? { url: image.url } : { b64_json: image.b64_json }),
      ...(typeof image.revised_prompt === "string" ? { revised_prompt: image.revised_prompt } : {}),
    })),
  };
  return { taskId: ctx.publicTaskId, taskData, immediate };
}
export function buildQueryRequest() { throw new Error("JieAPI FLUX is synchronous; query the gateway image task instead"); }
export function extractUsage(ctx) { validate(ctx.requestBody); return { image_count: 1 }; }
export function extractUsageOnSubmit(ctx, body) { return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? 1 : 0 }; }
export function extractUsageOnComplete(ctx, result) { return { image_count: result.status === "SUCCESS" ? 1 : 0 }; }

export const protocols = { openai_image: {
  decodeRequest(ctx) {
    if (!["generate", "edit"].includes(ctx.operation)) throw new Error("unsupported image operation");
    let req;
    const files = [];
    if (ctx.body && ctx.body.kind === "json" && object(ctx.body.value)) req = Object.assign({}, ctx.body.value);
    else if (ctx.body && ctx.body.kind === "multipart") {
      req = {};
      for (const key of Object.keys(ctx.body.fields || {})) {
        const values = ctx.body.fields[key];
        if (key === "image" || key === "image[]") {
          req.image = (req.image || []).concat(values);
        } else {
          if (values.length !== 1) throw new Error(key + " must be provided once");
          req[key] = values[0];
        }
      }
      for (const key of ["n", "output_compression"]) if (req[key] !== undefined) {
        if (!/^\d+$/.test(req[key])) throw new Error(key + " must be an integer");
        req[key] = Number(req[key]);
      }
      if (req.stream === "false") req.stream = false;
      for (const file of ctx.body.files || []) {
        if (!/^image(\[\d*\])?$/.test(file.field) || !["image/png", "image/jpeg", "image/webp"].includes(file.mimeType)
          || file.size <= 0 || file.size > MAX_BYTES) throw new Error("invalid reference upload");
        files.push({ __fileRef: file.ref, encoding: "dataUrl", mimeType: file.mimeType, maxBytes: MAX_BYTES });
      }
    } else throw new Error("JSON or multipart body required");
    if (req.image !== undefined && !Array.isArray(req.image)) req.image = [req.image];
    if (files.length) req.image = (req.image || []).concat(files);
    req.model = ctx.model;
    validate(req);
    const action = req.image && req.image.length ? "image_to_image" : "text_to_image";
    if (ctx.operation === "edit" && action !== "image_to_image") throw new Error("image is required for editing");
    return { kind: "submit", model: ctx.model, action: action === "image_to_image" ? "image_edit" : "image_generate", requestBody: req };
  },
  render(ctx, task) {
    if (parseTaskResult(ctx, task.data).status !== "SUCCESS") throw new Error("no completed FLUX image available");
    return { data: task.data.data };
  },
} };
