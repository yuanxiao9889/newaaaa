import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

const source = readFileSync(new URL('./plugin.js', import.meta.url), 'utf8');
const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const model = '全能视频2.5-301010-c';
const base = { model, upstreamModel: 'seedance2.5-ae-720p', baseUrl: 'https://www.aicost.me', apiKey: 'fixture-secret' };
const request = { model, prompt: 'A landscape', duration: 5, ratio: '16:9', images: ['https://media.example/image.jpg'] };
function decode(value, kind = 'json') {
  return plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: kind === 'json' ? { kind, value } : { kind, fields: value } }).requestBody;
}

test('shared model and client aliases produce the documented AICost request and one duration multiplier', () => {
  for (const input of [request, { model, prompt: request.prompt, seconds: '5', aspect_ratio: '16:9', image_url: request.images[0] }]) {
    const snapshot = structuredClone(input);
    const ctx = { ...base, requestBody: decode(input) };
    const descriptor = plugin.buildSubmitRequest(ctx);
    assert.equal(descriptor.url, 'https://www.aicost.me/v1/videos');
    assert.equal(descriptor.model, undefined);
    assert.deepEqual(descriptor.body, { model: base.upstreamModel, prompt: request.prompt, seconds: 5, aspect_ratio: '16:9', resolution: '720p', images: request.images });
    assert.deepEqual(plugin.extractUsage(ctx), { seconds: 5 });
    assert.deepEqual(input, snapshot);
  }
  assert.throws(() => plugin.buildSubmitRequest({ ...base, upstreamModel: 'other-model', requestBody: decode(request) }), /mapping/);
});

test('duration and media bounds reject unsafe or conflicting values before billing', () => {
  for (const seconds of [0, -1, 31, 1.5, 18446744073686646784, Infinity, NaN, '', false, '1e20', '18446744073686646784']) {
    assert.throws(() => decode({ model, prompt: 'test', seconds }));
    assert.throws(() => plugin.extractUsage({ ...base, requestBody: { model, prompt: 'test', seconds } }));
  }
  for (const seconds of [1, 5, 30, '30']) assert.equal(decode({ model, prompt: 'test', seconds }).seconds, Number(seconds));
  for (const patch of [
    { seconds: 6 }, { aspect_ratio: '9:16' }, { resolution: '1080p' }, { n: 2 }, { stream: true },
    { images: ['file:///private/image.png'] }, { audios: ['data:image/png;base64,AAAA'] },
    { images: Array.from({ length: 31 }, (_, i) => `https://media.example/${i}.png`) },
    { videos: Array.from({ length: 11 }, (_, i) => `https://media.example/${i}.mp4`) },
    { metadata: { duration: 18446744073686646784 } },
  ]) assert.throws(() => decode({ ...request, ...patch }));
  assert.equal(decode({ ...request, image_url: request.images[0] }).images.length, 1);
  assert.equal(decode({ model, prompt: 'test', size: '720x1280' }).aspect_ratio, '9:16');
});

test('multipart keeps opaque upload references and enforces combined counts', () => {
  const body = decode({ model: [model], prompt: ['test'], seconds: ['5'], images: ['https://media.example/a.png'] }, 'multipart');
  const files = [{ field: 'input_reference', ref: 'request_file:input_reference', filename: 'a.png' }];
  const descriptor = plugin.buildSubmitRequest({ ...base, requestBody: body, files });
  assert.equal(descriptor.bodyType, 'multipart');
  assert.equal(descriptor.headers['Content-Type'], undefined);
  assert.deepEqual(descriptor.parts.at(-1), { name: 'images', fileRef: files[0].ref, filename: 'a.png' });
  assert.ok(descriptor.parts.some(p => p.name === 'model' && p.value === base.upstreamModel));
  assert.throws(() => decode({ model: [model], prompt: ['test'], seconds: ['5', '30'] }, 'multipart'));
  assert.throws(() => plugin.buildSubmitRequest({ ...base, requestBody: body, files: Array(30).fill(files[0]) }), /Too many images/);
});

test('submission and polling accept documented states and reject unrelated tasks', () => {
  const ctx = { ...base, requestBody: decode(request) };
  const parsed = plugin.parseSubmitResponse(ctx, { statusCode: 202, body: { id: 'task-a', status: 'queued' } });
  assert.equal(parsed.taskId, 'task-a');
  assert.equal(parsed.taskData.seconds, '5');
  assert.equal(plugin.buildQueryRequest({ ...base, taskId: parsed.taskId }).url, 'https://www.aicost.me/v1/videos/task-a');
  for (const [status, expected] of [['NOT_START', 'NOT_START'], ['queued', 'QUEUED'], ['IN_PROGRESS', 'IN_PROGRESS'], ['processing', 'IN_PROGRESS'], ['running', 'IN_PROGRESS'], ['SUCCESS', 'SUCCESS'], ['succeeded', 'SUCCESS'], ['completed', 'SUCCESS'], ['FAILURE', 'FAILURE'], ['failed', 'FAILURE'], ['unknown', 'UNKNOWN']]) {
    assert.equal(plugin.parseTaskResult({ ...base, taskId: 'task-a' }, { id: 'task-a', status }).status, expected);
  }
  assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { id: 'task-b', status: 'SUCCESS' }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { id: 'task-a', status: 'SUCCESS' }, { status: 500 }).status, 'UNKNOWN');
  assert.equal(plugin.parseTaskResult({ ...base, taskId: 'task-a' }, { id: 'task-a', status: 'failed', error: { message: 'failed fixture-secret' } }).reason, 'failed [redacted]');
  assert.throws(() => plugin.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'a', task_id: 'b' } }), /Conflicting/);
  assert.throws(() => plugin.buildQueryRequest({ ...base, taskId: '../other' }));
});

test('downloads keep credentials on the channel origin and public responses omit upstream identifiers', () => {
  const content = plugin.buildContentRequest({ ...base, artifactKey: 'video', upstreamTaskId: 'task-a', clientRequest: { method: 'GET' }, data: { result_url: 'https://untrusted.example/a.mp4' } });
  assert.equal(content.url, 'https://www.aicost.me/v1/videos/task-a/content');
  assert.equal(content.headers.Authorization, 'Bearer fixture-secret');
  assert.deepEqual(plugin.listArtifacts({ status: 'SUCCESS' }), [{ key: 'video', type: 'video', mimeType: 'video/mp4' }]);
  assert.deepEqual(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: { id: 'private', video_url: 'https://upstream.example/private' } }), {});
  assert.throws(() => plugin.buildContentRequest({ ...base, artifactKey: 'other' }));
});
