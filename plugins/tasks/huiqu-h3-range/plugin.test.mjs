import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url), 'utf8');
const p = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const reference = 'https://media.example/image.png';
function context(model, seconds) {
  return { model, upstreamModel: model.includes('933') ? model : model.replace('-5-15s', '-930-5-15s'),
    baseUrl: 'https://api.bjhuiqu.net', apiKey: 'fixture', requestBody: { model, prompt: 'Ocean sunrise', seconds, images: [reference] } };
}
test('all six tiers preserve public identity, dynamic mapping and exact seconds billing', () => {
  for (const model of p.meta.models.slice(0, 6)) {
    for (const seconds of model.includes('933') ? [10, 11, 12, 13, 14, 15] : [5, 10, 12, 15]) {
      const ctx = context(model, seconds);
      const intent = p.protocols.openai_video.decodeRequest({ model, operation: 'create', body: { kind: 'json', value: ctx.requestBody } });
      assert.equal(intent.model, model);
      ctx.requestBody = intent.requestBody;
      const request = p.buildSubmitRequest(ctx);
      assert.equal(request.url, 'https://api.bjhuiqu.net/v1/videos');
      assert.equal(request.body.model, ctx.upstreamModel);
      assert.equal(request.body.seconds, seconds);
      for (const usagePurpose of ['facts', 'billing_ratios']) assert.deepEqual(p.extractUsage({ ...ctx, usagePurpose }), { seconds });
      const price = model.includes('768p') ? .15 : model.includes('1080p') ? .2 : .24;
      assert.equal(Math.round(p.extractUsage(ctx).seconds * price * 100), Math.round(seconds * price * 100));
      const parsed = p.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'upstream-1', status: 'queued' } });
      assert.equal(parsed.taskData.seconds, String(seconds));
      assert.equal(p.buildQueryRequest({ ...ctx, taskId: parsed.taskId }).url, 'https://api.bjhuiqu.net/v1/videos/upstream-1');
    }
  }
});
test('933 media layout, fixed alias and invalid durations protect the request and billing boundaries', () => {
  const ctx = context(p.meta.models[4], 12);
  ctx.requestBody.metadata = { video_urls: ['https://media.example/a.mp4'], audio_urls: ['https://media.example/a.mp3'] };
  assert.deepEqual(p.buildSubmitRequest(ctx).body.metadata, ctx.requestBody.metadata);
  const legacy = context(p.meta.models[6], 15);
  assert.equal(p.buildSubmitRequest(legacy).body.seconds, undefined);
  assert.deepEqual(p.extractUsage(legacy), { seconds: 15 });
  for (const value of [-1, 0, 9, 16, 12.5, NaN, Infinity, true, '18446744073686646784']) {
    const bad = context(p.meta.models[4], value);
    assert.throws(() => p.extractUsage(bad));
    assert.throws(() => p.buildSubmitRequest(bad));
  }
  assert.throws(() => p.buildSubmitRequest({ ...ctx, requestBody: { ...ctx.requestBody, duration: 15 } }));
  assert.throws(() => p.buildSubmitRequest({ ...ctx, requestBody: { ...ctx.requestBody, metadata: { seconds: 99999 } } }));
  assert.throws(() => p.buildSubmitRequest(context(p.meta.models[6], 10)));
  const noImage = context(p.meta.models[3], 10); noImage.requestBody.images = [];
  assert.throws(() => p.buildSubmitRequest(noImage));
  const noVideo = context(p.meta.models[0], 10); noVideo.requestBody.video_urls = ['https://media.example/a.mp4'];
  assert.throws(() => p.buildSubmitRequest(noVideo));
});
test('polling checks task identity and media download never forwards keys to a CDN', () => {
  const ctx = { ...context(p.meta.models[0], 10), taskId: 'task-1' };
  assert.equal(p.parseTaskResult(ctx, { id: 'task-1', status: 'completed' }).status, 'SUCCESS');
  assert.equal(p.parseTaskResult(ctx, { id: 'other', status: 'completed' }).status, 'UNKNOWN');
  assert.equal(p.parseTaskResult(ctx, { id: 'task-1', status: 'failed', error: { message: 'failure fixture' } }).reason, 'failure [redacted]');
  const content = { ...ctx, upstreamTaskId: 'task-1', artifactKey: 'video', clientRequest: { method: 'GET' }, data: { metadata: { url: 'https://cdn.example/result.mp4' } } };
  assert.deepEqual(p.buildContentRequest(content), { url: 'https://cdn.example/result.mp4', method: 'GET', credentialless: true });
  globalThis.utils = { fetch: () => ({ status: 200 }) };
  try { assert.equal(p.buildContentRequest({ ...content, data: {} }).url, 'https://api.bjhuiqu.net/v1/videos/task-1/content'); }
  finally { delete globalThis.utils; }
});

test('authenticated content redirects become credentialless downloads without changing the task', () => {
  const ctx = { ...context(p.meta.models[3], 10), upstreamTaskId: 'task-933', artifactKey: 'video', clientRequest: { method: 'GET' }, data: {} };
  let location = 'https://ivd.bjstlx.com/v1/videos/task-933/content';
  let status = 302;
  globalThis.utils = { fetch(request) {
    assert.equal(request.method, 'HEAD');
    assert.equal(request.url, 'https://api.bjhuiqu.net/v1/videos/task-933/content');
    assert.deepEqual(request.headers, { Authorization: 'Bearer fixture' });
    return { status, headers: { LoCaTiOn: location } };
  } };
  try {
    for (status of [301, 302, 303, 307, 308]) {
      assert.deepEqual(p.buildContentRequest(ctx), { url: location, method: 'GET', credentialless: true });
      assert.equal(p.buildContentRequest({ ...ctx, clientRequest: { method: 'HEAD' } }).method, 'GET');
    }
    for (location of ['', 'http://cdn.example/file', 'https://127.0.0.1/file', 'https://a.internal/file', 'https://user:pass@cdn.example/file', 'https://cdn.example/file#x', 'https://cdn.example/file?key=fixture']) {
      assert.throws(() => p.buildContentRequest(ctx), /Invalid public artifact redirect/);
    }
    status = 200;
    assert.deepEqual(p.buildContentRequest(ctx).headers, { Authorization: 'Bearer fixture' });
  } finally { delete globalThis.utils; }
});
