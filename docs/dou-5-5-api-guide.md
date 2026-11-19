# OOpii dou-5-5 图片模型接入文档

更新日期：2026-11-18

`dou-5-5` 是 OOpii 中转站提供的 Seedream 5.5 图片模型，支持文字生成图片、参考图生成与编辑。使用本站 API 令牌，通过图片接口调用。

## 1. 接入信息

| 项目 | 配置 |
| --- | --- |
| 控制台 | <https://www.oopii.cc> |
| 服务根地址 | `https://www.oopii.cc` |
| OpenAI 兼容客户端的 Base URL | `https://www.oopii.cc/v1` |
| 模型 ID | `dou-5-5`，请原样填写 |
| 鉴权 | `Authorization: Bearer YOUR_API_KEY` |
| JSON 请求类型 | `Content-Type: application/json` |
| 单次生成数量 | `n: 1` |
| 推荐调用方式 | 异步提交 → 查询任务 → 下载图片 |

在控制台创建 API 令牌，确认账户余额、令牌额度及模型权限可用。若令牌启用了模型限制，需要放行 `dou-5-5`；分组需具备该模型的可用渠道。

下文的 `YOUR_API_KEY` 均替换为你自己的本站令牌。将令牌放在服务端环境变量中，不要写入公开网页或客户端源码。

这是图片模型，请调用 `/v1/images/generations` 或 `/v1/images/edits`。仅支持聊天接口的软件，需要另外接入图片生成功能。客户端已经自动拼接 `/v1` 时，不要重复填写成 `/v1/v1`。

## 2. 接口一览

| 用途 | 方法 | 路径 |
| --- | --- | --- |
| 异步文生图 | POST | `/v1/images/generations?async=true` |
| 异步参考图编辑 | POST | `/v1/images/edits?async=true` |
| 查询图片任务 | GET | `/v1/images/tasks/{task_id}` |
| 下载任务图片 | GET | `/v1/images/tasks/{task_id}/content?index=0` |
| 同步文生图 | POST | `/v1/images/generations` |
| 同步参考图编辑 | POST | `/v1/images/edits` |

`async=true` 放在 URL 查询参数中。收到 `task_id` 表示任务已受理，图片是否成功需要继续查询。

以下 JSON 响应均为结构示例；任务 ID、图片链接和时间戳以实际响应为准，客户端应允许存在其他字段。

## 3. 快速开始：生成一张图片

### 第一步：提交任务

以下 curl 示例使用 Bash 语法，适用于 Linux、macOS、WSL 或 Git Bash。Windows 用户也可直接使用第 7 节的 Python 示例。

```bash
export OOPII_API_KEY='YOUR_API_KEY'

curl --fail-with-body 'https://www.oopii.cc/v1/images/generations?async=true' \
  -H "Authorization: Bearer $OOPII_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "dou-5-5",
    "prompt": "一只橘猫坐在窗边，温暖的午后阳光，写实摄影，细节清晰，不要文字",
    "n": 1,
    "size": "2048x2048",
    "response_format": "url"
  }'
```

受理后返回示例：

```json
{
  "task_id": "task_example",
  "status": "submitted",
  "status_url": "https://www.oopii.cc/v1/images/tasks/task_example",
  "content_url": "https://www.oopii.cc/v1/images/tasks/task_example/content",
  "expires_at": 0
}
```

**立即保存 `task_id`。** 页面刷新、进程重启或查询超时后，可使用同一任务 ID 继续查询。提交阶段的 `expires_at: 0` 不表示永久保存。

### 第二步：查询任务

用返回的真实任务 ID 替换 `task_example`，建议每 5 秒查询一次。

```bash
curl --fail-with-body 'https://www.oopii.cc/v1/images/tasks/task_example' \
  -H "Authorization: Bearer $OOPII_API_KEY"
```

| `status` | 含义 | 客户端处理 |
| --- | --- | --- |
| `submitted` | 已受理 | 继续查询 |
| `queued` | 排队中 | 继续查询 |
| `processing` | 处理中 | 继续查询 |
| `succeeded` | 图片生成成功 | 下载并保存图片 |
| `failed` | 任务失败 | 读取 `error`，记录任务 ID |
| `expired` | 结果已过期 | 停止查询，该结果已不可下载 |

