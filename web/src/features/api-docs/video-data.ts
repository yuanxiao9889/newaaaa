// Public contracts checked against the canvas and enabled relay channels on 2026-10-12.
export type VideoParameter = {
  name: string
  type: string
  required: boolean
  description: string
}
export type VideoVariant = {
  models: string[]
  seconds: string
  resolution: string
  ratios: string
  limits: string
}
export type VideoExample = { title: string; body: Record<string, unknown> }
export type VideoGroup = {
  id: string
  title: string
  variants: VideoVariant[]
  parameters: VideoParameter[]
  notes: string[]
  examples: VideoExample[]
}
const key = (value: string) => `apiDocs.video.${value}`
const parameter = (
  name: string,
  type: string,
  required: boolean,
  description: string
): VideoParameter => ({ name, type, required, description: key(description) })
const image = 'https://media.example.com/reference.jpg'
const video = 'https://media.example.com/motion.mp4'
const audio = 'https://media.example.com/sound.mp3'
const ratios3 = '16:9, 9:16, 1:1'
const ratios5 = `${ratios3}, 4:3, 3:4`
const ratios6 = `${ratios5}, 21:9`
const ratios8 = `${ratios6}, 3:2, 2:3`

export const videoCommonParameters = [
  parameter('model', 'string', true, 'modelField'),
  parameter('prompt', 'string', true, 'promptField'),
]

