# YCY Adapter 接口调用修复

**Date**: 2026-07-03  
**Type**: Bug Fix  
**Priority**: High

## 问题描述

在实施 upstream adapter 架构时，YCY adapter 的引用图片传递方式与 YCY API 文档要求不符。

### 错误实现

```go
// ❌ 错误：上传到 RustFS 获取 URL
refURLs := make([]string, len(refs))
for i, refBytes := range refs {
    tempPath := fmt.Sprintf("temp/ycy-ref-%s-%d.png", randomID(12), i)
    a.store.Put(ctx, tempPath, refBytes, "image/png")
    refURLs[i] = a.store.PublicURL(tempPath)
}
a.Client.GenerateVideo(ctx, baseURL, apiKey, model, prompt, ratio, refURLs, downloadResult)
```

**问题**：
- YCY API 需要 **base64 data-URI** 格式（如 `data:image/jpeg;base64,...`）
- 实现却上传到 RustFS 并传递 URL
- 导致 YCY 上游报错"参考图抓取失败"

### 正确实现

```go
// ✅ 正确：直接编码为 base64 data-URI
refDataURIs := make([]string, 0, len(refs))
for _, refBytes := range refs {
    if len(refBytes) == 0 {
        continue
    }
    dataURI := bytesToDataURI(refBytes) // "data:image/jpeg;base64,/9j/..."
    refDataURIs = append(refDataURIs, dataURI)
}
a.Client.GenerateVideo(ctx, baseURL, apiKey, model, prompt, ratio, refDataURIs, downloadResult)
```

## API 文档要求

根据 YCY API 文档（`YCYAPI视频生成接口使用说明 (2).md`）：

### 单图生视频
```json
{
  "model": "video-v1-5s",
  "prompt": "镜头缓缓推进，猫咪眨眼并轻轻转头",
  "image": "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQ..."
}
```

### 多图生视频
```json
{
  "model": "video-v1-15s",
  "prompt": "镜头平滑运镜，画面依次自然过渡",
  "images": [
    "data:image/jpeg;base64,/9j/4AAQ...（第 1 张）",
    "data:image/jpeg;base64,/9j/4AAQ...（第 2 张）",
    "data:image/jpeg;base64,/9j/4AAQ...（第 3 张）"
  ]
}
```

**关键点**：
1. 必须使用 `data:image/jpeg;base64,` 前缀
2. 不能只传裸 base64，会失败
3. 支持最多 8 张图片
4. PNG 图片用 `data:image/png;base64,` 前缀

## 修复内容

### 1. 新增 `bytesToDataURI` 函数

**文件**: `backend/internal/provider/ycy/adapter.go`

```go
// bytesToDataURI converts image bytes to a base64 data-URI string.
// Detects image format from magic bytes and uses appropriate MIME type.
func bytesToDataURI(data []byte) string {
    if len(data) == 0 {
        return ""
    }

    // Detect image format from magic bytes
    var mimeType string
    switch {
    case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
        mimeType = "image/jpeg"
    case len(data) >= 8 && string(data[1:4]) == "PNG":
        mimeType = "image/png"
    case len(data) >= 4 && string(data[0:4]) == "RIFF":
        mimeType = "image/webp"
    default:
        // Default to JPEG if unknown
        mimeType = "image/jpeg"
    }

    // Encode to base64 with proper data-URI prefix
    encoded := base64.StdEncoding.EncodeToString(data)
    return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)
}
```

**特点**：
- 自动检测图片格式（JPEG/PNG/WebP）
- 使用正确的 MIME 类型
- 包含完整的 data-URI 前缀

### 2. 简化 `GenerateVideo` 方法

**文件**: `backend/internal/provider/ycy/adapter.go`

**Before**:
```go
func (a *Adapter) GenerateVideo(...) {
    // 上传到临时存储
    tempPaths := make([]string, len(refs))
    for i, refBytes := range refs {
        tempPath := fmt.Sprintf("temp/ycy-ref-%s-%d.png", randomID(12), i)
        a.store.Put(ctx, tempPath, refBytes, "image/png")
        refURLs[i] = a.store.PublicURL(tempPath)
    }
    defer a.cleanupTempRefs(ctx, tempPaths)
    // ...
}
```