成功响应中的主要字段：

```json
{
  "task_id": "task_example",
  "status": "succeeded",
  "status_url": "https://www.oopii.cc/v1/images/tasks/task_example",
  "content_url": "https://www.oopii.cc/v1/images/tasks/task_example/content",
  "data": [
    {
      "url": "https://www.oopii.cc/v1/images/tasks/task_example/signed-content?index=0&token=EXAMPLE"
    }
  ],
  "expires_at": 1795051200
}
```

任务失败时，查询接口仍可能返回 HTTP 200；必须检查 JSON 中的 `status`。例如：

```json
{
  "task_id": "task_example",
  "status": "failed",
  "error": "任务失败原因，以实际响应为准"
}
```

### 第三步：下载图片

成功后，用同一账户的有效令牌请求内容接口。`index=0` 表示第一张图片。

```bash
curl --fail-with-body \
  'https://www.oopii.cc/v1/images/tasks/task_example/content?index=0' \
  -H "Authorization: Bearer $OOPII_API_KEY" \
  --output result.image
```

内容接口返回图片二进制。按响应 `Content-Type` 确定文件扩展名，例如 `image/png` 对应 `.png`，`image/jpeg` 对应 `.jpg`；示例中的 `.image` 只是通用文件名。

也可以使用 `data[0].url` 展示或下载图片。此类带签名的链接需完整保留查询参数，不需要附加 API 令牌。不要把 API 令牌发送到外部图片域名。

当前站点的异步图片结果保留配置为 **12 小时**，具体过期时间以 `expires_at`（Unix 秒）为准。若返回 `url_expires_at`，它表示展示链接的到期时间，可能早于图片结果的到期时间。图片尚未过期时可重新查询任务获取可用链接，或使用带鉴权的内容接口。生成成功后请及时保存到自己的存储。

## 4. 请求参数与尺寸

| 参数 | 类型 | 要求 | 说明 |
| --- | --- | --- | --- |
| `model` | string | 必填 | 固定为 `dou-5-5` |
| `prompt` | string | 必填 | 非空提示词，可使用中文；建议写清主体、构图、风格及修改要求 |
| `n` | integer | 建议显式传入 | 固定为 `1`；需要多张时分别提交任务 |
| `size` | string | 建议显式传入 | 使用下表的像素规格，连接符为小写字母 `x` |
| `response_format` | string | 建议传入 `url` | 异步方式通过任务查询和内容接口获取图片 |
| `image` | string[] | 参考图编辑时传入 | 可公开访问的图片 URL 数组，详见下一节 |

建议从以下规格开始接入：

| 画幅 | `size` | 示例用途 |
| --- | --- | --- |
| 1:1 | `2048x2048` | 方形主图、头像 |
| 16:9 | `2048x1152` | 横版配图、视频封面 |
| 9:16 | `1152x2048` | 竖版海报 |
| 4:3 | `2048x1536` | 横版插画 |
| 3:4 | `1536x2048` | 竖版商品图 |
| 3:2 | `2048x1365` | 横版摄影构图 |
| 2:3 | `1365x2048` | 竖版摄影构图 |
| 21:9 | `2048x878` | 超宽横幅 |

`size` 用于表达期望的尺寸或画幅，模型可能按可用规格调整最终像素尺寸。需要固定宽高的业务，应在下载后读取实际图片尺寸并做裁切或缩放。21:9 的可用性取决于当前支持该画幅的线路。

以上为接入建议规格，并非承诺任意像素值、所有分辨率档位均可使用。不建议直接套用其他图片模型的 `4K`、`quality: "hd"`、透明背景或遮罩参数。额外参数在不同线路上的支持情况可能不同；首次接入先使用本节列出的基础参数。

## 5. 参考图生成与编辑

### 使用图片 URL

参考图 URL 应指向图片文件本身，且无需登录即可通过公网获取。带有效签名的存储 URL 可以使用，但有效期需覆盖排队和生成过程。网页地址、登录后的预览页、本地文件路径和内网地址不能当作图片 URL。

