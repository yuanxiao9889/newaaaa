# Async Image Task API Integration Guide

This document describes the internal image sync-to-async capability in new-api.
It is intended for downstream clients, SDK authors, and AI agents that need to
integrate with the async image flow.

For the full Chinese guide, see
[`async-image-task-integration.zh-CN.md`](./async-image-task-integration.zh-CN.md).

## Overview

Async mode is explicit. A client adds `?async=true` to a normal image request.
When the root-admin switch `AsyncImageInternalTaskEnabled` is enabled, new-api
creates its own `task_id` immediately, stores a temporary request snapshot, and
returns task metadata without waiting for the upstream image result.

A background worker later replays the saved request through the normal image
relay pipeline, calls any available image-capable upstream channel, downloads or
decodes the returned image, rejects SVG content, and stores the final image on
local disk. The task succeeds only after the backend has stored the image.

If `AsyncImageInternalTaskEnabled` is disabled, `?async=true` falls back to the
original synchronous image behavior for compatibility.

## Supported Endpoints

- `POST /v1/images/generations?async=true`
- `POST /v1/images/edits?async=true`
- `GET /v1/images/tasks/{task_id}`
- `GET /v1/images/tasks/{task_id}/content`

V1 only supports `n=1`. Requests with `n > 1` are rejected before billing.

## Submit Response

Successful async submission returns HTTP `200 OK`:

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "status": "submitted",
  "status_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "content_url": "https://your-host/v1/images/tasks/task_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx/content",
  "expires_at": 0
}
```

`response_format=b64_json` does not change async billing or output retrieval.
Async image results are always fetched later through `content_url`.

## Status Values

- `submitted`: new-api has created the local task.
- `queued`: reserved queued state.
- `processing`: a background worker is executing the request or storing the image.
- `succeeded`: the backend has stored the image; `content_url` can be fetched.
- `failed`: upstream execution, image extraction, download, decode, or storage failed.
- `expired`: the task once succeeded, but the stored image is no longer available.

Clients should poll every 2 seconds at first, then back off to 5-10 seconds.
Status polling may return `429`; clients should slow down and add jitter.

## Content Download

```http
GET /v1/images/tasks/{task_id}/content
Authorization: Bearer <API_KEY>
```

Successful responses stream the stored image with its real `Content-Type` and
`X-Content-Type-Options: nosniff`.

Common errors:

- `400`: task is not completed yet.
- `404`: task does not exist or does not belong to the current user.
- `410`: image content has expired or is unavailable.
- `429`: status polling is too frequent.

## Billing

The submit path pre-consumes quota after validation. Billing is settled when the
backend successfully downloads/decodes and stores the image. Whether the user
later opens or downloads the image does not affect billing.

The task is fully refunded if upstream execution fails, no valid image is
returned, SVG content is returned, image download/decoding fails, local storage
fails, or the task times out. Normal expiry after the configured retention
window does not refund.

## Storage And Worker Settings

- `AsyncImageInternalTaskEnabled`: root-admin switch; default `false`.
- `AsyncImageRetentionHours`: root-admin retention setting; allowed values are
  `2`, `6`, `12`, `18`, and `24`.
- `ASYNC_IMAGE_WORKER_CONCURRENCY`: worker count, default `4`.
- `ASYNC_IMAGE_MAX_UNFINISHED_TASKS`: unfinished internal image task cap,
  default `500`.
- `ASYNC_IMAGE_REQUEST_STORAGE_PATH`: temporary request snapshot directory,
  default `./data/async-image-requests`.
- `ASYNC_IMAGE_STORAGE_PATH`: stored image directory, default
  `./data/async-images`.
- `ASYNC_IMAGE_CLEANUP_INTERVAL_MINUTES`: expired image cleanup interval,
  default `10`.

The current storage model assumes a single node or shared disk. In a multi-node
deployment without shared storage, use object storage or ensure workers and
content requests can access the same files.
