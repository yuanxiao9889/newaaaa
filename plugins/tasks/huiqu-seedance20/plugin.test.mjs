import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url), 'utf8');
const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const model = plugin.meta.models[0];
const base = { model, prompt: 'Ocean sunrise' };
const context = { model, upstreamModel: 'sd2.0-mx720p', baseUrl: 'https://api.bjhuiqu.net', apiKey: 'fixture-secret' };
function decode(body) {
  return plugin.protocols.openai_video.decodeRequest({ model, operation: 'create', body: { kind: 'json', value: body } });
}

test('shared aliases become the documented Huiqu fields without changing caller identity', () => {
  const input = { ...base, seconds: '15', aspect_ratio: '9:16', images: ['https://media.example/a.jpg'], videos: ['https://media.example/v.mp4'], audios: ['https://media.example/a.mp3'] };
  const before = structuredClone(input);
  const intent = decode(input);
  const request = plugin.buildSubmitRequest({ ...context, requestBody: intent.requestBody });
  assert.equal(intent.model, model);
  assert.equal(request.model, undefined);
  assert.equal(request.url, 'https://api.bjhuiqu.net/v1/videos/generations');
  assert.deepEqual(request.body, { model: 'sd2.0-mx720p', prompt: base.prompt, duration: 15, ratio: '9:16', resolution: '720p', images: input.images, video_urls: input.videos, audio_urls: input.audios });
  assert.deepEqual(input, before);
  assert.deepEqual(plugin.extractUsage({ ...context, requestBody: intent.requestBody }), { seconds: 15 });
  assert.throws(() => plugin.buildSubmitRequest({ ...context, upstreamModel: 'wrong-model', requestBody: intent.requestBody }));
  assert.equal(plugin.buildSubmitRequest({ ...context, baseUrl: context.baseUrl + '/v1/', requestBody: intent.requestBody }).url, request.url);
});

test('text-only documented duration and shared ratio defaults and media boundaries are accepted', () => {
  const body = decode(base).requestBody;
  assert.deepEqual(body, { ...base, duration: 10, ratio: '16:9', resolution: '720p' });
  assert.equal(decode(base).action, 'text_to_video');
  for (const seconds of [4, 6, 10, 15]) {
    const intent = decode({ ...base, seconds, images: Array(9).fill('https://media.example/i.jpg'), video_urls: Array(3).fill('https://media.example/v.mp4'), audio_urls: Array(3).fill('https://media.example/a.mp3') });
    assert.equal(intent.requestBody.duration, seconds);
  }
});

test('unsupported or conflicting inputs cannot bypass validation before routing or billing', () => {
  const invalid = [
    ...[0, -1, 3, 16, 3601, 1e30, NaN, Infinity, null, true, '18446744073686646784', 4.5].map(seconds => ({ seconds })),
    { seconds: 5, duration: 15 }, { aspect_ratio: '9:16', ratio: '16:9' },
    { images: Array(10).fill('https://media.example/i.jpg') }, { videos: Array(4).fill('https://media.example/v.mp4') },
    { audios: ['https://media.example/a.mp3'] }, { images: ['https://media.example/i.jpg'], audios: Array(4).fill('https://media.example/a.mp3') },
    { images: ['https://media.example/a.jpg'], image: 'https://media.example/b.jpg' },
    { images: ['data:image/png;base64,YQ=='] }, { images: ['http://127.0.0.1/a.jpg'] },
    { metadata: { seconds: 1e30 } }, { parameters: { duration: 1e30 } }, { n: 2 }, { stream: true },
    { ratio: '21:9' }, { resolution: '480p' }, { watermark: false }, { first_image: 'https://media.example/a.jpg' },
  ];
  for (const fields of invalid) {
    assert.throws(() => decode({ ...base, ...fields }), JSON.stringify(fields));
    assert.throws(() => plugin.extractUsage({ ...context, requestBody: { ...base, ...fields } }), JSON.stringify(fields));
    assert.throws(() => plugin.buildSubmitRequest({ ...context, requestBody: { ...base, ...fields } }), JSON.stringify(fields));
  }
});

test('submission and polling accept numeric seconds, enforce task identity and preserve terminal states', () => {
  const ctx = { ...context, requestBody: decode(base).requestBody, taskId: 'task-1' };
  assert.equal(plugin.parseSubmitResponse(ctx, { statusCode: 202, body: { id: 'task-1', task_id: 'task-1', seconds: 5, status: 'queued' } }).taskId, 'task-1');
  assert.throws(() => plugin.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'a', task_id: 'b' } }));
  assert.equal(plugin.buildQueryRequest(ctx).url, 'https://api.bjhuiqu.net/v1/videos/task-1');
  for (const [state, expected] of [['queued', 'QUEUED'], ['processing', 'IN_PROGRESS'], ['completed', 'SUCCESS'], ['succeeded', 'SUCCESS'], ['failed', 'FAILURE'], ['unknown', 'UNKNOWN']]) {
    assert.equal(plugin.parseTaskResult(ctx, { id: ctx.taskId, status: state }).status, expected);
  }
  assert.equal(plugin.parseTaskResult(ctx, { id: 'other', status: 'completed' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult(ctx, { id: ctx.taskId, status: 'completed' }, { status: 503 }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult(ctx, { id: ctx.taskId, status: 'failed', error: { message: 'failure fixture-secret' } }).reason, 'failure [redacted]');
});

test('public CDN downloads omit credentials and missing URLs use the documented authenticated content path', () => {
  const ctx = { ...context, upstreamTaskId: 'task-1', artifactKey: 'video', clientRequest: { method: 'HEAD' }, data: { metadata: { url: 'https://ivd.bjstlx.com/video_cache/task-1.mp4' } } };
  assert.deepEqual(plugin.buildContentRequest(ctx), { url: ctx.data.metadata.url, method: 'GET', credentialless: true });
  assert.deepEqual(plugin.buildContentRequest({ ...ctx, data: {} }), { url: 'https://api.bjhuiqu.net/v1/videos/task-1/content', method: 'HEAD', headers: { Authorization: 'Bearer fixture-secret' } });
  assert.equal(plugin.listArtifacts({ status: 'SUCCESS', data: {} }).length, 1);
  assert.deepEqual(plugin.listArtifacts({ status: 'IN_PROGRESS' }), []);
});
