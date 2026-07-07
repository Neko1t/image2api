# YCYAPI 视频生成接口使用说明

> 接口地址：https://ycyapi.cn
> 更新日期：2026-07-03

---

## 一、模型与尺寸

| 模型名 | 时长 | 支持比例 |
|---|---|---|
| `video-v1-5s` | 5 秒 | 16:9 / 9:16 / 1:1 |
| `video-v1-10s` | 10 秒 | 16:9 / 9:16 / 1:1 |
| `video-v1-15s` | 15 秒 | 16:9 / 9:16 / 1:1 |

**分辨率对照：**

| 比例 | 分辨率 | 适用场景 |
|---|---|---|
| 16:9（横屏） | 1920×1080 | PC 展示、B 站、YouTube |
| 9:16（竖屏） | 1080×1920 | 抖音、微信视频号、小红书 |
| 1:1（方形） | 1080×1080 | 朋友圈、Instagram |

---

## 二、接口使用

### 第一步：提交生成任务

**请求**

```
POST https://ycyapi.cn/v1/video/generations
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json
```

**参数说明**

| 参数 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | ✅ | 模型名，见上表 |
| `prompt` | string | ✅ | 文生视频＝画面描述；图生视频＝运动描述。支持中英文 |
| `image` | string | ❌ | **单图生视频**：一张参考图的 base64 data-URI，视频以该图为起始画面动起来（详见「图生视频」一节） |
| `images` | string[] | ❌ | **多图生视频**：多张参考图的 base64 data-URI 数组（实测支持 8 张），视频按图片顺序生成转场画面，适合相册/分镜成片（详见「多图生视频」一节） |
| `ratio` | string | ❌ | 宽高比，默认 `16:9`，可选 `9:16` / `1:1` |

> 只传 `prompt` = 文生视频；带 `image` = 单图生视频；带 `images` 数组 = 多图生视频。模型、轮询、下载方式完全一致。

**请求示例**

```json
{
  "model": "video-v1-5s",
  "prompt": "一只橘猫在阳光下的草地上奔跑，慢动作，电影感",
  "ratio": "16:9"
}
```

**返回示例**

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxx",
  "status": "queued",
  "seconds": "5"
}
```

---

### 图生视频（带参考图 / 让图片动起来）

在上面文生视频请求的基础上，**加参考图字段即可**：`image` 传一张（让静态图动起来），`images` 数组传多张（多图转场成片）。模型档位、轮询、下载、价格全部与文生视频相同。

#### 参考图统一用 base64 data-URI 传（把图直接放进请求里）

把本地图片编码成 base64 的 `data:` URI，直接塞进 `image` 字段。图片数据随请求直接送达，**不经过任何「抓图」环节**，无需图床，成功率最高：

```json
{
  "model": "video-v1-5s",
  "prompt": "镜头缓缓推进，猫咪眨眼并轻轻转头",
  "image": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQ...（此处为整张图片的 base64 编码）"
}
```

> ⚠️ 必须带 `data:image/jpeg;base64,` 前缀（png 图片则用 `data:image/png;base64,`）；**只放裸 base64 会失败**。
> 建议先把图片压到 **≤1024px、JPEG 格式**再编码，体积更小、更稳定。

**本地图片转 data-URI（Python 片段）**

```python
import base64
with open("cat.jpg", "rb") as f:
    data_uri = "data:image/jpeg;base64," + base64.b64encode(f.read()).decode()
