import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';

const source = await readFile(new URL('./plugin.js', import.meta.url));
const plugin = await import('data:text/javascript;base64,' + source.toString('base64'));
const channel = { model: 'magi2-video', upstreamModel: 'SK-magi2-7', apiKey: 'fixture', baseUrl: 'https://shuikule.top' };
const media = (kind, count) => Array.from({ length: count }, (_, index) => 'https://media.example/' + kind + index);

function decode(params) {
  const value = { model: channel.model, prompt: 'Follow the references', ...params };
  return plugin.protocols.openai_video.decodeRequest({ operation: 'create', model: channel.model, body: { kind: 'json', value } });
}

test('duration never enters the billing facts and all references reach workbench metadata in order', () => {
  const images = media('image', 10), videos = media('video', 5), audios = media('audio', 3);
  for (const resolution of ['480p', '720p', '1080p']) {
    for (const duration of [4, 30]) {
      const { requestBody } = decode({ resolution, duration, ratio: '21:9', images, videos, audios });
      const ctx = { ...channel, requestBody };
      assert.deepEqual(plugin.extractUsage(ctx), { resolution, video_count: 1 });
      assert.deepEqual(plugin.buildSubmitRequest(ctx).body, {
        model: channel.upstreamModel, prompt: requestBody.prompt, seconds: duration,
        metadata: {
          __workbench_passthrough: { aspect_ratio: '21:9', resolution },
          __workbench_refs: { reference_image_urls: images, reference_video_urls: videos, reference_audio_urls: audios },
        },
      });
    }
  }
});

test('validates before submitting and counts Unicode code points', () => {
  assert.equal(decode({ prompt: '🎞'.repeat(30000) }).requestBody.duration, 5);
  for (const params of [
    { images: media('image', 11) }, { videos: media('video', 6) }, { audios: media('audio', 4) },
    { start_frame: media('frame', 1) }, { end_frame: media('frame', 1) },
    { duration: 3 }, { duration: 31 }, { duration: 4.5 }, { seconds: 5, duration: 4 },
    { resolution: '4k' }, { ratio: '2:1' }, { prompt: '🎞'.repeat(30001) },
  ]) assert.throws(() => decode(params));
  assert.equal(decode({ audios: media('audio', 1) }).action, 'image_to_video');
});
