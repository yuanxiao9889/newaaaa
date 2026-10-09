// Tencent TokenHub Hy-Image-3.5-preview synchronous API:
// https://cloud.tencent.com/document/product/1823/135745
const MODEL = "hy-image-v3.5-preview";
const VENDOR_PATH = "/v1/wand/hunyuan-image/v35-generation";
const MAX_IMAGE_BYTES = 20 * 1024 * 1024;
// TokenHub does not publish a preflight tokenizer or a total-token ceiling.
// This is a coarse reservation, not measured usage or a provider limit. The
// host reconciles it with actual billing tokens on immediate completion.
const RESERVATION_TOKENS = 100000;
const OPTIONS = ["size", "seed", "generate_max_pixels", "resize_max_pixels", "session", "footnote", "use_search_tool"];

export const meta = {
  apiVersion: 1,
  key: "tencent-hy-image",
  name: "Tencent TokenHub Hy Image",
  icon: "text:HY",
  version: "1.0.0",
  author: { name: "OOpii" },
  description: {
    en: "Hy-Image-3.5-preview generation and multi-turn editing through Tencent TokenHub",
    zh: "通过腾讯 TokenHub 调用 Hy-Image-3.5-preview 生图与多轮编辑",
  },
  baseUrl: "https://tokenhub.tencentmaas.com",
  auth: "api_key",
  models: [MODEL],
  fetchMode: "per_task",
  protocols: ["openai_image"],
  routes: [{
    method: "POST", path: "/tencent-hy-image" + VENDOR_PATH,
    type: "submit", decode: "generate", render: "imageCreated", retainResult: false,
  }],
  usageSchema: {
    image_count: {
      type: "number", unit: "count", unitLabel: { en: "image", zh: "张" },
      description: { en: "Image generation unit price", zh: "图片生成单价" },
    },
    tokens: {
      type: "number", unit: "token",
      description: { en: "Image generation token unit price", zh: "图片生成 Token 单价" },
    },
  },
  usageExamples: [{ label: "Hy Image", facts: { image_count: 1, tokens: 20000 } }],
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function isHTTPURL(value) {
  return typeof value === "string" && /^https?:\/\/[^\s/?#@\\]+(?:[/?#][^\s\\]*)?$/i.test(value);
}

function validateReference(value) {
  // Only the host resolves these request-scoped multipart references.
  if (isObject(value) && typeof value.__fileRef === "string" && value.encoding === "dataUrl" && value.maxBytes === MAX_IMAGE_BYTES) return;
  if (isHTTPURL(value)) return;
  if (typeof value !== "string") throw new Error("image must be an HTTP(S) URL or a PNG/JPEG data URL");
  const match = /^data:image\/(?:png|jpeg|jpg);base64,([A-Za-z0-9+/]+={0,2})$/.exec(value);
  if (!match || match[1].length % 4 !== 0) throw new Error("image must be an HTTP(S) URL or a PNG/JPEG data URL");
  const padding = match[1].endsWith("==") ? 2 : match[1].endsWith("=") ? 1 : 0;
  if (match[1].length / 4 * 3 - padding > MAX_IMAGE_BYTES) throw new Error("each reference image must be at most 20 MiB");
}

function validateRequest(req) {
  if (!isObject(req)) throw new Error("request body must be an object");
  if (typeof req.model !== "string" || !req.model.trim()) throw new Error("model is required");
  if (req.n !== undefined && req.n !== 1) throw new Error("n must be 1; Hy Image generates one image per request");
  if (req.stream !== undefined && req.stream !== false) throw new Error("stream is not supported by the TokenHub image endpoint");
  if (req.size !== undefined && req.size !== "") {
    const match = typeof req.size === "string" && /^(\d+)x(\d+)$/.exec(req.size);
    if (!match) throw new Error("size must use WIDTHxHEIGHT");
    const width = Number(match[1]);
    const height = Number(match[2]);
    if (width < 256 || width > 8192 || height < 256 || height > 8192 || width * height > 16777216)
      throw new Error("size dimensions must be 256 to 8192 and total area must not exceed 16777216 pixels");
  }
  // Sobek numbers cannot faithfully round-trip every int64 seed.
  if (req.seed !== undefined && (!Number.isSafeInteger(req.seed) || req.seed < 0))
    throw new Error("seed must be an integer between 0 and 9007199254740991");
  for (const key of ["generate_max_pixels", "resize_max_pixels"]) {
    if (req[key] !== undefined && (!Number.isSafeInteger(req[key]) || req[key] <= 0))
      throw new Error(key + " must be a positive safe integer");
  }
  if (req.session !== undefined && typeof req.session !== "string") throw new Error("session must be a string");
  if (req.footnote !== undefined && (typeof req.footnote !== "string" || Array.from(req.footnote).length > 16))
    throw new Error("footnote must contain at most 16 characters");
  if (req.use_search_tool !== undefined && (!isObject(req.use_search_tool) || typeof req.use_search_tool.value !== "boolean" || Object.keys(req.use_search_tool).length !== 1))
    throw new Error("use_search_tool must be an object containing a boolean value");
  if (!Array.isArray(req.messages) || !req.messages.length) throw new Error("messages must be a non-empty array");
  let hasUser = false;
  let imageCount = 0;
  for (const message of req.messages) {
    if (!isObject(message) || !["user", "assistant", "tool"].includes(message.role)) throw new Error("message role must be user, assistant or tool");
    if (message.content === undefined && message.role === "assistant" && Array.isArray(message.tool_calls) && message.tool_calls.length) continue;
    if (!Array.isArray(message.content) || !message.content.length) throw new Error("message content must be a non-empty array");
    if (message.role === "user") hasUser = true;
    for (const part of message.content) {
      if (!isObject(part)) throw new Error("message content entries must be objects");
      if (part.type === "text") {
        if (typeof part.text !== "string" || !part.text.trim()) throw new Error("text content must be a non-empty string");
      } else if (part.type === "image_url") {
        if (!isObject(part.image_url)) throw new Error("image_url must contain a url");
        validateReference(part.image_url.url);
        imageCount++;
      } else throw new Error("content type must be text or image_url");
    }
  }
  if (!hasUser) throw new Error("messages must contain a user message");
  if (imageCount > 20) throw new Error("at most 20 reference images are supported");
  return imageCount ? "image_to_image" : "text_to_image";
}

function billingTokens(body) {
  // The gateway's billing counter takes precedence, including explicit zero.
  for (const usage of [body.tokenhub_usage, body.usage]) {
    const value = usage && usage.total_tokens;
    if (typeof value === "number" && Number.isInteger(value) && value >= 0 && value <= 2147483647) return value;
  }
  return undefined;
}

export function buildSubmitRequest(ctx) {
  validateRequest(ctx.requestBody);
  const model = ctx.upstreamModel || ctx.model;
  if (model !== MODEL) throw new Error("unsupported Tencent Hy Image model");
  const body = { model, messages: ctx.requestBody.messages };
  for (const key of OPTIONS) {
    if (ctx.requestBody[key] !== undefined) body[key] = ctx.requestBody[key];
  }
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + VENDOR_PATH,
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
    body,
  };
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body)) return { status: "UNKNOWN", reason: "invalid TokenHub image response" };
  const choice = Array.isArray(body.choices) && body.choices[0];
  if (body.error || (choice && ["error", "content_filter"].includes(choice.finish_reason))) {
    // Never echo upstream error text, which may include request credentials.
    return { status: "FAILURE", reason: "TokenHub image generation failed or was rejected by content moderation" };
  }
  const image = choice && choice.delta && choice.delta.image;
  // finish_reason is normally null. Only the final delivery URL proves success;
  // assembled_history contains intermediate images for the next edit round.
  if (!image || !isHTTPURL(image.url)) return { status: "UNKNOWN", reason: "TokenHub returned no final image URL" };
  const result = { status: "SUCCESS", progress: "100%", url: image.url };
  const tokens = billingTokens(body);
  if (tokens !== undefined) result.tokenUsage = { total_tokens: tokens };
  return result;
}

