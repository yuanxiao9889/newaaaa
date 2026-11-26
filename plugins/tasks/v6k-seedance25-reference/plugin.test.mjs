import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url));
const plugin = await import('data:text/javascript;base64,' + source.toString('base64'));
const model = '全能视频2.5-301010-720-聚合';
const channel = { model, upstreamModel: '特价按条seadanco2.5-720', baseUrl: 'https://www.v6k.top', apiKey: 'fixture' };

function decode(refs) {
  const value = { model, prompt: 'Follow the movement in the reference', ...refs };
  return plugin.protocols.openai_video.decodeRequest({ model, operation: 'create', body: { kind: 'json', value } });
}

test('video-only requests are reference tasks and forward URLs in order under the mapped model', () => {
  const videos = ['https://media.example/first.mp4', 'https://media.example/second.mp4'];
  for (const refs of [{ videos }, { reference_video_urls: videos }, { reference_video: videos[0] }]) {
    const intent = decode(refs);
    assert.equal(intent.action, 'image_to_video');
    const ctx = { ...channel, requestBody: intent.requestBody };
    const expected = refs.reference_video ? [videos[0]] : videos;
    assert.deepEqual(plugin.buildSubmitRequest(ctx).body, {
      model: channel.upstreamModel, prompt: intent.requestBody.prompt, seconds: 5, ratio: '16:9', resolution: '720p', videos: expected,
    });
    assert.deepEqual(plugin.extractUsage(ctx), { seconds: 5 });
  }
  assert.equal(decode({ videos: [] }).action, 'text_to_video');
});

test('video references retain URL, count and alias validation', () => {
  const url = 'https://media.example/ref.mp4';
  assert.equal(decode({ videos: Array(10).fill(url) }).requestBody.videos.length, 10);
  assert.throws(() => decode({ videos: Array(11).fill(url) }), /at most 10/);
  assert.throws(() => decode({ videos: ['file:///ref.mp4'] }), /public HTTP/);
  assert.throws(() => decode({ videos: [url], reference_video: url + '?other' }), /Conflicting/);
});
