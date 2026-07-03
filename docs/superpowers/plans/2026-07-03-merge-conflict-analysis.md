# 上游更新与本地改动冲突分析

**Date**: 2026-07-03  
**Status**: 需要人工决策  
**Upstream Commits**: 21 个新提交 (9be5c1b..f9e72c0)

## 概况

- **上游变更**: 36 个文件，+1337/-360 行
- **本地改动**: 14 个已修改文件 + 7 个新文件
- **冲突风险**: ⚠️ 高（多个核心文件同时被修改）

---

## 上游主要更新

### 1. Bug 修复和优化 (f9e72c0)
- `backend/internal/service/v1.go`: 142 行变更
- `backend/internal/service/tokens.go`: 69 行变更
- `backend/internal/provider/adobe/client.go`: 32 行变更
- `backend/internal/provider/grok/client.go`: 32 行变更
- `backend/internal/service/maintenance.go`: 18 行变更

### 2. 新功能：缩略图生成 (273892f)
- 新增 `backend/internal/service/thumbnail.go` (71 行)
- 新增 `backend/internal/service/video_frames.go` (76 行)
- 修改 `v1.go` 添加缩略图提取逻辑

### 3. OpenAI 风控协议更新 (39b149b)
- `backend/internal/provider/chatgpt/client.go`: +50 行
- `backend/internal/provider/chatgpt/util.go`: +87 行

### 4. Grok 视频修复和重试 (ab94165, c99c134, 41bd120)
- `backend/internal/provider/grok/video.go`: 大幅重构
- `backend/internal/provider/grok/client.go`: +90 行

### 5. 尺寸兼容性更新 (53dc178, 1f205fc)
- `backend/internal/service/v1.go`: 添加 `snapRatio` 逻辑
- 修改分辨率验证

### 6. 提示词限制调整 (c5a059d, 1de4fe9)
- 移除 1500 字限制
- 调整最大字数

### 7. Adobe 限流处理 (7a168a1)
- Adobe 限流不再直接封号
- 增加上传重试 (dde178f)

---

## 本地改动（Adapter 架构重构）

### 新增文件
1. `backend/internal/service/upstream_adapter.go` - 接口定义
2. `backend/internal/provider/custom/adapter.go` - OpenAI adapter
3. `backend/internal/provider/ycy/adapter.go` - YCY adapter
4. `backend/internal/provider/ycy/client.go` - YCY client
5. `docs/superpowers/plans/*.md` - 3 个文档

### 修改文件
1. `backend/internal/service/v1.go` - 核心调度逻辑重构
2. `backend/internal/service/tokens.go` - 添加 WithAdapter 方法
3. `backend/internal/bootstrap/app.go` - adapter registry
4. `backend/internal/http/handler/provider_admin.go` - adapter_type 处理
5. `frontend/src/components/UpstreamModal.vue` - UI 选择框

---

## 关键冲突点

### 🔴 高冲突：`backend/internal/service/v1.go`

**上游改动**：
- 添加 `snapRatio()` 逻辑（行 1039）
- 添加缩略图生成（行 518-521, 662-673）
- 修改 `prepareVideo()` 添加 ycy 检查（行 1037-1043）
- 其他 bug 修复

**本地改动**：
- 移除 `custom *custom.Client` 字段
- 添加 `upstreamAdapters map[string]UpstreamAdapter`
- 添加 `dispatchUpstreamImage/Video()` 方法（~200 行）
- 添加 `upstreamActive()`, `upstreamAdapterType()` 等辅助方法
- 三处 switch 从 `case "custom"` 改为 `case "upstream"`
- 修改 `effectiveProvider()` 返回 "upstream"

**冲突性质**：
- ❌ 上游在 `prepareVideo()` 添加了 `s.ycyActive()` 调用，但我们已删除这个方法
- ❌ 上游添加的缩略图逻辑在我们删除的代码段中
- ✅ 上游的 `snapRatio()` 不冲突（纯新增）

### 🟡 中冲突：`backend/internal/service/tokens.go`

**上游改动**：
- `ImportCustomAccount()` 方法可能有修改
- `ImportYCYAccount()` 方法可能有修改
- 其他账号管理逻辑

**本地改动**：
- 添加 `ImportCustomAccountWithAdapter()`
- 添加 `ImportYCYAccountWithAdapter()`

**冲突性质**：
- ⚠️ 如果上游修改了原方法签名或逻辑，我们的包装器可能需要同步

### 🟢 低冲突：`backend/internal/bootstrap/app.go`

**上游改动**：可能无或很少

**本地改动**：
- 移除 `customClient := custom.NewClient()`
- 移除 `ycyClient := ycy.NewClient()`
- 添加 `upstreamAdapters` map
- 修改 `NewV1Service()` 调用

**冲突性质**：
- ✅ 结构性改动，合并时需手动调整

