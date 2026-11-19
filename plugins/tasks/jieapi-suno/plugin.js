// JieAPI Suno native contract: https://jieapi.apifox.cn/485644081e0
const VERSIONS = ["chirp-v3.0", "chirp-v3.5", "chirp-v4", "chirp-auk", "chirp-bluejay", "chirp-crow"];
export const meta = {
  apiVersion: 1, key: "jieapi-suno", name: "JieAPI Suno", version: "1.0.0",
  author: { name: "OOpii" }, baseUrl: "https://jieapi.com", auth: "api_key", icon: "text:音乐",
  models: ["suno_music", "suno_lyrics"], fetchMode: "per_task",
  description: { en: "Suno music and lyrics through JieAPI", zh: "通过 JieAPI 生成 Suno 音乐与歌词" },
  routes: [
    { method: "POST", path: "/jieapi-suno/submit/:action", type: "submit", decode: "submit", render: "created" },
    { method: "GET", path: "/jieapi-suno/fetch/:task_id", type: "query", render: "task" },
  ],
  usageSchema: { clips: { type: "number", unit: "count", description: { en: "Song or lyrics generation unit price", zh: "生成歌曲或歌词单价" } } },
  usageExamples: [{ label: "Music", facts: { clips: 2 } }],
};

function normalize(action, input) {
  if (!input || typeof input !== "object" || Array.isArray(input)) throw new Error("JSON body required");
  const body = {};
  for (const field of ["prompt", "tags", "title", "gpt_description_prompt"]) {
    if (input[field] !== undefined) {
      if (typeof input[field] !== "string" || input[field].length > 20000) throw new Error("invalid " + field);
      body[field] = input[field].trim();
    }
  }
  if (action === "lyrics") {
    if (!body.prompt) throw new Error("prompt required");
    return { prompt: body.prompt };
  }
  if (action !== "music") throw new Error("invalid action");
  if (!body.prompt && !body.gpt_description_prompt && !(input.make_instrumental === true && body.tags)) throw new Error("prompt required");
  if (!VERSIONS.includes(input.mv)) throw new Error("invalid Suno version");
  if (input.make_instrumental !== undefined && typeof input.make_instrumental !== "boolean") throw new Error("invalid instrumental flag");
  body.mv = input.mv;
  body.make_instrumental = input.make_instrumental === true;
  return body;
}

export function buildSubmitRequest(ctx) {
  const action = String(ctx.action || "music").toLowerCase();
  return { url: ctx.baseUrl + "/suno/submit/" + action, method: "POST",
    headers: { Authorization: "Bearer " + ctx.apiKey, "Content-Type": "application/json" },
    body: normalize(action, ctx.requestBody), action: action };
}
export function parseSubmitResponse(ctx, resp) {
  const body = resp.body || {};
  if (body.code !== "success" || typeof body.data !== "string" || !/^[A-Za-z0-9_-]{1,256}$/.test(body.data)) throw new Error("Suno submission not acknowledged");
  return { taskId: body.data };
}
export function buildQueryRequest(ctx) {
  if (!/^[A-Za-z0-9_-]{1,256}$/.test(ctx.taskId)) throw new Error("invalid task id");
  return { url: ctx.baseUrl + "/suno/fetch/" + ctx.taskId, method: "GET", headers: { Authorization: "Bearer " + ctx.apiKey } };
}
export function parseTaskResult(ctx, body) {
  if (!body || body.code !== "success" || !body.data || body.data.task_id !== ctx.taskId) throw new Error("invalid Suno task response");
  const task = body.data;
  if (!["NOT_START", "SUBMITTED", "QUEUED", "IN_PROGRESS", "SUCCESS", "FAILURE"].includes(task.status)) throw new Error("unknown Suno status");
  if (task.status === "SUCCESS" && String(ctx.action).toLowerCase() !== "lyrics" &&
      (!Array.isArray(task.data) || !task.data.length || !task.data.every(song => song && song.id && song.audio_url))) {
    throw new Error("Suno music result is incomplete");
  }
  return { status: task.status, reason: task.status === "FAILURE" ? "Suno generation failed" : "",
    progress: task.progress, data: task.data, finishTime: task.finish_time || 0 };
}
export function extractUsage(ctx) {
  if (ctx.usagePurpose === "billing_ratios") return null;
  return { clips: String(ctx.action).toLowerCase() === "lyrics" ? 1 : 2 };
}
export function extractUsageOnComplete(task, result, body) {
  return { clips: Array.isArray(body) ? body.length : 1 };
}
function songs(task) { return Array.isArray(task.data) ? task.data : []; }
function key(song) { return "audio-" + utils.hmacSHA256(String(song.id), "oopii:jieapi-suno:audio"); }
export function listArtifacts(task) {
  if (task.status !== "SUCCESS") return [];
  return songs(task).filter(song => song && song.id && song.audio_url).map(song => ({ key: key(song), type: "audio", mimeType: "audio/mpeg" }));
}
export function buildContentRequest(ctx) {
  const song = songs(ctx).find(song => song && song.id && key(song) === ctx.artifactKey);
  if (!song || !/^https:\/\//.test(song.audio_url)) throw new Error("artifact_not_found");
  return { url: song.audio_url, method: ctx.clientRequest.method, credentialless: true };
}
export const native = {
  submit(ctx) {
    const action = String((ctx.params || {}).action || "").toLowerCase();
    if (!ctx.body || ctx.body.kind !== "json") throw new Error("JSON body required");
    return { kind: "submit", model: action === "lyrics" ? "suno_lyrics" : "suno_music", action,
      requestBody: normalize(action, ctx.body.value) };
  },
  created(ctx, task) { return { code: "success", data: task.task_id }; },
  task(ctx, task) {
    return { code: "success", data: { task_id: task.task_id, status: task.status, progress: task.progress,
      fail_reason: task.status === "FAILURE" ? "Suno generation failed" : "",
      data: task.status === "SUCCESS" && !Array.isArray(task.data) ? task.data : null } };
  },
};