# 然后把 data_uri 当作 image 传入，其余流程与文生视频完全一致：
# json = {"model": "video-v1-5s", "prompt": "...", "image": data_uri}
```

#### 多图生视频（相册 / 分镜转场成片）

用 `images` 数组一次传入多张图（**实测支持 8 张**，每张都是 base64 data-URI），视频会**按图片顺序依次生成转场画面**——适合产品多角度展示、旅行相册、分镜串片：

```json
{
  "model": "video-v1-15s",
  "prompt": "镜头平滑运镜，画面依次自然过渡",
  "images": [
    "data:image/jpeg;base64,/9j/4AAQ...（第 1 张）",
    "data:image/jpeg;base64,/9j/4AAQ...（第 2 张）",
    "data:image/jpeg;base64,/9j/4AAQ...（第 3 张，最多 8 张）"
  ]
}
```

> 💡 图片数量多时建议选 `video-v1-15s`，每个场景的停留时间更充分；只传 1 张时请用 `image` 字段。
> 💡 每张图建议压到 **≤1024px、JPEG** 再编码，8 张也只有几百 KB，提交更快更稳。

**提示词写法**：图生视频的 `prompt` 用来描述**画面怎么动**（镜头运动、主体动作、转场节奏），不要重复描述图片内容。例如「镜头缓慢拉远，人物微笑并挥手」「场景间平滑淡入淡出」。

---

### 第二步：轮询任务状态

**请求**

```
GET https://ycyapi.cn/v1/video/generations/{task_id}
Authorization: Bearer YOUR_API_KEY
```

**状态说明**

| status | 含义 | 处理建议 |
|---|---|---|
| `queued` | 排队中 | 继续等待 |
| `IN_PROGRESS` | 生成中 | 继续轮询（进度停在 5% 属正常） |
| `SUCCESS` | 生成完成 | 取 result_url 下载视频 |
| `FAILURE` | 生成失败 | 稍等后重新提交 |

**完成后返回示例**

```json
{
  "task_id": "task_xxxxxxxxxxxxxxxxxxxx",
  "status": "SUCCESS",
  "result_url": "/v1/videos/task_xxxxxxxxxxxxxxxxxxxx/content"
}
```

**视频下载地址**

```
https://ycyapi.cn/v1/videos/{task_id}/content
```

---

## 三、调用示例（Python）

```python
import requests
import time

API_KEY = "YOUR_API_KEY"
BASE_URL = "https://ycyapi.cn"
HEADERS = {"Authorization": f"Bearer {API_KEY}", "Content-Type": "application/json"}

# 第一步：提交任务
resp = requests.post(f"{BASE_URL}/v1/video/generations", headers=HEADERS, json={
    "model": "video-v1-10s",
    "prompt": "夕阳下的城市天际线，延时摄影，电影感",
    "ratio": "16:9"
    # 图生视频：再加一个 "image": "data:image/jpeg;base64,..."（转法见「图生视频」一节）
})
task_id = resp.json()["task_id"]
print(f"任务已提交：{task_id}")

# 第二步：轮询结果
while True:
    time.sleep(15)
    result = requests.get(f"{BASE_URL}/v1/video/generations/{task_id}", headers=HEADERS).json()
    status = result.get("data", result).get("status")
    print(f"状态：{status}")
    if status == "SUCCESS":
        video_url = f"{BASE_URL}/v1/videos/{task_id}/content"
        print(f"视频地址：{video_url}")
        break
    elif status == "FAILURE":
        print("生成失败，请重新提交")
        break
```

---

## 四、注意事项

1. **生成时间约 3～5 分钟**，请耐心等待，勿频繁重复提交
2. **轮询频率建议 15 秒一次**，进度停在 5% 是正常现象，不是卡死
3. **视频格式为 MP4**，可直接下载或嵌入播放器
4. **Prompt 越具体，效果越好**，建议包含：主体、动作、画面风格（电影感/慢动作/航拍等）
5. 遇到 `FAILURE` 时，等待 1～2 分钟后重新提交即可，属上游偶发波动
6. **图生视频**：单图加 `image`、多图加 `images` 数组（最多 8 张），参考图**一律用 base64 data-URI 传**；模型 / 轮询 / 下载 / 价格与文生视频完全一致（按模型时长计费，图生视频不额外加价）
7. **还在用旧版「图片直链」方式的用户请尽快改为 base64**：直链需要上游联网抓图，会报「参考图抓取失败：上游图片服务暂时不可用」——把图转成 base64 data-URI 传即可彻底解决，不要原样反复重试
8. **base64 前缀不能少**：`data:image/jpeg;base64,`（png 用 `data:image/png;base64,`），只放裸 base64 会失败

---

## 五、Prompt 示例参考

| 场景 | Prompt 示例 |
|---|---|
| 产品展示 | 一款白色智能手表在大理石桌面上旋转展示，高清，商业摄影风格 |
| 自然风景 | 青藏高原日出，云海翻涌，延时摄影，航拍视角，4K 电影感 |
| 人物动作 | 一位女舞者在空旷的舞台上独舞，慢动作，逆光，艺术感 |
| 城市夜景 | 上海外滩夜景，霓虹灯倒影在水面，延时摄影，广角 |
| 竖屏短视频 | 一杯热咖啡冒着白色蒸汽，特写，暖色调，适合竖屏（需加 ratio: 9:16） |
| 图生视频（运动描述） | 镜头缓慢推进，主体轻微动起来（眨眼 / 转头 / 头发随风飘动），背景轻微虚化流动 |