export function parseSubmitResponse(ctx, resp) {
  const immediate = parseTaskResult(ctx, resp.body);
  if (immediate.status === "UNKNOWN") throw new Error(immediate.reason);
  if (!ctx.publicTaskId) throw new Error("missing gateway task id");
  let taskData = resp.body;
  if (immediate.status === "FAILURE") {
    taskData = { error: { code: "image_generation_failed", message: immediate.reason } };
    if (typeof resp.body.request_id === "string") taskData.request_id = resp.body.request_id;
  }
  return { taskId: ctx.publicTaskId, taskData, immediate };
}

export function buildQueryRequest() {
  // Required by API v1; every submission here completes synchronously.
  throw new Error("TokenHub Hy Image does not provide a task polling API");
}

export function extractUsage(ctx) {
  validateRequest(ctx.requestBody);
  if (ctx.usagePurpose === "billing_ratios") return { image_count: 1 };
  return { image_count: 1, tokens: RESERVATION_TOKENS };
}

export function extractUsageOnSubmit(ctx, body) {
  return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? 1 : 0 };
}

export function extractUsageOnComplete(ctx, result, body) {
  if (result.status !== "SUCCESS") return { image_count: 0, tokens: 0 };
  const facts = { image_count: 1 };
  const tokens = billingTokens(body || {});
  // Missing/invalid counters retain the reservation; never manufacture usage.
  if (tokens !== undefined) facts.tokens = tokens;
  return facts;
}

