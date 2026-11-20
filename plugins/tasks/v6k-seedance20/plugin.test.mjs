import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url));
const plugin = await import('data:text/javascript;base64,' + source.toString('base64'));
const model = '全能视频2.0-903-720-c';
const channel = { model, upstreamModel: '官方按条SD2.0', baseUrl: 'https://www.v6k.top', apiKey: 'fixture' };
const input = {
  model, prompt: 'Follow @Image2 in the rhythm of @Audio1.', duration: 11, resolution: '720p', ratio: '3:4',
  images: ['https://media.example/one.jpg', 'https://media.example/two.jpg'], audios: ['https://media.example/one.mp3'],
};
function decode(value) {
  return plugin.protocols.openai_video.decodeRequest({ model, operation: 'create', body: { kind: 'json', value } });
}

test('client reference order, prompt and non-default duration survive channel mapping', () => {
  const intent = decode(input);
  assert.equal(intent.model, model);
  assert.equal(intent.action, 'image_to_video');
  const ctx = { ...channel, requestBody: intent.requestBody };
  const { duration, ...fields } = input;
  assert.deepEqual(plugin.buildSubmitRequest(ctx), {
    method: 'POST', url: channel.baseUrl + '/v1/videos',
    headers: { Authorization: 'Bearer fixture', 'Content-Type': 'application/json' },
    body: { ...fields, model: channel.upstreamModel, seconds: duration },
  });
  assert.deepEqual(plugin.extractUsage(ctx), { seconds: 11 });
});

test('903 enforces the public duration, resolution and reference limits before submission', () => {
  for (const duration of [4, 5, 15]) {
    assert.equal(decode({ ...input, duration, images: Array(9).fill(input.images[0]), audios: Array(3).fill(input.audios[0]) }).requestBody.duration, duration);
  }
  for (const extra of [{ seconds: 8 }, { duration: 16 }, { duration: 3 }, { videos: ['https://media.example/ref.mp4'] },
    { start_frame: input.images }, { images: Array(10).fill(input.images[0]) }, { audios: Array(4).fill(input.audios[0]) },
    { resolution: '1080p' }, { aspect_ratio: '9:16' }]) {
    assert.throws(() => decode({ ...input, ...extra }));
  }
});

test('polling and authenticated content use the accepted upstream task ID', (t) => {
  globalThis.utils = { fetch: () => ({ status: 206, headers: {} }) };
  t.after(() => { delete globalThis.utils; });
  const ctx = { ...channel, requestBody: decode(input).requestBody };
  const accepted = plugin.parseSubmitResponse(ctx, { statusCode: 200, body: { id: 'task_v6k', status: 'queued' } });
  assert.equal(accepted.taskId, 'task_v6k');
  const query = { ...ctx, taskId: accepted.taskId };
  assert.equal(plugin.buildQueryRequest(query).url, channel.baseUrl + '/v1/videos/task_v6k');
  assert.deepEqual(plugin.parseTaskResult(query, { id: 'other', status: 'completed' }), { status: 'UNKNOWN' });
  assert.equal(plugin.parseTaskResult(query, { id: accepted.taskId, status: 'completed' }).status, 'SUCCESS');
  assert.equal(plugin.parseTaskResult(query, { id: accepted.taskId, status: 'failed', error: { message: 'bad reference' } }).reason, 'bad reference');
  for (const method of ['GET', 'HEAD']) {
    assert.deepEqual(plugin.buildContentRequest({ ...ctx, upstreamTaskId: accepted.taskId, artifactKey: 'video', clientRequest: { method } }),
      { method, url: channel.baseUrl + '/v1/videos/task_v6k/content', headers: { Authorization: 'Bearer fixture' } });
  }
});
