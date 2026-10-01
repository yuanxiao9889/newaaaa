export const meta = {
  apiVersion: 1,
  key: "huiqu-h3",
  name: "Huiqu MiniMax H3",
  description: {
    en: "Huiqu MiniMax H3 video generation with final video artifact retrieval",
    zh: "汇取云 MiniMax H3 视频生成及最终视频获取",
  },
  version: "1.0.1",
  author: { name: "OOpii" },
  baseUrl: "https://api.bjhuiqu.net",
  auth: "api_key",
  // Explicit channel binding only; do not replace the OpenAI/Sora channel adaptor.
  models: ["Minimax-H3-10s", "Minimax-H3-15s"],
  fetchMode: "per_task",
  protocols: ["openai_video"],
  usageSchema: {
    video_count: {
      type: "number",
      unit: "count",
      unitLabel: { en: "video", zh: "条" },
      description: { en: "Video generation unit price", zh: "视频生成单价" },
    },
  },
  usageExamples: [{ label: "H3 video", facts: { video_count: 1 } }],
};

function validateRequest(req, model) {
  if (!meta.models.includes(model) && !["Minimax-H3-768p-933-10s", "Minimax-H3-768p-933-15s"].includes(model))
    throw new Error("unsupported Huiqu H3 model");
  if (typeof req.prompt !== "string" || !req.prompt.trim()) throw new Error("field prompt is required");
  if (req.metadata !== undefined && (!req.metadata || typeof req.metadata !== "object" || Array.isArray(req.metadata)))
    throw new Error("metadata must be an object");
  if (req.images !== undefined && (!Array.isArray(req.images) || req.images.length > 9))
    throw new Error("images must be an array with at most 9 references");
  // Validate both aliases, including metadata, before any billing multiplier is read.
  for (const values of [req, req.metadata || {}]) {
    for (const key of ["seconds", "duration"]) {
      if (values[key] !== undefined && (!Number.isFinite(Number(values[key])) || Number(values[key]) <= 0 || Number(values[key]) > 3600))
        throw new Error(key + " must be between 1 and 3600");
    }
    if (values.n !== undefined && Number(values.n) !== 1) throw new Error("Huiqu H3 generates one video per task");
  }
}

