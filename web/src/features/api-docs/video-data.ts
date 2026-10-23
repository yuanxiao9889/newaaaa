// Contracts checked against canvas runtime catalog revision 63 and public models on 2026-10-22.
import type { ContractParameter } from './contract-parameters'

export type VideoParameter = ContractParameter
export type VideoVariant = {
  models: string[]
  seconds: string
  resolution: string
  ratios: string
  limits: string
  constraints: Record<string, unknown>
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

export const pendingVideoModels = [
  '全能视频2.5-301010-c1',
  '全能视频2.5-30010-T',
]
export const pendingVideoNote =
  'These canvas aliases still need a public API mapping check. Do not infer an available model ID from their names.'

export const videoCommonParameters: VideoParameter[] = [
  {
    name: 'model',
    type: 'string',
    required: true,
    description: 'apiDocs.video.modelField',
  },
  {
    name: 'prompt',
    type: 'string',
    required: true,
    description: 'apiDocs.video.promptField',
  },
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
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 10,
          maxVideos: 0,
          maxAudios: 0,
          maxTotal: 10,
          maxPromptCharacters: 100000,
          audioRequiresVisual: true,
        },
      },
    ],
    parameters: [
      {
        name: 'duration',
        type: 'integer',
        required: true,
        description: 'apiDocs.video.durationField',
      },
      {
        name: 'ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.ratioField',
      },
      {
        name: 'images',
        type: 'string[]',
        required: false,
        description:
          'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
      },
      {
        name: 'resolution',
        type: 'string',
        required: true,
        description: 'Use a resolution listed for the selected model.',
      },
    ],
    notes: [
      'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
    ],
    examples: [
      {
        title: 'apiDocs.video.textExample',
        body: {
          model: 'muse-video',
          prompt: 'A slow camera push through a sunlit forest.',
          duration: 6,
          ratio: '16:9',
          resolution: '720p',
        },
      },
      {
        title: 'Image reference example',
        body: {
          model: 'muse-video',
          prompt: 'Animate the reference character walking through the scene.',
          duration: 6,
          images: ['https://your-storage.example.com/reference.jpg'],
          ratio: '16:9',
          resolution: '720p',
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
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 10,
          maxVideos: 5,
          maxAudios: 5,
          maxTotal: 20,
          maxPromptCharacters: 20000,
          audioRequiresVisual: false,
          maxTotalAudioSeconds: 15,
        },
      },
      {
        models: ['wan3.0-video'],
        seconds: '2–30',
        resolution: '480p / 720p / 1080p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: [
            'textToVideo',
            'reference',
            'firstFrame',
            'firstLastFrame',
          ],
          maxImages: 10,
          maxVideos: 5,
          maxAudios: 5,
          maxTotal: 20,
          maxPromptCharacters: 20000,
          maxVideoSeconds: 15,
          audioRequiresVisual: false,
          maxTotalVideoSeconds: 15,
          maxVideoAndOutputSeconds: 30,
          maxTotalAudioSeconds: 15,
        },
      },
    ],
    parameters: [
      {
        name: 'duration',
        type: 'integer',
        required: true,
        description: 'apiDocs.video.durationField',
      },
      {
        name: 'ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.ratioField',
      },
      {
        name: 'resolution',
        type: 'string',
        required: true,
        description: 'Use a resolution listed for the selected model.',
      },
      {
        name: 'images / videos / audios',
        type: 'string[]',
        required: false,
        description:
          'Use publicly accessible HTTP(S) media URLs. Local paths, private network URLs and login pages cannot be used.',
      },
      {
        name: 'start_frame / end_frame',
        type: 'string[]',
        required: false,
        description:
          'Only wan3.0-video supports these frame arrays: one start frame and optionally one end frame. Do not mix with reference arrays.',
      },
      {
        name: 'watermark',
        type: 'boolean',
        required: false,
        description: 'Send watermark=false only for wan3.0-video.',
      },
    ],
    notes: [
      'Use a resolution listed for the selected model.',
      'Durations in the constraints are in seconds. Check total reference duration and reference-plus-output duration where specified.',
    ],
    examples: [
      {
        title: 'apiDocs.video.mixedExample',
        body: {
          model: '万能视频-3.0-720',
          prompt:
            'Animate the character using the reference motion and audio rhythm.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          images: ['https://your-storage.example.com/reference.jpg'],
          videos: ['https://your-storage.example.com/motion.mp4'],
          audios: ['https://your-storage.example.com/sound.mp3'],
        },
      },
      {
        title: 'wan3.0-video',
        body: {
          model: 'wan3.0-video',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          watermark: false,
        },
      },
      {
        title: 'First and last frame example',
        body: {
          model: 'wan3.0-video',
          prompt: 'A smooth transition between the two frames.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          watermark: false,
          start_frame: ['https://your-storage.example.com/start.jpg'],
          end_frame: ['https://your-storage.example.com/end.jpg'],
        },
      },
    ],
  },
  {
    id: 'video-arrays',
    title: 'Omni video: URL arrays',
    variants: [
      {
        models: ['全能视频2.0-903-720-c'],
        seconds: '4–15',
        resolution: '720p',
        ratios: '16:9, 9:16, 4:3, 1:1, 3:4',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 0,
          maxAudios: 3,
          maxTotal: 12,
          inputModes: ['textToVideo', 'reference'],
        },
      },
      {
        models: ['全能视频2.0-933-720-c'],
        seconds: '4–15',
        resolution: '720p',
        ratios: '16:9, 9:16, 4:3, 1:1, 3:4',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: ['textToVideo', 'reference'],
        },
      },
      {
        models: ['全能视频2.5-301010-T'],
        seconds: '4–30',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          maxPromptCharacters: 100000,
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频2.5-301010-c'],
        seconds: '4–30',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          maxPromptCharacters: 100000,
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频2.0-903-720-c1'],
        seconds: '5–15',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 9,
          maxVideos: 0,
          maxAudios: 3,
          maxTotal: 12,
          maxPromptCharacters: 20000,
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频2.0-930-720-c1'],
        seconds: '5–15',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 0,
          maxTotal: 12,
          maxPromptCharacters: 20000,
          audioRequiresVisual: false,
          maxVideoSeconds: 15,
        },
      },
    ],
    parameters: [
      {
        name: 'duration',
        type: 'integer',
        required: true,
        description: 'apiDocs.video.durationField',
      },
      {
        name: 'ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.ratioField',
      },
      {
        name: 'resolution',
        type: 'string',
        required: true,
        description: 'Use a resolution listed for the selected model.',
      },
      {
        name: 'images / videos / audios',
        type: 'string[]',
        required: false,
        description: 'apiDocs.video.arrayMedia',
      },
    ],
    notes: [
      'apiDocs.video.xingTags',
      'Do not reuse model limits across aliases. The c1 models have different media limits; 301010-T and 301010-c use 16:9, 9:16, 1:1 or 21:9.',
    ],
    examples: [
      {
        title: 'apiDocs.video.mixedExample',
        body: {
          model: '全能视频2.5-301010-T',
          prompt:
            '@Image1 walks with the camera motion of @Video1 and the rhythm of @Audio1.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
          images: ['https://your-storage.example.com/reference.jpg'],
          videos: ['https://your-storage.example.com/motion.mp4'],
          audios: ['https://your-storage.example.com/sound.mp3'],
        },
      },
      {
        title: '全能视频2.0-903-720-c',
        body: {
          model: '全能视频2.0-903-720-c',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
        },
      },
      {
        title: '全能视频2.0-933-720-c',
        body: {
          model: '全能视频2.0-933-720-c',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
        },
      },
      {
        title: '全能视频2.5-301010-c',
        body: {
          model: '全能视频2.5-301010-c',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
        },
      },
      {
        title: '全能视频2.0-903-720-c1',
        body: {
          model: '全能视频2.0-903-720-c1',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
        },
      },
      {
        title: '全能视频2.0-930-720-c1',
        body: {
          model: '全能视频2.0-930-720-c1',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 5,
          ratio: '16:9',
          resolution: '720p',
        },
      },
    ],
  },
  {
    id: 'video-content',
    title: 'Omni video: content format',
    variants: [
      {
        models: ['全能视频2.5-301010-480'],
        seconds: '4–30',
        resolution: '480p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          inputModes: ['textToVideo', 'reference'],
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频2.5-301010-720'],
        seconds: '4–30',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          inputModes: ['textToVideo', 'reference'],
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频2.5-301010-1080'],
        seconds: '4–30',
        resolution: '1080p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          inputModes: ['textToVideo', 'reference'],
          audioRequiresVisual: false,
        },
      },
      {
        models: ['全能视频-2.0-933-480'],
        seconds: '4–15',
        resolution: '480p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: [
            'textToVideo',
            'reference',
            'firstFrame',
            'firstLastFrame',
          ],
          audioRequiresVisual: true,
        },
      },
      {
        models: ['全能视频-2.0-933-720'],
        seconds: '4–15',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: [
            'textToVideo',
            'reference',
            'firstFrame',
            'firstLastFrame',
          ],
          audioRequiresVisual: true,
        },
      },
      {
        models: ['全能视频2.0-参考视频-720-token'],
        seconds: '4–15',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: [
            'textToVideo',
            'reference',
            'firstFrame',
            'firstLastFrame',
          ],
          audioRequiresVisual: true,
        },
      },
      {
        models: ['全能视频2.5-参考视频-720-token'],
        seconds: '4–30',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 30,
          maxVideos: 10,
          maxAudios: 10,
          maxTotal: 50,
          inputModes: ['textToVideo', 'reference'],
          audioRequiresVisual: false,
        },
      },
    ],
    parameters: [
      {
        name: 'seconds',
        type: 'integer',
        required: true,
        description: 'apiDocs.video.secondsField',
      },
      {
        name: 'ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.contentRatio',
      },
      {
        name: 'content',
        type: 'object[]',
        required: false,
        description: 'apiDocs.video.contentField',
      },
      {
        name: 'content[].type',
        type: 'string',
        required: false,
        description: 'apiDocs.video.contentType',
      },
      {
        name: 'content[].role',
        type: 'string',
        required: false,
        description: 'apiDocs.video.contentRole',
      },
      {
        name: 'content[].image_url / video_url / audio_url',
        type: '{ url: string }',
        required: false,
        description: 'apiDocs.video.contentUrl',
      },
      {
        name: 'omni_reference_task_type',
        type: 'string',
        required: false,
        description: 'apiDocs.video.omniType',
      },
    ],
    notes: [
      'apiDocs.video.contentFrame',
      'apiDocs.video.contentTags',
      'apiDocs.video.contentDurations',
      'The model suffix selects resolution: -480, -720 or -1080. Do not send resolution, duration or top-level media arrays.',
      'For 2.5 content models, use reference roles only and set omni_reference_task_type to reference when attaching media.',
    ],
    examples: [
      {
        title: 'apiDocs.video.contentExample',
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
              image_url: {
                url: 'https://your-storage.example.com/reference.jpg',
              },
            },
            {
              type: 'video_url',
              role: 'reference_video',
              video_url: {
                url: 'https://your-storage.example.com/motion.mp4',
              },
            },
          ],
        },
      },
      {
        title: 'apiDocs.video.framesExample',
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
              image_url: {
                url: 'https://your-storage.example.com/reference.jpg',
              },
            },
            {
              type: 'image_url',
              role: 'last_frame',
              image_url: {
                url: 'https://your-storage.example.com/end.jpg',
              },
            },
          ],
        },
      },
      {
        title: '全能视频2.5-301010-480',
        body: {
          model: '全能视频2.5-301010-480',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          seconds: 5,
          ratio: '16:9',
        },
      },
      {
        title: '全能视频2.5-301010-1080',
        body: {
          model: '全能视频2.5-301010-1080',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          seconds: 5,
          ratio: '16:9',
        },
      },
      {
        title: '全能视频-2.0-933-480',
        body: {
          model: '全能视频-2.0-933-480',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          seconds: 5,
          ratio: '16:9',
        },
      },
      {
        title: '全能视频2.0-参考视频-720-token',
        body: {
          model: '全能视频2.0-参考视频-720-token',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          seconds: 5,
          ratio: '16:9',
        },
      },
      {
        title: '全能视频2.5-参考视频-720-token',
        body: {
          model: '全能视频2.5-参考视频-720-token',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          seconds: 5,
          ratio: '16:9',
        },
      },
    ],
  },
  {
    id: 'video-fixed',
    title: 'apiDocs.video.fixedTitle',
    variants: [
      {
        models: ['全能-fast-10s'],
        seconds: '10 / 15',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9, 3:2, 2:3',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: ['textToVideo', 'reference'],
          maxVideoSeconds: 15,
          maxAudioSeconds: 15,
        },
      },
      {
        models: ['全能-fast-15s'],
        seconds: '10 / 15',
        resolution: '720p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9, 3:2, 2:3',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 15,
          inputModes: ['textToVideo', 'reference'],
          maxVideoSeconds: 15,
          maxAudioSeconds: 15,
        },
      },
      {
        models: ['Minimax-H3-10s'],
        seconds: '10 / 15',
        resolution: '768p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9, 3:2, 2:3',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 12,
          inputModes: ['textToVideo', 'reference'],
          maxVideoSeconds: 15,
          maxAudioSeconds: 15,
          maxPromptCharacters: 7000,
        },
      },
      {
        models: ['Minimax-H3-15s'],
        seconds: '10 / 15',
        resolution: '768p',
        ratios: '16:9, 9:16, 1:1, 4:3, 3:4, 21:9, 3:2, 2:3',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 9,
          maxVideos: 3,
          maxAudios: 3,
          maxTotal: 12,
          inputModes: ['textToVideo', 'reference'],
          maxVideoSeconds: 15,
          maxAudioSeconds: 15,
          maxPromptCharacters: 7000,
        },
      },
    ],
    parameters: [
      {
        name: 'aspect_ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.ratioField',
      },
      {
        name: 'images',
        type: 'string[]',
        required: false,
        description: 'apiDocs.video.fixedImages',
      },
      {
        name: 'metadata.video_urls',
        type: 'string[]',
        required: false,
        description: 'apiDocs.video.fixedVideos',
      },
      {
        name: 'metadata.audio_urls',
        type: 'string[]',
        required: false,
        description: 'apiDocs.video.fixedAudios',
      },
    ],
    notes: [
      'apiDocs.video.fixedNote',
      'apiDocs.video.h3Note',
      'apiDocs.video.fixedTags',
    ],
    examples: [
      {
        title: 'apiDocs.video.fixedExample',
        body: {
          model: 'Minimax-H3-10s',
          prompt: '@图片1 moves following @视频1, with the rhythm of @音频1.',
          aspect_ratio: '16:9',
          images: ['https://your-storage.example.com/reference.jpg'],
          metadata: {
            video_urls: ['https://your-storage.example.com/motion.mp4'],
            audio_urls: ['https://your-storage.example.com/sound.mp3'],
          },
        },
      },
      {
        title: '全能-fast-10s',
        body: {
          model: '全能-fast-10s',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          aspect_ratio: '16:9',
        },
      },
      {
        title: '全能-fast-15s',
        body: {
          model: '全能-fast-15s',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          aspect_ratio: '16:9',
        },
      },
      {
        title: 'Minimax-H3-15s',
        body: {
          model: 'Minimax-H3-15s',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          aspect_ratio: '16:9',
        },
      },
    ],
  },
  {
    id: 'video-grok-preview',
    title: 'apiDocs.video.previewTitle',
    variants: [
      {
        models: ['ok-video-1.5-preview'],
        seconds: '10 / 15',
        resolution: '720p',
        ratios: '16:9, 9:16',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 1,
          maxVideos: 0,
          maxAudios: 0,
          maxTotal: 1,
          inputModes: ['reference'],
        },
      },
    ],
    parameters: [
      {
        name: 'seconds',
        type: 'string',
        required: true,
        description: 'apiDocs.video.previewSeconds',
      },
      {
        name: 'size',
        type: 'string',
        required: true,
        description: 'apiDocs.video.previewSize',
      },
      {
        name: 'input_reference / image_url',
        type: 'string',
        required: true,
        description: 'apiDocs.video.previewImage',
      },
    ],
    notes: ['apiDocs.video.previewNote'],
    examples: [
      {
        title: 'apiDocs.video.previewExample',
        body: {
          model: 'ok-video-1.5-preview',
          prompt: 'Animate this scene with a slow camera push-in.',
          seconds: '10',
          size: '1280x720',
          input_reference: 'https://your-storage.example.com/reference.jpg',
          image_url: 'https://your-storage.example.com/reference.jpg',
        },
      },
    ],
  },
  {
    id: 'video-grok-imagine',
    title: 'apiDocs.video.imagineTitle',
    variants: [
      {
        models: ['ok-imagine-video'],
        seconds: '10 / 16 / 20',
        resolution: '480p / 720p',
        ratios: '16:9, 9:16, 1:1',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          maxImages: 7,
          maxVideos: 0,
          maxAudios: 0,
          maxTotal: 7,
          inputModes: ['textToVideo', 'reference', 'firstFrame'],
        },
      },
    ],
    parameters: [
      {
        name: 'duration / seconds',
        type: 'string',
        required: true,
        description: 'apiDocs.video.imagineSeconds',
      },
      {
        name: 'aspect_ratio / ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.imagineRatio',
      },
      {
        name: 'size',
        type: 'string',
        required: true,
        description: 'apiDocs.video.imagineSize',
      },
      {
        name: 'resolution_name / resolution',
        type: 'string',
        required: true,
        description:
          'Set resolution_name and resolution to the same value: 480p or 720p.',
      },
      {
        name: 'stream',
        type: 'boolean',
        required: true,
        description: 'apiDocs.video.streamField',
      },
      {
        name: 'image_url',
        type: 'string',
        required: false,
        description: 'apiDocs.video.imagineImage',
      },
      {
        name: 'messages',
        type: 'object[]',
        required: false,
        description: 'apiDocs.video.imagineMessages',
      },
    ],
    notes: ['apiDocs.video.imagineNote'],
    examples: [
      {
        title: 'apiDocs.video.textExample',
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
        title: 'apiDocs.video.imagineExample',
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
                {
                  type: 'image_url',
                  image_url: {
                    url: 'https://your-storage.example.com/reference.jpg',
                  },
                },
                {
                  type: 'image_url',
                  image_url: {
                    url: 'https://your-storage.example.com/scene.jpg',
                  },
                },
              ],
            },
          ],
        },
      },
    ],
  },
  {
    id: 'video-kling',
    title: 'Kling Video v4',
    variants: [
      {
        models: ['灵动视频-video-v4'],
        seconds: '3–30',
        resolution: '720p / 1080p',
        ratios: '16:9, 9:16, 1:1, 21:9',
        limits:
          'Apply both per-type and total reference limits. A zero limit means that media type is unsupported.',
        constraints: {
          inputModes: ['textToVideo', 'reference'],
          maxImages: 10,
          maxVideos: 5,
          maxAudios: 3,
          maxTotal: 15,
          maxPromptCharacters: 8000,
          maxVideoSeconds: 30,
          maxTotalVideoSeconds: 30,
          maxAudioSeconds: 30,
          audioRequiresVisual: false,
        },
      },
    ],
    parameters: [
      {
        name: 'duration',
        type: 'integer',
        required: true,
        description: 'apiDocs.video.durationField',
      },
      {
        name: 'ratio',
        type: 'string',
        required: true,
        description: 'apiDocs.video.ratioField',
      },
      {
        name: 'resolution',
        type: 'string',
        required: true,
        description: 'Use a resolution listed for the selected model.',
      },
      {
        name: 'images / videos / audios',
        type: 'string[]',
        required: false,
        description: 'apiDocs.video.arrayMedia',
      },
    ],
    notes: [
      'Durations in the constraints are in seconds. Check total reference duration and reference-plus-output duration where specified.',
    ],
    examples: [
      {
        title: '灵动视频-video-v4',
        body: {
          model: '灵动视频-video-v4',
          prompt: 'A cinematic shot of a mountain ridge at sunrise.',
          duration: 10,
          ratio: '16:9',
          resolution: '720p',
        },
      },
    ],
  },
]

export const videoGuideKeys: string[] = [
  'apiDocs.video.scope',
  'apiDocs.video.workflow',
  'apiDocs.video.authentication',
  'apiDocs.video.wireRules',
  'apiDocs.video.mediaRules',
  'apiDocs.video.modes',
  'apiDocs.video.pollRules',
  'apiDocs.video.retryRules',
  'apiDocs.video.resultRules',
  'apiDocs.video.availability',
]

export const videoResponseExamples = {
  created: {
    id: 'task_example',
    object: 'video',
    status: 'queued',
  },
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
    error: {
      code: 'generation_failed',
      message: 'Generation failed',
    },
  },
}
