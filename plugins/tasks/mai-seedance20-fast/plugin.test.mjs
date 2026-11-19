import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
const plugin = await import('data:text/javascript;base64,' + readFileSync(new URL('./plugin.js', import.meta.url)).toString('base64'));
const model = '全能视频2.0-fast-720';
const request = {model, prompt: 'Use @图片1 appearance and @视频1 motion', duration: 15, ratio: '16:9', resolution: '720p', images: ['https://media.example/ref.png'], videos: ['https://media.example/motion.mp4']};
const context = {model, upstreamModel: 'sd2.0fast', baseUrl: 'https://api.mai-token.com', apiKey: 'fixture-secret', requestBody: request};
const decoded = plugin.protocols.openai_video.decodeRequest({operation: 'create', model, body: {kind: 'json', value: request}});
const outgoing = plugin.buildSubmitRequest({...context, requestBody: decoded.requestBody});
assert.equal(outgoing.url, 'https://api.mai-token.com/api/v3/contents/generations/tasks');
assert.deepEqual(outgoing.body, {model: 'sd2.0fast', content: [{type: 'text', text: request.prompt}, {type: 'image_url', role: 'reference_image', image_url: {url: request.images[0]}}, {type: 'video_url', role: 'reference_video', video_url: {url: request.videos[0]}}], duration: 15, ratio: '16:9', resolution: '720p', watermark: false});
assert.deepEqual(plugin.extractUsage(context), {seconds: 15});
for (const patch of [{duration: 30}, {ratio: '9:16'}, {images: [request.images[0], request.images[0]]}, {audios: ['https://media.example/a.mp3']}, {videos: ['http://localhost/ref.mp4']}, {watermark: true}]) {
  assert.throws(() => plugin.buildSubmitRequest({...context, requestBody: {...request, ...patch}}));
}
const submitted = plugin.parseSubmitResponse(context, {statusCode: 200, body: {id: 'task-1'}});
assert.equal(submitted.taskId, 'task-1');
assert.deepEqual(plugin.parseTaskResult({...context, taskId: 'task-1'}, {id: 'task-1', status: 'succeeded', content: {video_url: 'https://cdn.example/artifact'}}), {status: 'SUCCESS', progress: '100%'});
assert.deepEqual(plugin.parseTaskResult({...context, taskId: 'task-1'}, {id: 'other', status: 'succeeded'}), {status: 'UNKNOWN'});
const download = plugin.buildContentRequest({...context, artifactKey: 'video', upstreamTaskId: 'task-1', clientRequest: {method: 'GET'}, data: {content: {video_url: 'https://cdn.example/artifact'}}});
assert.deepEqual(download, {url: 'https://cdn.example/artifact', method: 'GET', credentialless: true});
assert.equal(plugin.parseTaskResult({...context, taskId: 'task-1'}, {id: 'task-1', status: 'failed', error: {message: 'fixture-secret failure'}}).reason, '[redacted] failure');
console.log('MAI Fast: Ark mixed-reference wire, bounds, status, mapping and credentialless download passed');
