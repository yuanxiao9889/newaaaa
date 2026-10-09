import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
const source = readFileSync(new URL('./plugin.js', import.meta.url), 'utf8');
const plugin = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
for (const [model, upstream, durations] of [
  ['全能视频2.0-933-720-tem', 'sd2.0-mx720p', [5, 10, 15]],
  ['全能视频2.5-301010-720-tem', 'sd2.5-mx720p', [30]],
]) {
  for (const duration of durations) {
    const body = { model, duration, prompt: 'A landscape', ratio: '16:9', resolution: '720p', images: ['https://media.example/image.jpg'] };
    const intent = plugin.protocols.openai_video.decodeRequest({ operation: 'create', model, body: { kind: 'json', value: body } });
    const ctx = { model, upstreamModel: upstream, requestBody: intent.requestBody, apiKey: 'fixture-secret', baseUrl: 'https://api.bjhuiqu.net' };
    const request = plugin.buildSubmitRequest(ctx);
    assert.equal(request.url, 'https://api.bjhuiqu.net/v1/videos/generations');
    assert.deepEqual(request.body, { ...body, model: upstream });
    assert.deepEqual(plugin.extractUsage(ctx), {});
    const parsed = plugin.parseSubmitResponse(ctx, { statusCode: 202, body: { task_id: 'task-a', status: 'queued' } });
    assert.equal(parsed.taskData.seconds, String(duration));
    assert.equal(plugin.buildQueryRequest({ ...ctx, taskId: 'task-a' }).url, 'https://api.bjhuiqu.net/v1/videos/task-a');
    for (const invalid of [{ images: [] }, { images: [...body.images, ...body.images] }, { duration: 6 }, { resolution: '1080p' }, { videos: ['https://media.example/video.mp4'] }, { images: ['http://localhost/a.jpg'] }, { seconds: 4 }]) {
      assert.throws(() => plugin.buildSubmitRequest({ ...ctx, requestBody: { ...body, ...invalid } }));
    }
  }
}
const task = { id: 'task-a', status: 'completed', metadata: { url: 'https://cdn.example/final.mp4' } };
assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, task).url, task.metadata.url);
assert.equal(plugin.parseTaskResult({ taskId: 'wrong' }, task).status, 'UNKNOWN');
assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { ...task, status: 'unknown' }).status, 'UNKNOWN');
assert.equal(plugin.parseTaskResult({ taskId: 'task-a' }, { ...task, metadata: {} }).status, 'IN_PROGRESS');
assert.equal(plugin.parseTaskResult({ taskId: 'task-a', apiKey: 'secret' }, { ...task, status: 'failed', error: { message: 'failed secret' } }).reason, 'failed [redacted]');
const content = plugin.buildContentRequest({ data: task, artifactKey: 'video', clientRequest: { method: 'GET' } });
assert.equal(content.url, task.metadata.url);
assert.equal(content.credentialless, true);
assert.equal(content.headers, undefined);
assert.equal(plugin.protocols.openai_video.render({}, { status: 'SUCCESS', data: task }).video_url, task.metadata.url);
console.log('Huiqu MX: both model mappings, duration bounds, reference validation, polling, results and credentialless download passed');
