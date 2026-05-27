# 异步图片任务 API 接入文档

本文档说明 new-api 中转站的“图片同步转异步”能力，适合下游客户端、SDK 和后续 AI Agent 接入时查阅。

## 1. 功能概览

开启后台开关后，客户端在图片接口 URL 上显式追加 `?async=true`，中转站会立即返回自己的 `task_id`，然后由后台 worker 继续调用上游图片渠道、等待结果、下载图片并保存到本地磁盘。

核心行为：

- 不带 `?async=true`：保持原 OpenAI 图片接口同步响应，兼容老客户端。
- 带 `?async=true` 且后台开关开启：立即返回任务信息，不等待图片生成完成。
- 带 `?async=true` 但后台开关关闭：退回原同步图片流程。
- 任务成功标准：后台已经拿到上游图片并成功保存到本地磁盘。
- 用户是否后续打开或下载图片，不影响任务成功和扣费结算。
- 图片文件按后台配置保留，默认 24 小时；过期删除不退款。
- V1 只支持 `n=1`，`n>1` 会在扣费前拒绝。

支持接口：

- `POST /v1/images/generations?async=true`
- `POST /v1/images/edits?async=true`
- `GET /v1/images/tasks/{task_id}`
- `GET /v1/images/tasks/{task_id}/content`

## 2. 鉴权与权限

所有异步图片接口都需要鉴权：

```http
Authorization: Bearer <API_KEY>
```

权限规则：

- 只能查询和下载任务所属用户自己的图片任务。
- 浏览器登录用户也可以在控制台任务页查看自己的异步任务。
- 管理员任务列表可以查看元数据，但图片内容接口仍按任务所属用户鉴权。

## 3. 提交异步任务

### 图片生成

```http
POST /v1/images/generations?async=true
Authorization: Bearer <API_KEY>
Content-Type: application/json
```

请求体与普通同步图片生成接口一致：

```json
{
  "model": "gpt-image-2",
  "prompt": "a cinematic orange cat cooking in a neon kitchen",
  "size": "1024x1024",
  "n": 1,
  "response_format": "url"
}
```

### 图片编辑

```http
POST /v1/images/edits?async=true
Authorization: Bearer <API_KEY>
Content-Type: multipart/form-data
```

图片编辑请求体与普通同步编辑接口一致。中转站会在提交时保存必要的请求快照，因此即使用户断开连接，后台 worker 仍可继续执行任务。任务成功或失败后，原始请求快照会被删除。

### 成功响应

HTTP 状态码：`200 OK`

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "status": "submitted",
  "status_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "content_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content",
  "expires_at": 0
}
```

字段说明：

- `task_id`：中转站生成的稳定任务 ID，客户端必须保存。
- `status`：提交后的初始状态，固定为 `submitted`。
- `status_url`：任务状态查询地址。
- `content_url`：任务成功后的站内图片下载地址。
- `expires_at`：Unix 秒级时间戳；提交时通常为 `0`，成功后会返回真实过期时间。

## 4. 查询任务状态

```http
GET /v1/images/tasks/{task_id}
Authorization: Bearer <API_KEY>
```

成功任务示例：

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "status": "succeeded",
  "status_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "content_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content",
  "expires_at": 1777970000
}
```

失败任务示例：

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "status": "failed",
  "status_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "content_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content",
  "expires_at": 0,
  "error": "upstream returned no image result"
}
```

状态说明：

| 状态 | 含义 | 客户端建议 |
| --- | --- | --- |
| `submitted` | 中转站已创建任务，等待后台 worker 执行。 | 继续轮询。 |
| `queued` | 保留状态，表示任务排队中。 | 继续轮询。 |
| `processing` | 后台正在调用上游或处理图片。 | 继续轮询。 |
| `succeeded` | 后台已下载并保存图片。 | 请求 `content_url` 下载图片。 |
| `failed` | 上游失败、超时、无结果图、下载失败或落盘失败。 | 停止轮询，展示 `error`。 |
| `expired` | 任务曾经成功，但图片文件已过期或不可用。 | 停止轮询，提示图片已过期。 |

`expired` 是图片内容层状态。数据库中的任务仍可能是 `SUCCESS`，只是图片文件已无法下载。

## 5. 下载图片内容

```http
GET /v1/images/tasks/{task_id}/content
Authorization: Bearer <API_KEY>
```

示例：

```bash
curl -L \
  -H "Authorization: Bearer $API_KEY" \
  -o result.png \
  "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content"
