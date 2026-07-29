# Grok 视频 `/v1/videos/generations` 适配层

## 用途

该适配层让 NewAPI 的 OpenAI 视频任务接口兼容 Grok 视频生成协议。桌面端继续提交原有的公开模型和字段，NewAPI 根据渠道类型和模型映射转换请求，无需在桌面端增加渠道专用逻辑。

## 启用条件

在渠道编辑页把“类型”明确设置为 `Grok 视频（Generations）`。适配器不依赖数据库渠道 ID，也不会因为其他渠道使用了相同模型 ID 而自动启用。

该渠道类型当前支持以下映射后的上游模型：

- `grok-api-video-1.5`
- `grok-api-video-1.5-10s`
- `grok-api-video-1.5-15s`
- `grok-video-3`
- `grok-video-3-10s`
- `grok-video-3-16s`
- `grok-video-3-20s`
- `grok-video-1.5-10s`

其他 Sora/OpenAI 视频渠道即使使用相同模型 ID，也仍然使用各自原有的请求和响应逻辑。

## 请求转换

- 提交地址从 `/v1/videos` 切换为 `/v1/videos/generations`。
- `size` 或 `ratio` 转为 `aspect_ratio`。
- 分辨率统一为 `480P`、`720P` 或 `1080P`。
- `input_reference`、`image_url`、`image`、`images`、`reference_images` 及 `messages[].content[].image_url` 会归一化为渠道要求的图片字段。
- `grok-video-3-*` 使用 `images` 和 `quality`。
- 其他 Grok 视频模型使用单个 `image` 和 `resolution`。
- 公开模型 `ok-video-1.5-preview` 映射到 `grok-api-video-1.5` 后，适配器会按画布时长选择按次计费的固定模型：
  - 10 秒 → `grok-api-video-1.5-10s`
  - 15 秒 → `grok-api-video-1.5-15s`
- 公开模型 `ok-imagine-video` 映射到 `grok-video-3` 后，适配器会按画布时长选择按次计费的固定模型：
  - 10 秒 → `grok-video-3-10s`
  - 16 秒 → `grok-video-3-16s`
  - 20 秒 → `grok-video-3-20s`
- 上述固定时长请求不会向上游转发 `seconds`。
- 直接请求 `grok-api-video-1.5` 时仍保留可变时长语义，转发字符串形式的 `seconds`，并严格限制为 1–20 的整数。

## 响应转换

- 兼容提交响应中的嵌套 `data.id`。
- 对外始终返回 NewAPI 的公开任务 ID，不暴露上游任务 ID。
- 成功后 `video_url` 返回渠道提供的直接视频地址，供桌面端优先下载。
- `url` 返回 NewAPI `/v1/videos/{task_id}/content` 代理地址，供直接下载失败时兜底。

## 维护与验证

主要代码：

- `relay/channel/task/sora/grok_video_generations.go`
- `relay/channel/task/sora/adaptor.go`
- `relay/channel/task/sora/grok_video_generations_test.go`

定向回归：

```bash
go test ./relay/channel/task/sora
```

新增同协议渠道时，选择 `Grok 视频（Generations）` 类型并配置对应的 Base URL、密钥、公开模型和模型映射即可。新增上游模型时，应先确认其字段规则与 `/v1/videos/generations` 协议一致，再加入支持列表并补充测试。不要使用模型 ID 或渠道数据库 ID 作为适配器开关。