```bash
curl --fail-with-body 'https://www.oopii.cc/v1/images/edits?async=true' \
  -H "Authorization: Bearer $OOPII_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "dou-5-5",
    "prompt": "保留参考图中商品的外观、颜色和标志，将背景改为浅米色摄影棚，柔和侧光，商品居中",
    "n": 1,
    "size": "1536x2048",
    "response_format": "url",
    "image": ["https://your-storage.example.com/product.jpg"]
  }'
```

请将示例 URL 替换为自己的真实图片地址。多图时在 `image` 数组中依次添加 URL，并在提示词中说明“图 1”“图 2”各自的用途。为兼容当前接入线路，建议控制在 **1–10 张参考图**，初次接入先使用 1 张。

### 上传本地图片

使用 `multipart/form-data` 调用编辑接口，通过重复 `image` 文件字段上传多张参考图。不要手动设置 `Content-Type`，由 curl 自动生成 multipart boundary。

```bash
curl --fail-with-body 'https://www.oopii.cc/v1/images/edits?async=true' \
  -H "Authorization: Bearer $OOPII_API_KEY" \
  -F 'model=dou-5-5' \
  -F 'prompt=保留人物面部特征，将背景改为海边日落，写实摄影' \
  -F 'n=1' \
  -F 'size=1152x2048' \
  -F 'response_format=url' \
  -F 'image=@./reference.jpg'
```

文件上传和 JSON URL 引用选用一种方式，不要在同一请求中混用多种参考图来源。文件上传需要可处理本地文件的线路，若提示该方式不可用，可先上传到自己的图片存储，再使用 URL 方式。

编辑任务的查询和下载步骤与文生图完全相同。

## 6. 同步调用

不添加 `?async=true` 时，请求会等待生成完成后返回图片结果：

```bash
curl --fail-with-body --max-time 660 'https://www.oopii.cc/v1/images/generations' \
  -H "Authorization: Bearer $OOPII_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "dou-5-5",
    "prompt": "雪山湖泊，清晨薄雾，电影感自然风光摄影",
    "n": 1,
    "size": "2048x1152",
    "response_format": "url"
  }'
```

成功响应示例：

```json
{
  "created": 1795008000,
  "data": [
    {
      "url": "https://your-storage.example.com/generated-image.png"
    }
  ]
}
```

同步模式的 URL 应及时保存，其有效期不能直接套用本站异步结果的保留时间。客户端设置较长超时，并不能避免网络代理或服务端自身的超时限制。

同步调用超时不一定代表生成失败，直接重复提交可能产生新的任务和费用。需要断线恢复的业务应使用异步方式。标准 OpenAI 图片客户端通常只处理同步 `data` 响应；本站异步任务需要按本文自行轮询。

## 7. Python 完整示例：提交、恢复查询、下载

使用 Python 3 标准库，无需安装第三方包。先设置环境变量：

```bash
# Bash
export OOPII_API_KEY='YOUR_API_KEY'
```

```powershell
# Windows PowerShell
$env:OOPII_API_KEY = 'YOUR_API_KEY'
```

保存以下代码为 `dou55.py`，运行 `python dou55.py`。程序将任务 ID 保存到 `dou55-task.txt`；再次运行时优先恢复该任务，不会自动重新生成。需要生成新图片时，确认旧任务状态后另行保存或删除这个任务 ID 文件。