```

成功响应：

- HTTP `200 OK`
- `Content-Type` 为真实图片 MIME，例如 `image/png`、`image/jpeg`、`image/webp`
- `Content-Disposition: inline`
- `X-Content-Type-Options: nosniff`

常见错误：

| HTTP 状态码 | 含义 |
| --- | --- |
| `400` | 任务还没完成，不能下载内容。 |
| `404` | 任务不存在，或不属于当前鉴权用户。 |
| `410` | 图片内容已过期或不可用。 |
| `429` | 状态查询过于频繁，需要退避。 |
| `500` | 后端读取已保存图片失败。 |

## 6. 轮询建议

推荐流程：

1. 提交图片请求时追加 `?async=true`。
2. 保存返回的 `task_id`、`status_url`、`content_url`。
3. 每 2 秒查询一次 `status_url`。
4. 如果多次仍未完成，逐步退避到 5 到 10 秒。
5. 状态为 `succeeded` 后下载 `content_url`。
6. 状态为 `failed` 或 `expired` 后停止轮询。

如果收到 `429`，客户端应该降低轮询频率，并加入随机抖动，避免大量用户同时打到状态接口。

## 7. 计费与退款

计费规则：

- 提交成功前会按当前模型价格预扣额度。
- 后台成功下载并保存图片后，任务算成功，预扣费用完成结算。
- 用户后续是否下载图片，不影响扣费结果。
- 上游失败、任务超时、无有效图片、下载失败、落盘失败、SVG 被拒绝，都会任务失败并全额退款。
- 图片正常过期删除不退款，因为后台已经完成取图和保存承诺。
- `response_format=b64_json` 不改变计费；异步模式最终仍通过站内 `content_url` 取图。

## 8. 限制与安全策略

- V1 只支持 `n=1`。
- SVG 图片结果会被拒绝，不保存、不结算，并触发退款。
- 图片下载遵守服务端 URL 安全/SSRF 防护配置。
- 请求快照只在任务执行期间保留，任务成功或失败后删除。
- 默认后台 worker 并发为 `4`，未完成内部图片任务上限为 `500`。

相关配置：

| 配置 | 默认值 | 说明 |
| --- | --- | --- |
| `AsyncImageInternalTaskEnabled` | `false` | 后台开关，控制是否启用图片同步转异步。 |
| `ASYNC_IMAGE_WORKER_CONCURRENCY` | `4` | 后台图片 worker 数量。 |
| `ASYNC_IMAGE_MAX_UNFINISHED_TASKS` | `500` | 未完成内部图片任务上限。 |
| `ASYNC_IMAGE_WORKER_STALE_MINUTES` | `30` | worker 任务领取超时后可被重新领取的时间。 |
| `ASYNC_IMAGE_STORAGE_PATH` | `./data/async-images` | 成功图片存储目录。 |
| `ASYNC_IMAGE_REQUEST_STORAGE_PATH` | `./data/async-image-requests` | 执行中的请求快照目录。 |
| `AsyncImageRetentionHours` | `24` | 图片成功保存后的保留小时数，可在后台选择 2、6、12、18、24。 |

## 9. JavaScript 示例

```js
async function submitAsyncImage(apiBase, apiKey, body) {
  const res = await fetch(`${apiBase}/v1/images/generations?async=true`, {
    method: 'POST',
    headers: {
      Authorization: `Bearer ${apiKey}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body),
  });

  const data = await res.json();
  if (!res.ok) {
    throw new Error(data?.error?.message || 'submit async image task failed');
  }
  return data;
}

async function waitForAsyncImage(task, apiKey) {
  let delayMs = 2000;

  for (;;) {
    const res = await fetch(task.status_url, {
      headers: { Authorization: `Bearer ${apiKey}` },
    });
    const data = await res.json();

    if (!res.ok) {
      throw new Error(data?.error?.message || 'query async image task failed');
    }

    if (data.status === 'succeeded') return data;
    if (data.status === 'failed') throw new Error(data.error || 'image task failed');
    if (data.status === 'expired') throw new Error('image task expired');

    await new Promise((resolve) => setTimeout(resolve, delayMs));
    delayMs = Math.min(delayMs + 1000, 10000);
  }
}
```