export const videoGroups: VideoGroup[] = [
  {
    id: 'video-muse',
    title: 'Muse Video',
    variants: [
      {
        models: ['muse-video'],
        seconds: '1–30',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits:
          'Up to 10 public image URLs. Video and audio references are not supported. A duration of 10 seconds or less is recommended.',
      },
    ],
    parameters: [
      parameter('duration', 'integer', true, 'durationField'),
      parameter('aspect_ratio', 'string', true, 'ratioField'),
      {
        name: 'images',
        type: 'string[]',
        required: false,
        description:
          'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
      },
    ],
    notes: [
      'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
    ],
    examples: [
      {
        title: key('textExample'),
        body: {
          model: 'muse-video',
          prompt: 'A slow camera push through a sunlit forest.',
          duration: 6,
          aspect_ratio: '16:9',
        },
      },
      {
        title: 'Image reference example',
        body: {
          model: 'muse-video',
          prompt: 'Animate the reference character walking through the scene.',
          duration: 6,
          aspect_ratio: '16:9',
          images: [image],
        },
      },
    ],
  },
  {
    id: 'video-wan30',
    title: 'Wan 3.0',
    variants: [
      {
        models: ['万能视频-3.0-720'],
        seconds: '5–30',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits:
          'Up to 10 images, 5 videos and 5 audio clips; at most 20 references in total. Prompt limit: 20000 characters.',
      },
    ],
    parameters: [
      parameter('duration', 'integer', true, 'durationField'),
      parameter('ratio', 'string', true, 'ratioField'),
      parameter('resolution', 'string', true, 'resolution720'),
      {
        name: 'images / videos / audios',
        type: 'string[]',
        required: false,
        description:
          'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
      },
    ],
    notes: [
      'Use duration as an integer, ratio and resolution=720p. Do not substitute seconds, aspect_ratio or content for this model.',
    ],
    examples: [
      {
        title: key('mixedExample'),
        body: {
          model: '万能视频-3.0-720',
          prompt:
            'Animate the character using the reference motion and audio rhythm.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          images: [image],
          videos: [video],
          audios: [audio],
        },
      },
    ],
  },

  {
    id: 'video-arrays',
    title: key('arraysTitle'),
    variants: [
      {
        models: ['全能视频2.5-301010-T', '全能视频2.5-301010-c'],
        seconds: '4–30',
        resolution: '720p',
        ratios: ratios6,
        limits: key('limits301010'),
      },
      {
        models: ['全能视频2.0-903-720-c'],
        seconds: '4–15',
        resolution: '720p',
        ratios: ratios5,
        limits: key('limits903'),
      },
      {
        models: ['全能视频2.0-933-720-c'],
        seconds: '4–15',
        resolution: '720p',
        ratios: ratios5,
        limits: key('limits933'),
      },
    ],
    parameters: [
      parameter('duration', 'integer', true, 'durationField'),
      parameter('ratio', 'string', true, 'ratioField'),
      parameter('resolution', 'string', true, 'resolution720'),
      parameter('images / videos / audios', 'string[]', false, 'arrayMedia'),
    ],
    notes: [key('arraysNote'), key('xingTags')],
    examples: [
      {
        title: key('mixedExample'),
        body: {
          model: '全能视频2.5-301010-T',
          prompt:
            '@Image1 walks with the camera motion of @Video1 and the rhythm of @Audio1.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          images: [image],
          videos: [video],
          audios: [audio],
        },
      },
    ],
  },
  {
    id: 'video-content',
    title: key('contentTitle'),
    variants: [
      {
        models: ['全能视频2.5-301010-480', '全能视频2.5-301010-720'],
        seconds: '4–30',
        resolution: '480p / 720p',
        ratios: ratios6,
        limits:
          'Up to 30 images, 10 videos and 10 audio clips; at most 50 references in total. Use fixed ratios; adaptive, first/last frames, editing and extension are not supported.',
      },
      {
        models: ['全能视频2.5-无视频参考-480', '全能视频2.5-无视频参考-720'],
        seconds: '4–30',
        resolution: '480p / 720p',
        ratios: ratios6,
        limits: key('limitsNoVideo'),
      },
      {
        models: ['全能视频-2.0-933-480', '全能视频-2.0-933-720'],
        seconds: '4–15',
        resolution: '480p / 720p',
        ratios: `${ratios3}, adaptive`,
        limits: key('limitsMai20'),
      },
    ],
    parameters: [
      parameter('seconds', 'integer', true, 'secondsField'),
      parameter('ratio', 'string', true, 'contentRatio'),
      parameter('content', 'object[]', false, 'contentField'),
      parameter('content[].type', 'string', false, 'contentType'),
      parameter('content[].role', 'string', false, 'contentRole'),
      parameter(
        'content[].image_url / video_url / audio_url',
        '{ url: string }',
        false,
        'contentUrl'
      ),
      parameter('omni_reference_task_type', 'string', false, 'omniType'),
    ],
    notes: [
      key('contentResolution'),
      key('contentFrame'),
      key('contentTags'),
      key('contentDurations'),
      'For 301010-480/720, use reference_image, reference_video and reference_audio roles only. Do not mix content with images/videos/audios. Video references may change billing; check current pricing.',
    ],
    examples: [
      {
        title: key('contentExample'),
        body: {
          model: '全能视频2.5-301010-720',
          prompt:
            '@image1 turns toward the camera, following the movement of @video1.',
          seconds: 5,
          ratio: '16:9',
          omni_reference_task_type: 'reference',
          content: [
            {
              type: 'image_url',
              role: 'reference_image',
              image_url: { url: image },
            },
            {
              type: 'video_url',
              role: 'reference_video',
              video_url: { url: video },
            },
          ],
        },
      },
      {
        title: key('noVideoExample'),
        body: {
          model: '全能视频2.5-无视频参考-480',
          prompt: '@image1 walks to the rhythm of @audio1.',
          seconds: 5,
          ratio: '4:3',
          omni_reference_task_type: 'reference',
          content: [
            {
              type: 'image_url',
              role: 'reference_image',
              image_url: { url: image },
            },
            {
              type: 'audio_url',
              role: 'reference_audio',
              audio_url: { url: audio },
            },
          ],
        },
      },
      {
        title: key('framesExample'),
        body: {
          model: '全能视频-2.0-933-720',
          prompt:
            'A smooth transition from the opening frame to the closing frame.',
          seconds: 5,
          ratio: '16:9',
          content: [
            {
              type: 'image_url',
              role: 'first_frame',
              image_url: { url: image },
            },
            {
              type: 'image_url',
              role: 'last_frame',
              image_url: { url: 'https://media.example.com/end.jpg' },
            },
          ],
        },
      },
    ],
  },
  {
    id: 'video-fixed',
    title: key('fixedTitle'),
    variants: [
      {
        models: ['全能-fast-10s', '全能-fast-15s'],
        seconds: '10 / 15',
        resolution: '720p',
        ratios: ratios8,
        limits: key('limitsFast'),
      },
      {
        models: ['Minimax-H3-10s', 'Minimax-H3-15s'],
        seconds: '10 / 15',
        resolution: '768p',
        ratios: ratios8,
        limits: key('limitsH3'),
      },
    ],
    parameters: [
      parameter('aspect_ratio', 'string', true, 'ratioField'),
      parameter('images', 'string[]', false, 'fixedImages'),
      parameter('metadata.video_urls', 'string[]', false, 'fixedVideos'),
      parameter('metadata.audio_urls', 'string[]', false, 'fixedAudios'),
    ],
    notes: [key('fixedNote'), key('h3Note'), key('fixedTags')],
    examples: [
      {
        title: key('fixedExample'),
        body: {
          model: 'Minimax-H3-10s',
          prompt: '@图片1 moves following @视频1, with the rhythm of @音频1.',
          aspect_ratio: '16:9',
          images: [image],
          metadata: { video_urls: [video], audio_urls: [audio] },
        },
      },
    ],
  },
  {
    id: 'video-mini',
    title: key('miniTitle'),
    variants: [
      {
        models: ['全能-mini'],
        seconds: '480p: 5–15; 720p: 5–12',
        resolution: '480p / 720p',
        ratios: ratios3,
        limits: key('limitsMini'),
      },
    ],
    parameters: [
      parameter('seconds', 'integer', true, 'secondsField'),
      parameter('ratio', 'string', true, 'ratioField'),
      parameter('resolution', 'string', true, 'miniResolution'),
      parameter('image_urls', 'string[]', false, 'miniImages'),
      parameter('audio_urls', 'string[]', false, 'miniAudios'),
    ],
    notes: [key('miniNote')],
    examples: [
      {
        title: key('miniExample'),
        body: {
          model: '全能-mini',
          prompt: 'The character in the reference image waves at the camera.',
          seconds: 5,
          resolution: '480p',
          ratio: '16:9',
          image_urls: [image],
          audio_urls: [audio],
        },
      },
    ],
  },
  {
    id: 'video-grok-preview',
    title: key('previewTitle'),
    variants: [
      {
        models: ['ok-video-1.5-preview'],
        seconds: '10 / 15',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits: key('limitsPreview'),
      },
    ],
    parameters: [
      parameter('seconds', 'string', true, 'previewSeconds'),
      parameter('size', 'string', true, 'previewSize'),
      parameter('input_reference / image_url', 'string', true, 'previewImage'),
    ],
    notes: [key('previewNote')],
    examples: [
      {
        title: key('previewExample'),
        body: {
          model: 'ok-video-1.5-preview',
          prompt: 'Animate this scene with a slow camera push-in.',
          seconds: '10',
          size: '1280x720',
          input_reference: image,
          image_url: image,
        },
      },
    ],
  },
  {
    id: 'video-grok-imagine',
    title: key('imagineTitle'),
    variants: [
      {
        models: ['ok-imagine-video'],
        seconds: '10 / 16 / 20',
        resolution: '480p / 720p / 1080p',
        ratios: ratios3,
        limits: key('limitsImagine'),
      },
    ],
    parameters: [
      parameter('duration / seconds', 'string', true, 'imagineSeconds'),
      parameter('aspect_ratio / ratio', 'string', true, 'imagineRatio'),
      parameter('size', 'string', true, 'imagineSize'),
      parameter(
        'resolution_name / resolution',
        'string',
        true,
        'imagineResolution'
      ),
      parameter('stream', 'boolean', true, 'streamField'),
      parameter('image_url', 'string', false, 'imagineImage'),
      parameter('messages', 'object[]', false, 'imagineMessages'),
    ],
    notes: [key('imagineNote')],
    examples: [
      {
        title: key('textExample'),
        body: {
          model: 'ok-imagine-video',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          stream: false,
          duration: '10',
          seconds: '10',
          aspect_ratio: '16:9',
          ratio: '16:9',
          size: '1280x720',
          resolution_name: '720p',
          resolution: '720p',
        },
      },
      {
        title: key('imagineExample'),
        body: {
          model: 'ok-imagine-video',
          prompt: 'The same character walks through the scene.',
          stream: false,
          duration: '10',
          seconds: '10',
          aspect_ratio: '16:9',
          ratio: '16:9',
          size: '1280x720',
          resolution_name: '720p',
          resolution: '720p',
          messages: [
            {
              role: 'user',
              content: [
                {
                  type: 'text',
                  text: 'The same character walks through the scene.',
                },
                { type: 'image_url', image_url: { url: image } },
                {
                  type: 'image_url',
                  image_url: { url: 'https://media.example.com/scene.jpg' },
                },
              ],
            },
          ],
        },
      },
    ],
  },
]

export const videoGuideKeys = [
  'scope',
  'workflow',
  'authentication',
  'wireRules',
  'mediaRules',
  'modes',
  'pollRules',
  'retryRules',
  'resultRules',
  'availability',
].map(key)
export const videoResponseExamples = {
  created: { id: 'task_example', object: 'video', status: 'queued' },
  completed: {
    id: 'task_example',
    object: 'video',
    status: 'completed',
    progress: 100,
  },
  failed: {
    id: 'task_example',
    object: 'video',
    status: 'failed',
    error: { code: 'generation_failed', message: 'Generation failed' },
  },
}
