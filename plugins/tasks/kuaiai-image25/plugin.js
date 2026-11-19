// Channel binding, model mapping and prices belong to gateway configuration.
// This driver only adapts the vendor's synchronous OpenAI Images endpoint.
export const meta = {
  apiVersion: 1,
  key: "kuaiai-image25",
  name: "Kuaiai Image 2.5",
  version: "1.0.1",
  author: { name: "OOpii" },
  description: {
    en: "OpenAI Images generation and editing with multipart parameter compatibility",
    zh: "兼容 multipart 参数的 OpenAI Images 生图与编辑",
  },
  auth: "api_key",
  models: ["all-image-2.5-sunburst-H", "all-image-2.5-flare-H"],
  fetchMode: "per_task",
  protocols: ["openai_image"],
  usageSchema: {
    image_count: {
      type: "number", unit: "count", unitLabel: { en: "image", zh: "张" },
      description: { en: "Image generation unit price", zh: "图片生成单价" },
    },
  },
  usageExamples: [{ label: "Image", facts: { image_count: 1 } }],
};

function isObject(value) {
  return value !== null && typeof value === "object" && !Array.isArray(value);
}

function imageCount(request) {
  const body = request.body;
  const values = body.kind === "multipart" ? (body.fields.n || ["1"]) : [body.value.n === undefined ? 1 : body.value.n];
  if (values.length !== 1 || !/^\d+$/.test(String(values[0]))) throw new Error("n must be a positive integer");
  const count = Number(values[0]);
  if (!Number.isSafeInteger(count) || count < 1 || count > 128) throw new Error("n must be between 1 and 128");
  return count;
}

function hasPixelSize(fields) {
  if (!Array.isArray(fields.size) || fields.size.length !== 1) return false;
  const match = /^(\d+)x(\d+)$/.exec(fields.size[0]);
  return !!match && [match[1], match[2]].every(value => Number(value) > 0 && Number(value) <= 4294967295);
}

export function buildSubmitRequest(ctx) {
  const request = ctx.requestBody;
  imageCount(request);
  const descriptor = {
    url: ctx.baseUrl.replace(/\/+$/, "") + (request.operation === "edit" ? "/v1/images/edits" : "/v1/images/generations"),
    method: "POST",
    headers: { Accept: "application/json", Authorization: "Bearer " + ctx.apiKey },
  };
  const model = ctx.upstreamModel || ctx.model;
  if (request.body.kind === "json") {
    descriptor.headers["Content-Type"] = "application/json";
    descriptor.body = Object.assign({}, request.body.value, { model });
  } else {
    const fields = request.body.fields || {};
    const omitRatio = request.operation === "edit" && hasPixelSize(fields);
    descriptor.bodyType = "multipart";
    descriptor.parts = [{ name: "model", value: model }];
    for (const name of Object.keys(fields)) {
      if (name === "model" || (name === "aspect_ratio" && omitRatio)) continue;
      for (const value of fields[name]) descriptor.parts.push({ name, value });
    }
    for (const file of request.body.files || []) {
      descriptor.parts.push({ name: file.field, fileRef: file.ref, filename: file.filename });
    }
  }
  return descriptor;
}

export function parseTaskResult(ctx, body) {
  if (!isObject(body) || body.error) return { status: "FAILURE", reason: "Image generation failed" };
  const images = body.data;
  if (!Array.isArray(images) || !images.length || images.some(image => !isObject(image) ||
    !(typeof image.url === "string" && /^https?:\/\//i.test(image.url)) &&
    !(typeof image.b64_json === "string" && image.b64_json.length > 0))) {
    return { status: "FAILURE", reason: "Upstream returned no completed image" };
  }
  const result = { status: "SUCCESS", progress: "100%" };
  if (images[0].url) result.url = images[0].url;
  return result;
}

export function parseSubmitResponse(ctx, resp) {
  if (!ctx.publicTaskId) throw new Error("missing gateway task id");
  const immediate = parseTaskResult(ctx, resp.body);
  const taskData = immediate.status === "SUCCESS" ? resp.body : { error: { message: immediate.reason } };
  return { taskId: ctx.publicTaskId, taskData, immediate };
}

export function buildQueryRequest() {
  throw new Error("This upstream completes image requests synchronously");
}

// Ratios only: the gateway retains its existing ModelPrice and group discounts.
export function extractUsage(ctx) {
  return { image_count: imageCount(ctx.requestBody) };
}

export function extractUsageOnSubmit(ctx, body) {
  return { image_count: parseTaskResult(ctx, body).status === "SUCCESS" ? body.data.length : 0 };
}

export const protocols = {
  openai_image: {
    decodeRequest(ctx) {
      if (!["generate", "edit"].includes(ctx.operation)) throw new Error("unsupported image operation");
      if (!ctx.body || !["json", "multipart"].includes(ctx.body.kind)) throw new Error("JSON or multipart body required");
      if (ctx.body.kind === "json" && !isObject(ctx.body.value)) throw new Error("request body must be an object");
      const requestBody = { operation: ctx.operation, body: ctx.body };
      imageCount(requestBody);
      return { kind: "submit", model: ctx.model, action: ctx.operation === "edit" ? "image_edit" : "image_generate", requestBody };
    },
    render(ctx, task) {
      if (parseTaskResult(ctx, task.data).status !== "SUCCESS") throw new Error("no completed image is available");
      return task.data;
    },
  },
};