```python
import json
import os
import time
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

BASE = "https://www.oopii.cc"
API_KEY = os.environ["OOPII_API_KEY"]
TASK_FILE = Path("dou55-task.txt")


def call_json(path, body=None):
    headers = {"Authorization": "Bearer " + API_KEY}
    payload = None
    if body is not None:
        payload = json.dumps(body, ensure_ascii=False).encode("utf-8")
        headers["Content-Type"] = "application/json"
    request = Request(BASE + path, data=payload, headers=headers)
    with urlopen(request, timeout=60) as response:
        return json.load(response)


if TASK_FILE.exists():
    task_id = TASK_FILE.read_text(encoding="utf-8").strip()
    if not task_id:
        raise RuntimeError("任务 ID 文件为空，请核对原任务后再处理")
else:
    # POST 只发送一次；请求超时时不要直接自动重发。
    task = call_json("/v1/images/generations?async=true", {
        "model": "dou-5-5",
        "prompt": "一只橘猫坐在窗边，温暖的午后阳光，写实摄影",
        "n": 1,
        "size": "2048x2048",
        "response_format": "url",
    })
    task_id = task["task_id"]
    print("已受理，任务 ID：", task_id, flush=True)
    TASK_FILE.write_text(task_id, encoding="utf-8")

print("正在查询：", task_id, flush=True)
deadline = time.monotonic() + 15 * 60
while time.monotonic() < deadline:
    try:
        task = call_json("/v1/images/tasks/" + task_id)
    except HTTPError as exc:
        if exc.code == 429 or 500 <= exc.code < 600:
            time.sleep(10)  # 仅重试查询，不重新提交生成。
            continue
        raise
    except (URLError, TimeoutError):
        time.sleep(10)
        continue

    status = task.get("status")
    print("状态：", status, flush=True)
    if status == "succeeded":
        request = Request(
            BASE + "/v1/images/tasks/" + task_id + "/content?index=0",
            headers={"Authorization": "Bearer " + API_KEY},
        )
        with urlopen(request, timeout=120) as response:
            mime = response.headers.get_content_type()
            suffix = {"image/png": ".png", "image/jpeg": ".jpg",
                      "image/webp": ".webp"}.get(mime, ".image")
            output = Path("dou55-result" + suffix)
            output.write_bytes(response.read())
        print("图片已保存：", output.resolve())
        break
    if status in {"failed", "expired"}:
        raise RuntimeError(f"任务 {task_id} {status}: {task.get('error', '')}")
    if status not in {"submitted", "queued", "processing"}:
        raise RuntimeError(f"未知任务状态：{status}，请保留任务 ID 并检查响应")
    time.sleep(5)
else:
    raise TimeoutError(f"本次查询等待结束；任务 {task_id} 已保存，可再次运行继续查询")
```

示例中的 15 分钟是客户端单次等待上限，不是模型的生成时长承诺，也不会取消服务器上的任务。下载失败后可再次运行脚本，继续查询和下载同一任务。

## 8. 错误处理、计费与接入检查

| 情况 | 建议处理 |
| --- | --- |
| 401 / 令牌无效 | 检查是否使用本站 API 令牌、是否过期，以及 Bearer 请求头是否正确 |
| 无模型权限、分组不可用 | 检查令牌模型限制和所属分组，确认已开放 `dou-5-5` |
| 余额或令牌额度不足 | 检查账户余额和该令牌剩余额度 |
| 400 / 参数错误 | 检查 `model`、非空 `prompt`、`n=1`、尺寸及参考图字段 |
| 429 / 请求过于频繁 | 降低并发，增加查询间隔；按响应提示等待 |
| 查询返回 404 | 检查任务 ID、站点地址及令牌所属账户 |
| 下载返回 410 或任务为 `expired` | 结果已过期，不能通过重复查询恢复图片 |
| HTTP 200，但 `status=failed` | 读取 `error`，保留任务 ID 与错误信息后排查 |
| 提交超时或连接断开 | 若已有任务 ID，继续查询；没有任务 ID 时先在控制台核对任务和调用记录，避免盲目重发 |
| 图片尺寸与请求值不同 | 以下载文件的实际尺寸为准，业务端做必要的后处理 |

价格、分组倍率和账户实际扣费，以控制台模型价格页及调用记录为准。提交时可能先预扣额度，再根据任务结果结算；不要仅凭提交响应判断最终费用。失败任务的结算或退款状态可在控制台核对，重复 POST 应视为可能创建新的计费任务。

接入时确认以下事项：

1. 模型名使用 `dou-5-5`，通过图片接口提交，且 `n=1`。
2. 保存每次受理返回的任务 ID，按 `status` 判断结果。
3. 参考图可被公网访问，并在任务完成前保持有效。
4. 只对查询和下载做受控重试，不因等待较久反复提交同一生成请求。
5. 成功后及时下载图片，保留任务 ID 和必要的错误信息以便排查。
