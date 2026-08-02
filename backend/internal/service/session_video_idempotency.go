package service

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"backend/internal/model"
)

const sessionVideoJobType = "session_video"

func hashSessionVideoPayload(modelID, prompt, ratio, resolution, duration string, refs [][]byte) string {
	refHashes := make([]string, 0, len(refs))
	for _, ref := range refs {
		sum := sha256.Sum256(ref)
		refHashes = append(refHashes, hex.EncodeToString(sum[:]))
	}
	raw, _ := json.Marshal(struct {
		Version    int      `json:"version"`
		Model      string   `json:"model"`
		Prompt     string   `json:"prompt"`
		Ratio      string   `json:"ratio"`
		Resolution string   `json:"resolution"`
		Duration   string   `json:"duration"`
		References []string `json:"references"`
	}{1, strings.TrimSpace(modelID), strings.TrimSpace(prompt), strings.TrimSpace(ratio), strings.TrimSpace(resolution), strings.TrimSpace(duration), refHashes})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func sessionVideoResponse(item *model.EventLog, credits float64, replayed bool) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	status := item.Status
	if status == "pending" {
		status = "queued"
	}
	url := ""
	if item.Status == "success" && strings.TrimSpace(item.File) != "" {
		url = "/images/" + strings.ReplaceAll(strings.TrimSpace(item.File), "\\", "/")
	}
	return map[string]any{
		"event_id": item.ID, "request_id": item.RequestID, "kind": "video",
		"status": status, "job_stage": item.JobStage, "url": emptyOrNil(url),
		"error": emptyOrNil(item.Error), "charged": item.Cost, "credits": credits,
		"elapsed_ms": item.ElapsedMS, "replayed": replayed,
	}
}