### 🟢 低冲突：`backend/internal/http/handler/provider_admin.go`

**上游改动**：未知

**本地改动**：
- `ImportCustomAccount` 添加 `AdapterType` 字段
- `ImportYCYAccount` 添加 `AdapterType` 字段

**冲突性质**：
- ✅ 如果上游未改这两个方法，无冲突

---

## 合并策略建议

### 方案 A：渐进合并（推荐 ⭐）

```bash
# 1. 先提交我们的改动到独立分支
git checkout -b feature/upstream-adapter
git add backend/internal/provider/custom/adapter.go
git add backend/internal/provider/ycy/
git add backend/internal/service/upstream_adapter.go
git add backend/internal/service/v1.go
git add backend/internal/service/tokens.go
git add backend/internal/bootstrap/app.go
git add backend/internal/http/handler/provider_admin.go
git add frontend/src/components/UpstreamModal.vue
git add docs/
git commit -m "feat: upstream adapter architecture

- Add UpstreamAdapter interface
- Implement OpenAI and YCY adapters  
- Unify dispatch (6→4 cases)
- Fix YCY base64 data-URI format"

# 2. 切回 main 更新上游
git checkout main
git pull origin main

# 3. 在最新 main 上重新应用我们的改动
git checkout feature/upstream-adapter
git rebase main
# 解决冲突...

# 4. 测试通过后合并回 main
git checkout main
git merge feature/upstream-adapter
```

### 方案 B：直接合并（风险高 ⚠️）

```bash
git stash push -m "adapter architecture WIP"
git pull origin main
git stash pop
# 解决大量冲突...
```

---

## 手动解决冲突的关键点

### 1. `v1.go` 中的 `snapRatio` 调用

**上游添加**（行 1039）：
```go
aspectRatio = snapRatio(aspectRatio, repo.JSONStrings(modelItem.Ratios))
```

**处理**：✅ 保留，这是上游的 bug 修复

### 2. 缩略图生成逻辑

**上游添加**（v1.go 行 518-521）：
```go
if thumb, terr := makeThumbnail(imageBytes); terr == nil {
    _ = s.store.Put(genCtx, ThumbKey(relativePath), thumb, "image/jpeg")
}
```

**处理**：✅ 在我们的 `dispatchUpstreamImage` 成功后添加这段

### 3. 视频帧提取逻辑

**上游添加**（v1.go 行 662-673）：
```go
if thumb, last, terr := extractVideoFrames(genCtx, videoBytes); terr == nil {
    if len(thumb) > 0 {
        _ = s.store.Put(genCtx, ThumbKey(relativePath), thumb, "image/jpeg")
    }
    if len(last) > 0 {
        _ = s.store.Put(genCtx, LastFrameKey(relativePath), last, "image/jpeg")
    }
}
```

**处理**：✅ 在我们的 `dispatchUpstreamVideo` 成功后添加这段

### 4. `prepareVideo` 中的 ycy 检查

**上游添加**（v1.go 行 1037-1043）：
```go
if s.effectiveProvider(ctx, modelItem) == "ycy" {
    active, err := s.ycyActive(ctx, modelItem.ID)
    if err != nil {
        return nil, "", "", "", 0, err
    }
    if len(active) == 0 {
        return nil, "", "", "", 0, ErrNoProviderAccount
    }
}
```

**处理**：❌ 删除！我们的 `upstreamActive()` 已统一处理

### 5. `effectiveProvider` 返回值

**上游期望**：
```go
if eff := s.effectiveProvider(ctx, modelItem); eff == "custom" || eff == "ycy"
```

**我们的改动**：
```go
if eff := s.effectiveProvider(ctx, modelItem); eff == "upstream"
```

**处理**：✅ 改为我们的 `"upstream"`，并更新所有调用方

---

## 测试清单

合并后必须验证：

- [ ] 编译通过
- [ ] Custom (OpenAI) 账号导入成功
- [ ] YCY 账号导入成功
- [ ] 图像生成（custom adapter）
- [ ] 视频生成（ycy adapter）
- [ ] 缩略图生成正常
- [ ] Grok 视频修复生效
- [ ] Adobe 限流处理正常
- [ ] 所有内置 provider 不受影响

---

## 预估工作量

- **冲突解决**: 2-3 小时
- **测试验证**: 1-2 小时
- **文档更新**: 0.5 小时
- **总计**: 约 4-6 小时

---

## 建议行动

1. ✅ 先在独立分支提交我们的改动（保留完整 commit）
2. ✅ 更新 main 到最新上游
3. ✅ rebase 我们的分支到最新 main
4. ⚠️ 手动解决冲突（参考本文档的"关键点"）
5. ✅ 全面测试
6. ✅ 合并到 main

**需要帮助解决冲突时，请提供具体文件和冲突片段。**
