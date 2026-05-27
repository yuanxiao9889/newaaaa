# 图片异步任务接口 V1

本功能是 new-api 中转站内部的“图片同步转异步”能力。客户端显式使用 `?async=true` 后，中转站立即返回自己的 `task_id`，后台 worker 再调用上游图片渠道、下载结果图片并保存到站内。

更完整的中文接入说明见：[`docs/async-image-task-integration.zh-CN.md`](./async-image-task-integration.zh-CN.md)。

## 行为摘要

- 不带 `?async=true`：保持原同步图片接口行为。
- 带 `?async=true` 且后台开关 `AsyncImageInternalTaskEnabled` 开启：返回异步任务信息。
- 带 `?async=true` 但后台开关关闭：退回同步图片接口行为。
- V1 覆盖 `/v1/images/generations` 和 `/v1/images/edits`。
- V1 只支持 `n=1`，`n>1` 在扣费前拒绝。
- 任务成功标准是后台成功下载并保存图片，不是用户实际下载图片。
- 图片过期删除不退款。

## 提交任务

```http
POST /v1/images/generations?async=true
Authorization: Bearer <API_KEY>
Content-Type: application/json
```

```http
POST /v1/images/edits?async=true
Authorization: Bearer <API_KEY>
Content-Type: multipart/form-data
```

成功响应：

```json
{
  "task_id": "task_xxx",
  "status": "submitted",
  "status_url": "/v1/images/tasks/task_xxx",
  "content_url": "/v1/images/tasks/task_xxx/content",
  "expires_at": 0
}
```

## 查询状态

```http
GET /v1/images/tasks/{task_id}
Authorization: Bearer <API_KEY>
```

状态值：

- `submitted`
- `queued`
- `processing`
- `succeeded`
- `failed`
- `expired`

## 获取图片

```http
GET /v1/images/tasks/{task_id}/content
Authorization: Bearer <API_KEY>
```

成功时返回图片二进制流。任务未完成返回 `400`，图片过期返回 `410`，任务不存在或不属于当前用户返回 `404`。