// H3 returns an intermediate node before the final video. Never use array order
// or the envelope's result_url to choose the downloadable artifact.
function videoArtifact(body) {
  const task = body && body.data;
  const results = task && task.data && task.data.results;
  if (!Array.isArray(results)) return null;
  const videos = [];
  for (const result of results) {
    if (!result || typeof result.url !== "string") continue;
    const url = result.url;
    if (!/^https:\/\/[^/?#@]+\//i.test(url) || /[\s\\\u0000-\u001f\u007f]/.test(url)) continue;
    const type = String(result.outputType || "").toLowerCase();
    const extension = /\.(mp4|webm|mov)(?:[?#]|$)/i.exec(url);
    const videoType = ["mp4", "webm", "mov"].includes(type) ? type : { "video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov" }[type];
    if (type ? !videoType : !extension) continue;
    if (/\/v1\/videos\/[^/?#]+\/content(?:[?#]|$)/i.test(url)) continue;
    const format = videoType || extension[1].toLowerCase();
    if (!videos.some(function (item) { return item.url === url; })) {
      videos.push({ url, mimeType: { mp4: "video/mp4", webm: "video/webm", mov: "video/quicktime" }[format] });
    }
  }
  // Ambiguous outputs should not silently deliver a different node's video.
  return videos.length === 1 ? videos[0] : null;
}

export function buildSubmitRequest(ctx) {
  const req = Object.assign({}, ctx.requestBody || {}, { model: ctx.upstreamModel });
  validateRequest(req, ctx.upstreamModel);
  const headers = { Authorization: "Bearer " + ctx.apiKey };
  const url = ctx.baseUrl.replace(/\/+$/, "") + "/v1/videos";
  if ((ctx.files || []).length) {
    // Huiqu documents JSON images with URL/base64 references. Let the host read
    // bounded file references into JSON; its multipart transport loses Content-Type.
    req.images = (req.images || []).slice();
    for (const file of ctx.files) req.images.push({ __fileRef: file.ref, encoding: "dataUrl" });
    if (req.images.length > 9) throw new Error("images must contain at most 9 references");
  }
  headers["Content-Type"] = "application/json";
  return { url, method: "POST", headers, body: req };
}

export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  const taskId = body.id || body.task_id;
  if (typeof taskId !== "string" || !taskId.trim()) throw new Error("task_id is empty");
  return { taskId, taskData: body };
}

export function extractUsage(ctx) {
  validateRequest(ctx.requestBody || {}, ctx.upstreamModel || ctx.model);
  // Keep existing administrator per-call prices and group multipliers intact.
  return ctx.usagePurpose === "billing_ratios" ? null : { video_count: 1 };
}

export function buildQueryRequest(ctx) {
  return {
    url: ctx.baseUrl.replace(/\/+$/, "") + "/v1/video/generations/" + encodeURIComponent(ctx.taskId),
    method: "GET",
    headers: { Authorization: "Bearer " + ctx.apiKey },
  };
}

export function parseTaskResult(ctx, body) {
  const task = body && body.data;
  if (!body || body.code !== "success" || !task || task.task_id !== ctx.taskId)
    return { status: "UNKNOWN", reason: "invalid Huiqu task response" };
  const statuses = { NOT_START: "NOT_START", SUBMITTED: "SUBMITTED", QUEUED: "QUEUED", IN_PROGRESS: "IN_PROGRESS", SUCCESS: "SUCCESS", FAILURE: "FAILURE" };
  const status = statuses[task.status];
  if (!status) return { status: "UNKNOWN", reason: "unrecognized Huiqu task status" };
  const result = { status };
  if (status === "SUCCESS") {
    result.progress = "100%";
    const video = videoArtifact(body);
    if (video) result.url = video.url;
    // Generation has completed even when the provider omitted its final artifact.
    // Do not falsely fail/refund a generated video; content reports artifact_gone.
  } else if (status === "FAILURE") {
    result.reason = "Huiqu H3 video generation failed";
  } else {
    const progress = Number(String(task.progress || "").replace(/%$/, ""));
    if (Number.isFinite(progress) && progress > 0 && progress < 100) result.progress = progress + "%";
  }
  return result;
}

export function listArtifacts(task) {
  const video = task.status === "SUCCESS" && videoArtifact(task.data);
  return video ? [{ key: "video", type: "video", mimeType: video.mimeType }] : [];
}

export function buildContentRequest(ctx) {
  if (ctx.artifactKey !== "video") throw new Error("artifact_not_found");
  const video = videoArtifact(ctx.data);
  if (!video) throw new Error("artifact_gone");
  // CDN retrieval never receives either the channel key or the caller's token.
  // The host still enforces task ownership, SSRF checks and redirect validation.
  return { url: video.url, method: ctx.clientRequest.method, credentialless: true };
}

export const protocols = {
  openai_video: {
    decodeRequest: function (ctx) {
      if (ctx.operation !== "create") throw new Error("unsupported Huiqu H3 operation");
      if (!ctx.body || !["json", "multipart"].includes(ctx.body.kind)) throw new Error("JSON or multipart body required");
      let req;
      if (ctx.body.kind === "json") {
        if (!ctx.body.value || typeof ctx.body.value !== "object" || Array.isArray(ctx.body.value)) throw new Error("JSON object required");
        req = Object.assign({}, ctx.body.value);
      } else {
        req = {};
        for (const name of Object.keys(ctx.body.fields || {})) {
          const values = ctx.body.fields[name];
          if (values.length !== 1) throw new Error(name + " must be provided once");
          req[name] = values[0];
        }
        for (const name of ["metadata", "images", "videos", "audios"]) {
          if (req[name] !== undefined) req[name] = JSON.parse(req[name]);
        }
      }
      const fileFields = [];
      for (const file of ctx.body.files || []) {
        if (!["input_reference", "image", "images"].includes(file.field)) throw new Error("unsupported image file field");
        if (fileFields.includes(file.field)) throw new Error("each image file field must be provided once");
        fileFields.push(file.field);
      }
      req.model = ctx.model;
      validateRequest(req, ctx.model);
      return {
        kind: "submit",
        model: ctx.model,
        action: req.input_reference || req.image || (req.images || []).length || (ctx.body.files || []).length ? "image_to_video" : "text_to_video",
        requestBody: req,
      };
    },
    render: function (ctx, task) {
      const video = task.status === "SUCCESS" && videoArtifact(task.data);
      return video ? { url: video.url, result_url: video.url, metadata: { url: video.url } } : {};
    },
  },
};
