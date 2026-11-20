export const meta = {
  apiVersion: 1,
  key: "sunburst-images",
  name: "Sunburst and Flare Images",
  version: "1.0.3",
  author: { name: "OOpii" },
  description: {
    en: "Sunburst and Flare image generation and editing",
    zh: "Sunburst 与 Flare 图片生成和编辑",
  },
  auth: "api_key",
  upstreams: ["vendor", "new_api"],
  models: [
    "all-image-2",
    "all-image-2.5-sunburst", "all-image-2.5-flare",
    "gpt-image-2.5-sunburst-opi", "gpt-image-2.5-flare-opi",
    "all-image-2.6-sunburst", "all-image-2.6-flare",
    "all-image-2.5-sunburst-H", "all-image-2.5-flare-H",
    "all-image-2.6-sunburst-H", "all-image-2.6-flare-H",
  ],
  fetchMode: "per_task",
  protocols: ["openai_image"],
  usageSchema: {
    image_count: {
      type: "number", unit: "count", unitLabel: { en: "image", zh: "张" },
      description: { en: "Image generation unit price", zh: "图片生成单价" },
    },
  },
};

function imageCount(body) {
  const fields = body.kind === "multipart" ? body.fields : body.value;
  if (fields.n === undefined) return 1;
  const values = body.kind === "multipart" ? fields.n : [fields.n];
  const value = values[0];
  if (values.length !== 1 || !["number", "string"].includes(typeof value) ||
      !/^\d+$/.test(String(value)) || Number(value) > 128) {
    throw new Error("n must be an integer between 0 and 128");
  }
  return Number(value) || 1;
}

export const protocols = { openai_image: {
  decodeRequest(ctx) {
    const body = ctx.body;
    if (!["generate", "edit"].includes(ctx.operation)) throw new Error("Unsupported image operation");
    if (!body || !["json", "multipart"].includes(body.kind) ||
        (body.kind === "json" && (!body.value || typeof body.value !== "object" || Array.isArray(body.value)))) {
      throw new Error("JSON or multipart image request required");
    }
    const n = imageCount(body);
    const providerParameters = body.kind === "json" ? [body.value.parameters] : body.fields.parameters || [];
    for (const value of providerParameters) {
      const parameters = typeof value === "string" ? JSON.parse(value) : value;
      if (parameters && parameters.n !== undefined && parameters.n !== n) {
        throw new Error("parameters.n must match the image count");
      }
    }
    return { kind: "submit", model: ctx.model, action: ctx.operation === "edit" ? "image_edit" : "image_generate",
      requestBody: { operation: ctx.operation, body } };
  },
  render(ctx, task) {
    const output = imageOutput(task.data);
    if (!output) throw new Error("Completed task has no image");
    return output;
  },
} };

export function buildSubmitRequest(ctx) {
  const request = ctx.requestBody;
  const model = ctx.upstreamModel || ctx.model;
  const url = ctx.baseUrl.replace(/\/+$/, "") + (request.operation === "edit" ? "/v1/images/edits" : "/v1/images/generations");
  const headers = { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" };
  if (request.body.kind === "json") {
    headers["Content-Type"] = "application/json";
    return { url, method: "POST", headers, body: { ...request.body.value, model } };
  }
  const parts = [{ name: "model", value: model }];
  for (const name of Object.keys(request.body.fields)) {
    if (name === "model") continue;
    for (const value of request.body.fields[name]) parts.push({ name, value });
  }
  for (const file of request.body.files || []) parts.push({ name: file.field, fileRef: file.ref, filename: file.filename });
  return { url, method: "POST", headers, bodyType: "multipart", parts };
}

function imageOutput(body) {
  if (!body || typeof body !== "object") return null;
  for (const candidate of [body, body.result]) {
    if (!candidate || !Array.isArray(candidate.data) || !candidate.data.length) continue;
    if (candidate.data.length > 128 || !candidate.data.every(item => item &&
      ((typeof item.url === "string" && /^https?:\/\//i.test(item.url)) ||
       (typeof item.b64_json === "string" && item.b64_json.length > 0)))) continue;
    return { data: candidate.data, ...(candidate.created ? { created: candidate.created } : {}),
      ...(candidate.usage ? { usage: candidate.usage } : {}) };
  }
  if (typeof body.image_url === "string" && /^https?:\/\//i.test(body.image_url)) {
    return { data: [{ url: body.image_url }] };
  }
  return null;
}

function taskId(value) {
  return typeof value === "string" && /^[A-Za-z0-9_-][A-Za-z0-9._-]{0,254}$/.test(value);
}

export function parseSubmitResponse(ctx, response) {
  const body = response.body || {};
  const pending = ["submitted", "queued", "pending", "processing", "running", "in_progress"].includes(String(body.status || "").toLowerCase());
  if (response.statusCode === 202 || pending) {
    const id = taskId(body.id) ? body.id : body.task_id;
    if (!taskId(id)) throw new Error("Accepted image request has no usable task ID");
    return { taskId: id, taskData: { status: body.status || "submitted" } };
  }
  const immediate = parseTaskResult(ctx, body);
  if (immediate.status !== "SUCCESS" && immediate.status !== "FAILURE") throw new Error("Unrecognized image submission response");
  return { taskId: ctx.publicTaskId, immediate, taskData: immediate.status === "SUCCESS" ? imageOutput(body) : { error: immediate.reason } };
}

export function buildQueryRequest(ctx) {
  if (!taskId(ctx.taskId)) throw new Error("Invalid image task ID");
  return { method: "GET", url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/images/tasks/" + encodeURIComponent(ctx.taskId),
    headers: { Authorization: "Bearer " + ctx.apiKey, Accept: "application/json" } };
}

export function parseTaskResult(ctx, body, response) {
  if (response && response.status >= 400) throw new Error("Image status query returned HTTP " + response.status);
  const status = String(body && body.status || "").toLowerCase();
  if (["failed", "failure", "error", "expired", "cancelled", "canceled"].includes(status)) {
    const error = body.error;
    const reason = typeof error === "string" ? error : error && error.message;
    return { status: "FAILURE", reason: String(reason || body.fail_reason || "Upstream image task failed") };
  }
  if (["pending", "submitted", "queued", "not_start"].includes(status)) return { status: "QUEUED" };
  if (["processing", "running", "in_progress"].includes(status)) return { status: "IN_PROGRESS" };
  if (status && !["completed", "succeeded", "success"].includes(status)) return { status: "UNKNOWN" };
  const output = imageOutput(body);
  if (!output || body.error) return { status: "UNKNOWN" };
  const result = { status: "SUCCESS", progress: "100%" };
  if (output.data[0].url) result.url = output.data[0].url;
  if (output.usage) {
    const usage = output.usage;
    result.tokenUsage = { prompt_tokens: usage.prompt_tokens ?? usage.input_tokens,
      completion_tokens: usage.completion_tokens ?? usage.output_tokens, total_tokens: usage.total_tokens };
  }
  return result;
}

export function extractUsage(ctx) {
  return { image_count: imageCount(ctx.requestBody.body) };
}

export function extractUsageOnSubmit(ctx, body) {
  const output = imageOutput(body);
  return output ? { image_count: output.data.length } : {};
}

export function extractUsageOnComplete(ctx, result, body) {
  const output = imageOutput(body);
  return result.status === "SUCCESS" && output ? { image_count: output.data.length } : {};
}
