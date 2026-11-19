// The host owns durable image tasks and settlement. Pidoi's synchronous
// endpoint handles its own private-content/Base64 fallback before replying.
export const meta = {
  apiVersion: 1, key: "pidoi-seedream55", name: "Pidoi Seedream 5.5",
  version: "1.0.1", author: { name: "OOpii" }, auth: "api_key",
  models: ["dou-5-5", "seedream_5.5"], fetchMode: "per_task", protocols: ["openai_image"],
  description: { en: "Seedream 5.5 generation with public URL references", zh: "Seedream 5.5 生成与公开 URL 参考图" },
  usageSchema: { image_count: { type: "number", unit: "count", unitLabel: { en: "image", zh: "张" }, description: { en: "Image generation unit price", zh: "图片生成单价" } } },
  usageExamples: [{ label: "Image", facts: { image_count: 1 } }],
};

const SIZES = { "2048x2048": "1:1", "2048x1152": "16:9", "1152x2048": "9:16", "2048x1536": "4:3", "1536x2048": "3:4", "2048x878": "21:9" };
const REFS = ["images", "image", "image_urls", "image_url", "reference_images"];
const URLS_FIELD = "oopii_reference_urls_v1";
function object(value) { return value !== null && typeof value === "object" && !Array.isArray(value); }
function normalize(body) {
  let input;
  if (body.kind === "json" && object(body.value)) input = body.value;
  else if (body.kind === "multipart") {
    input = {};
    for (const name of Object.keys(body.fields || {})) {
      const values = body.fields[name];
      if (!Array.isArray(values) || values.length !== 1) throw new Error("Duplicate image form field: " + name);
      input[name] = values[0];
    }
  } else throw new Error("JSON or multipart image request required");
  if (typeof input.prompt !== "string" || !input.prompt.trim()) throw new Error("prompt is required");
  if (input.n !== undefined && String(input.n) !== "1") throw new Error("Pidoi Seedream 5.5 requires n=1");
  if (input.resolution !== undefined && input.resolution !== "2K") throw new Error("Pidoi Seedream 5.5 supports only 2K");
  const ratio = input.aspect_ratio || input.ratio || (input.size ? SIZES[input.size] : "1:1");
  if (!Object.values(SIZES).includes(ratio) || (input.size && SIZES[input.size] !== ratio) || (input.aspect_ratio && input.ratio && input.aspect_ratio !== input.ratio)) throw new Error("Pidoi Seedream 5.5 requires a supported 2K size and aspect ratio");
  const present = REFS.filter(name => input[name] !== undefined);
  const files = body.files || [];
  let images = [];
  if (files.length) {
    if (files.some(file => !["image", "image[]"].includes(file.field)) || present.length) throw new Error("Ambiguous reference image fields");
    if (typeof input[URLS_FIELD] !== "string") throw new Error("Pidoi requires public reference URLs; update the client or use JSON URL references");
    images = JSON.parse(input[URLS_FIELD]);
    if (!Array.isArray(images) || images.length !== files.length) throw new Error("Reference URL count must match uploaded files");
  } else {
    if (input[URLS_FIELD] !== undefined || present.length > 1) throw new Error("Use one reference image field");
    if (present.length) images = Array.isArray(input[present[0]]) ? input[present[0]] : [input[present[0]]];
  }
  if (images.length > 9 || images.some(url => typeof url !== "string" || !/^https?:\/\/[^\s]+$/i.test(url))) throw new Error("Pidoi supports at most 9 HTTP(S) reference URLs");
  if (input.mask !== undefined || (input.background && !["opaque", "auto"].includes(input.background))) throw new Error("Pidoi does not support masks or transparent backgrounds");
  const prompt = input.prompt.trimEnd();
  return { prompt: prompt.endsWith("不要水印。") ? prompt : prompt + "\n不要水印。", aspect_ratio: ratio, resolution: "2K", n: 1, images };
}

export function buildSubmitRequest(ctx) {
  if ((ctx.upstreamModel || ctx.model) !== "seedream_5.5") throw new Error("Channel mapping must select seedream_5.5");
  return {
    url: ctx.baseUrl.replace(/\/+$/, "").replace(/\/v1$/, "") + "/v1/images/generations",
    method: "POST", headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json", Accept: "application/json" },
    body: Object.assign({ model: "seedream_5.5" }, ctx.requestBody),
  };
}
export function parseTaskResult(ctx, body) {
  if (!object(body) || body.error || !Array.isArray(body.data) || body.data.length !== 1) return { status: "FAILURE", reason: "Pidoi did not return one completed image" };
  const image = body.data[0];
  if (!object(image) || !(typeof image.url === "string" && /^https?:\/\//i.test(image.url)) && !(typeof image.b64_json === "string" && image.b64_json.length)) return { status: "FAILURE", reason: "Pidoi returned no image content" };
  return { status: "SUCCESS", progress: "100%", url: image.url || "" };
}
export function parseSubmitResponse(ctx, resp) {
  if (!ctx.publicTaskId) throw new Error("Missing durable image task ID");
  const immediate = parseTaskResult(ctx, resp.body);
  return { taskId: ctx.publicTaskId, taskData: immediate.status === "SUCCESS" ? resp.body : { error: { message: immediate.reason } }, immediate };
}
export function buildQueryRequest() { throw new Error("Synchronous upstream results must not be resubmitted"); }
export function extractUsage(ctx) {
  if (!ctx.requestBody || ctx.requestBody.n !== 1) throw new Error("Invalid image quantity");
  return { image_count: 1 };
}
export function extractUsageOnSubmit(ctx, body) { return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? 1 : 0 }; }
export const protocols = {
  openai_image: {
    decodeRequest(ctx) {
      if (!["generate", "edit"].includes(ctx.operation)) throw new Error("Unsupported image operation");
      const requestBody = normalize(ctx.body);
      return { kind: "submit", model: ctx.model, action: requestBody.images.length ? "image_edit" : "image_generate", requestBody };
    },
    render(ctx, task) {
      if (parseTaskResult(ctx, task.data).status !== "SUCCESS") throw new Error("No completed image available");
      return task.data;
    },
  },
};