**After**:
```go
func (a *Adapter) GenerateVideo(...) {
    // 直接转换为 data-URI
    refDataURIs := make([]string, 0, len(refs))
    for _, refBytes := range refs {
        if len(refBytes) == 0 {
            continue
        }
        dataURI := bytesToDataURI(refBytes)
        refDataURIs = append(refDataURIs, dataURI)
    }
    // 无需清理，无临时文件
}
```

### 3. 移除不需要的依赖

**文件**: `backend/internal/provider/ycy/adapter.go`

- ✅ 移除 `storage *storage.Client` 字段
- ✅ 移除 `cleanupTempRefs()` 方法
- ✅ 移除 `import "backend/internal/storage"`
- ✅ 添加 `import "encoding/base64"`

**文件**: `backend/internal/bootstrap/app.go`

```go
// Before
upstreamAdapters := map[string]service.UpstreamAdapter{
    "openai": custom.NewAdapter(),
    "ycy":    ycy.NewAdapter(rustfsClient), // 需要 storage
}

// After
upstreamAdapters := map[string]service.UpstreamAdapter{
    "openai": custom.NewAdapter(),
    "ycy":    ycy.NewAdapter(), // 不再需要 storage
}
```

## 改进效果

### 性能提升
- ❌ 旧方案：上传临时文件 → 获取 URL → 生成 → 清理文件（4 步，2 次网络 IO）
- ✅ 新方案：内存编码 → 生成（1 步，0 次额外 IO）

### 可靠性提升
- ❌ 旧方案：依赖 RustFS 可用性，临时文件可能泄漏
- ✅ 新方案：纯内存操作，无临时状态

### 符合文档
- ✅ 完全符合 YCY API 文档要求
- ✅ 支持单图 `image` 和多图 `images` 两种模式
- ✅ 自动检测图片格式并使用正确 MIME 类型

## 测试建议

### 单元测试
```go
func TestBytesToDataURI(t *testing.T) {
    // JPEG magic bytes
    jpegData := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
    dataURI := bytesToDataURI(jpegData)
    assert.True(t, strings.HasPrefix(dataURI, "data:image/jpeg;base64,"))
    
    // PNG magic bytes
    pngData := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
    dataURI = bytesToDataURI(pngData)
    assert.True(t, strings.HasPrefix(dataURI, "data:image/png;base64,"))
}
```

### 集成测试
1. 使用真实 YCY API Key
2. 准备一张测试图片
3. 调用图生视频接口
4. 验证能够成功提交任务并返回 `task_id`

### 验证要点
- [ ] 单图生视频：`image` 字段包含完整 data-URI
- [ ] 多图生视频：`images` 数组每个元素都是 data-URI
- [ ] JPEG/PNG/WebP 三种格式都能正确识别
- [ ] 生成成功后能正常下载视频
- [ ] 无临时文件残留

## 向后兼容性

- ✅ 接口签名未变：`GenerateVideo(... refs [][]byte ...)`
- ✅ 对调用方透明：service 层无需修改
- ✅ 已有代码继续工作：custom adapter 不受影响

## 相关文件

| 文件 | 改动 |
|---|---|
| `backend/internal/provider/ycy/adapter.go` | 重写 GenerateVideo，添加 bytesToDataURI |
| `backend/internal/bootstrap/app.go` | 移除 NewAdapter 的 storage 参数 |

## 参考资料

- [YCYAPI视频生成接口使用说明 (2).md](../../YCYAPI视频生成接口使用说明%20(2).md)
- [Upstream Adapter Architecture Refactor](./2026-07-03-upstream-adapter-refactor.md)

---

**Status**: ✅ Fixed  
**Reviewed By**: -  
**Deployed**: Pending verification
