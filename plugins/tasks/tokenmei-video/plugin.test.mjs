import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const bytes = await readFile(new URL('./plugin.js', import.meta.url));
const api = await import('data:text/javascript;base64,' + bytes.toString('base64'));
const model = '全能视频2.5-301010-sss';
const ctx = { model, baseUrl: 'https://api.example/v1/', apiKey: 'test-channel-key' };
const request = { model, prompt: 'A scene', seconds: '5', ratio: '9:16' };
const decode = data => api.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value: data } });

for (const resolution of ['480p', '720p', '1080p']) {
  test(resolution + ' uses the currently mapped ID and exact output usage', () => {
    const { requestBody } = decode({ ...request, resolution, images: ['https://media.example/i'],
      videos: ['https://media.example/v'], audios: ['https://media.example/a'] });
    for (const upstreamModel of ['original-id', 'renamed-model']) {
      const built = api.buildSubmitRequest({ ...ctx, upstreamModel, requestBody });
      assert.equal(built.url, 'https://api.example/v1/videos');
      assert.deepEqual(built.body, { model: upstreamModel, prompt: request.prompt, duration: 5, resolution, ratio: '9:16',
        reference_images: ['https://media.example/i'], reference_videos: ['https://media.example/v'], reference_audio: ['https://media.example/a'] });
      assert.deepEqual(api.extractUsage({ ...ctx, requestBody }), { seconds: 5, resolution });
    }
  });
}
test('frame roles survive dispatch and invalid requests fail before submission', () => {
  const url = 'https://media.example/frame';
  const { requestBody } = decode({ ...request, start_frame: [url], end_frame: [url + '2'] });
  assert.equal(requestBody.first_frame, url);
  assert.equal(requestBody.last_frame, url + '2');
  for (const patch of [{ resolution: '4k' }, { duration: 4 }, { duration: 31 }, { duration: 5.5 },
    { images: Array(31).fill(url) }, { start_frame: [url], videos: [url] }, { end_frame: [url] }, { model: 'other' }]) {
    assert.throws(() => decode({ ...request, ...patch }));
  }
});
test('status requires the same task and a deliverable URL; failure is redacted', () => {
  const id = 'video_fixture', context = { ...ctx, taskId: id };
  const poll = patch => api.parseTaskResult(context, { id, ...patch }, { status: 200 });
  assert.equal(api.buildQueryRequest(context).url, 'https://api.example/v1/tasks/' + id);
  assert.equal(poll({ status: 'in_progress', progress: 85 }).progress, '85%');
  assert.equal(poll({ status: 'completed' }).status, 'UNKNOWN');
  assert.equal(poll({ id: 'video_other', status: 'completed', url: 'https://cdn.example/v' }).status, 'UNKNOWN');
  assert.equal(poll({ status: 'completed', url: 'https://cdn.example/v' }).status, 'SUCCESS');
  assert.deepEqual(poll({ status: 'failed', error: ctx.apiKey }), { status: 'FAILURE', reason: '[redacted]' });
});
