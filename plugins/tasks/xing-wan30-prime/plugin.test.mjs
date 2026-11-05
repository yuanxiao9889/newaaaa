import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const source = readFileSync(new URL('./plugin.js', import.meta.url), 'utf8');
const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const model = 'wan3.0-video';
const base = { model, upstreamModel: 'wan3.0-prime-720P', baseUrl: 'https://xingapi.top', apiKey: 'fixture-secret' };
const request = { model, prompt: 'A landscape', duration: 5, ratio: '16:9', resolution: '720P', images: ['https://media.example/a.png'] };
function decode(value) {
  return plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value } }).requestBody;
}

test('shared model maps to XingAPI with compatible pricing facts and client aliases', () => {
  for (const input of [request, { model, prompt: request.prompt, seconds: '5', aspect_ratio: '16:9', images: request.images }]) {
    const snapshot = structuredClone(input);
    const ctx = { ...base, requestBody: decode(input) };
    const descriptor = plugin.buildSubmitRequest(ctx);
    assert.equal(descriptor.url, 'https://xingapi.top/v1/videos/generations');
    assert.deepEqual(descriptor.body, { ...request, model: base.upstreamModel, resolution: '720p' });
    assert.deepEqual(plugin.extractUsage(ctx), { seconds: 5, resolution: '720P' });
    assert.deepEqual(plugin.extractUsage({ ...ctx, usagePurpose: 'billing_ratios' }), { seconds: 5 });
    assert.deepEqual(input, snapshot);
    assert.equal(plugin.buildSubmitRequest({ ...ctx, baseUrl: 'https://xingapi.top/v1/' }).url, descriptor.url);
  }
  assert.throws(() => plugin.buildSubmitRequest({ ...base, upstreamModel: 'wrong-model', requestBody: decode(request) }), /mapping/);
});

test('candidate validation skips unsupported requests and bounds all billing inputs', () => {
  for (const duration of [undefined, false, 0, -1, 4, 31, 1.5, 18446744073686646784, '1e20', Infinity, NaN]) {
    assert.throws(() => decode({ ...request, duration }), /integer from 5 to 30/);
  }
  assert.throws(() => plugin.extractUsage({ ...base, requestBody: { ...request, duration: 18446744073686646784 } }), /integer from 5 to 30/);
  for (const duration of [5, '5', 30]) assert.equal(decode({ ...request, duration }).duration, Number(duration));
  for (const patch of [
    { seconds: 6 }, { aspect_ratio: '9:16' }, { resolution: '480p' }, { resolution: '1080p' },
    { auto_duration: true }, { n: 2 }, { metadata: { duration: 1e20 } },
    { images: ['file:///private/a.png'] }, { images: ['http://127.0.0.1/a.png'] },
    { images: ['data:image/png;base64,AAAA'] }, { images: ['https://user:password@host.example/a.png'] },
    { images: Array(11).fill(request.images[0]) }, { videos: Array(6).fill('https://media.example/a.mp4') },
    { audios: Array(6).fill('https://media.example/a.mp3') },
  ]) assert.throws(() => decode({ ...request, ...patch }));
  assert.throws(() => plugin.buildSubmitRequest({ ...base, requestBody: decode(request), files: [{ ref: 'request_file:image' }] }), /Publish reference/);
});

test('create and poll normalize documented statuses without trusting unrelated or unknown results', () => {
  const ctx = { ...base, requestBody: decode(request) };
  for (const field of ['id', 'task_id']) {
    const result = plugin.parseSubmitResponse(ctx, { statusCode: 202, body: { [field]: 'task-a', status: 'queued' } });
    assert.equal(result.taskId, 'task-a');
    assert.equal(result.taskData.seconds, '5');
    assert.equal(plugin.buildQueryRequest({ ...base, taskId: result.taskId }).url, 'https://xingapi.top/v1/videos/task-a');
  }
  const statuses = {
    QUEUED: ['queued', 'pending'], IN_PROGRESS: ['running', 'processing', 'in_progress'],
    SUCCESS: ['success', 'completed', 'succeeded', 'done', 'finished'],
    FAILURE: ['failed', 'error', 'cancelled', 'canceled'], UNKNOWN: ['unknown'],
  };
  for (const [expected, values] of Object.entries(statuses)) {
    for (const status of values) assert.equal(plugin.parseTaskResult({ ...base, taskId: 'task-a' }, { id: 'task-a', status }).status, expected);
  }
  assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { id: 'task-b', status: 'success' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { id: 'task-a', status: 'success' }, { status: 500 }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({ ...base, taskId: 'task-a' }, { id: 'task-a', status: 'failed', error: { message: 'bad fixture-secret' } }).reason, 'bad [redacted]');
  assert.throws(() => plugin.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'a', task_id: 'b' } }), /Conflicting/);
  assert.throws(() => plugin.parseSubmitResponse(ctx, { statusCode: 200, body: {} }), /Invalid upstream task ID/);
});

test('downloads use protected content or credentialless CDN and hide upstream identifiers', () => {
  const ctx = { ...base, artifactKey: 'video', upstreamTaskId: 'task-a', clientRequest: { method: 'GET' } };
  assert.deepEqual(plugin.buildContentRequest(ctx), {
    url: 'https://xingapi.top/v1/videos/task-a/content', method: 'GET', headers: { Authorization: 'Bearer fixture-secret' },
  });
  assert.deepEqual(plugin.buildContentRequest({ ...ctx, data: { video_url: 'https://cdn.example/result.mp4' } }), {
    url: 'https://cdn.example/result.mp4', method: 'GET', credentialless: true,
  });
  assert.throws(() => plugin.buildContentRequest({ ...ctx, upstreamTaskId: '../other' }), /Invalid upstream task ID/);
  assert.deepEqual(plugin.listArtifacts({ status: 'IN_PROGRESS' }), []);
  assert.deepEqual(plugin.listArtifacts({ status: 'SUCCESS' }), [{ key: 'video', type: 'video', mimeType: 'video/mp4' }]);
  assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: { id: 'private', video_url: 'https://upstream.example/private.mp4' } }), {});
});