export const native = {
  generate: function (ctx) {
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    const req = ctx.body.value;
    const action = validateRequest(req);
    return { kind: "submit", model: req.model, action, requestBody: req };
  },
  imageCreated: function (ctx, task) {
    if (task.status === "FAILURE") return { error: { code: "image_generation_failed", message: task.fail_reason || "TokenHub image generation failed" } };
    return task.data;
  },
};

export const protocols = {
  openai_image: {
    decodeRequest: function (ctx) {
      if (!["generate", "edit"].includes(ctx.operation)) throw new Error("unsupported image operation");
      let req;
      const uploads = [];
      if (ctx.body && ctx.body.kind === "json") {
        if (!isObject(ctx.body.value)) throw new Error("request body must be an object");
        req = Object.assign({}, ctx.body.value);
      } else if (ctx.body && ctx.body.kind === "multipart") {
        req = {};
        for (const name of Object.keys(ctx.body.fields || {})) {
          const values = ctx.body.fields[name];
          if (values.length !== 1) throw new Error(name + " must be provided once");
          req[name] = values[0];
        }
        for (const key of ["n", "seed", "generate_max_pixels", "resize_max_pixels"]) {
          if (req[key] !== undefined) {
            if (!/^\d+$/.test(req[key])) throw new Error(key + " must be an integer");
            req[key] = Number(req[key]);
          }
        }
        if (req.stream === "false") req.stream = false;
        if (req.use_search_tool !== undefined) {
          try { req.use_search_tool = JSON.parse(req.use_search_tool); }
          catch (e) { throw new Error("use_search_tool must be a JSON object"); }
        }
        for (const file of ctx.body.files || []) {
          if (!/^image(\[\d*\])?$/.test(file.field)) throw new Error("only image files are supported");
          if (!["image/png", "image/jpeg", "image/jpg"].includes(file.mimeType)) throw new Error("reference files must be PNG or JPEG");
          if (file.size <= 0 || file.size > MAX_IMAGE_BYTES) throw new Error("each reference image must be between 1 byte and 20 MiB");
          uploads.push({ __fileRef: file.ref, encoding: "dataUrl", mimeType: file.mimeType, maxBytes: MAX_IMAGE_BYTES });
        }
      } else throw new Error("JSON or multipart body required");
      if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("prompt is required");
      if (req.images !== undefined) throw new Error("use image for reference images");
      for (const key of ["mask", "quality", "style", "background", "output_format", "output_compression", "moderation", "messages"]) {
        if (req[key] !== undefined) throw new Error(key + " is not supported on this image endpoint; use the native route for messages");
      }
      if (req.response_format !== undefined && !["url", "b64_json"].includes(req.response_format)) throw new Error("response_format must be url or b64_json");
      const content = [{ type: "text", text: req.prompt }];
      const images = req.image === undefined ? [] : Array.isArray(req.image) ? req.image : [req.image];
      for (const reference of images.concat(uploads)) {
        content.push({ type: "image_url", image_url: { url: reference } });
      }
      const requestBody = { model: ctx.model, messages: [{ role: "user", content }] };
      for (const key of OPTIONS.concat(["n", "stream"])) {
        if (req[key] !== undefined) requestBody[key] = req[key];
      }
      if (requestBody.size === "auto") delete requestBody.size;
      const action = validateRequest(requestBody);
      if (ctx.operation === "edit" && action !== "image_to_image") throw new Error("image is required for edits");
      return { kind: "submit", model: ctx.model, action, requestBody };
    },
    render: function (ctx, task) {
      const result = parseTaskResult(ctx, task.data);
      if (result.status !== "SUCCESS") throw new Error("no completed image is available");
      const body = { data: [{ url: result.url }] };
      if (result.tokenUsage) body.usage = result.tokenUsage;
      return body;
    },
  },
};
