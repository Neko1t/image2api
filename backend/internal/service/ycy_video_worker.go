package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"
	"time"

	"backend/internal/model"
	"backend/internal/provider/ycy"
	"backend/internal/repo"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrRequestIDRequired   = errors.New("request_id is required for YCY video generation")
	ErrIdempotencyConflict = errors.New("request_id was already used with different parameters")
	ErrYCYBindingAmbiguous = errors.New("model is bound to both YCY and another upstream adapter")
)

const (
	ycyJobType      = "ycy_video"
	ycyPollInterval = 15 * time.Second
	ycyWorkerTick   = 2 * time.Second
	ycyWorkerLease  = 60 * time.Second
	ycyJobDeadline  = 20 * time.Minute
	ycyWorkerLimit  = 4
)

// IsYCYVideoModel reports whether at least one active YCY account explicitly or
// implicitly serves this model. Only this route is moved to the durable worker.
func (s *V1Service) IsYCYVideoModel(ctx context.Context, modelID string) (bool, error) {
	items, err := s.upstreamActive(ctx, modelID)
	if err != nil {
		return false, err
	}
	hasYCY := false
	hasOther := false
	for _, item := range items {
		if upstreamAdapterType(&item) == "ycy" {
			hasYCY = true
		} else {
			hasOther = true
		}
	}
	if hasYCY && hasOther {
		return false, ErrYCYBindingAmbiguous
	}
	return hasYCY, nil
}

func (s *V1Service) StartSessionYCYVideoJob(ctx context.Context, principal *APIPrincipal, in V1VideoRequest, requestID string) (map[string]any, error) {
	ctx = context.WithoutCancel(ctx)
	requestID = strings.TrimSpace(requestID)
	if _, err := uuid.Parse(requestID); err != nil {
		return nil, ErrRequestIDRequired
	}
	if err := s.checkBannedPrompt(ctx, principal, in.Prompt); err != nil {
		s.logRejectedEvent(ctx, "video", in.Model, principal, in.Prompt, "user", err.Error())
		return nil, err
	}

	modelItem, resolution, aspectRatio, duration, _, err := s.prepareVideo(ctx, principal, in, false)
	if err != nil {
		s.logRejectedEvent(ctx, "video", in.Model, principal, in.Prompt, "user", err.Error())
		return nil, err
	}
	bound, err := s.IsYCYVideoModel(ctx, modelItem.ID)
	if err != nil {
		return nil, err
	}
	if !bound {
		return nil, ErrNoProviderAccount
	}
	agent := principal != nil && principal.User != nil && principal.User.Role == "agent"
	price, ok := modelPrice(modelItem, "video", resolution, duration, agent)
	if !ok {
		return nil, ErrUnsupportedParams
	}
	refs, err := decodeReferenceImages(in.ReferenceImages, max(1, modelItem.MaxReferenceImages))
	if err != nil {
		return nil, err
	}
	payloadHash := hashYCYVideoPayload(modelItem.ID, in.Prompt, aspectRatio, resolution, duration, refs)
	userID := ""
	if principal != nil && principal.User != nil {
		userID = principal.User.ID
	}
	if existing, err := s.events.GetYCYJobByRequest(ctx, userID, requestID); err != nil {
		return nil, err
	} else if existing != nil {
		if existing.PayloadHash != payloadHash {
			return nil, ErrIdempotencyConflict
		}
		return ycyUserJobResponse(existing, principalCredits(principal), true), nil
	}

	eventID := "evt-" + randomUpper(12)
	_, relativePath := s.allocateOutput(principal, "mp4", in.BaseURL)
	refFiles, err := s.persistYCYReferences(ctx, eventID, principal, refs)
	if err != nil {
		return nil, err
	}
	cleanupRefs := func() { s.cleanupYCYReferences(context.Background(), eventID, refFiles) }
	if !s.userAcquire(ctx, principal.User, eventID) {
		cleanupRefs()
		return nil, ErrUserConcurrencyFull
	}

	now := time.Now()
	event := &model.EventLog{
		ID: eventID, TS: now, Kind: "video", Status: "pending", Model: modelItem.ID,
		Provider: modelItem.Provider, Prompt: strings.TrimSpace(in.Prompt), Ratio: aspectRatio,
		Resolution: resolution, Duration: duration, Refs: len(refs), Source: "user",
		UserID: userID, Cost: price, File: relativePath, RequestID: requestID,
		PayloadHash: payloadHash, JobType: ycyJobType, JobStage: "queued",
		NextPollAt: &now, CreatedAt: now, UpdatedAt: now,
	}
	if len(refFiles) > 0 {
		event.RefFiles = jsonArray(refFiles)
	}
	created, err := s.events.CreateYCYJob(ctx, event)
	if err != nil {
		s.userRelease(ctx, userID, eventID)
		cleanupRefs()
		switch {
		case errors.Is(err, repo.ErrIdempotencyConflict):
			return nil, ErrIdempotencyConflict
		case errors.Is(err, repo.ErrInsufficientCredits):
			return nil, ErrInsufficientFunds
		default:
			return nil, err
		}
	}
	if principal != nil && principal.User != nil {
		principal.User.Credits = created.Credits
	}
	if !created.Created {
		s.userRelease(ctx, userID, eventID)
		cleanupRefs()
		return ycyUserJobResponse(created.Event, created.Credits, true), nil
	}
	return ycyUserJobResponse(created.Event, created.Credits, false), nil
}

func hashYCYVideoPayload(modelID, prompt, ratio, resolution, duration string, refs [][]byte) string {
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

func ycyUserJobResponse(item *model.EventLog, credits float64, replayed bool) map[string]any {
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

func (s *V1Service) persistYCYReferences(ctx context.Context, eventID string, principal *APIPrincipal, refs [][]byte) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if s.store == nil || !s.store.Configured() {
		return nil, errors.New("storage is not configured")
	}
	keys := make([]string, 0, len(refs))
	for i, ref := range refs {
		ext := imageExtFromBytes(ref)
		key := filepath.ToSlash(filepath.Join(s.userDir(principal), ".jobs", eventID, fmt.Sprintf("ref-%02d.%s", i+1, ext)))
		contentType := "image/" + strings.TrimPrefix(ext, ".")
		if ext == "jpg" || ext == "jpeg" {
			contentType = "image/jpeg"
		}
		if err := s.store.Put(ctx, key, ref, contentType); err != nil {
			s.cleanupYCYReferences(context.Background(), eventID, keys)
			return nil, fmt.Errorf("reference upload failed: %w", err)
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (s *V1Service) cleanupYCYReferences(ctx context.Context, eventID string, keys []string) {
	if s.store != nil {
		for _, key := range keys {
			_ = s.store.Delete(ctx, key)
		}
	}
	if eventID != "" {
		_ = s.events.ClearRefFiles(ctx, eventID)
	}
}

func (s *V1Service) readYCYReferences(ctx context.Context, item *model.EventLog) ([][]byte, []string, error) {
	if item == nil || len(item.RefFiles) == 0 {
		return nil, nil, nil
	}
	if s.store == nil || !s.store.Configured() {
		return nil, nil, errors.New("storage is not configured")
	}
	var keys []string
	if err := json.Unmarshal(item.RefFiles, &keys); err != nil {
		return nil, nil, err
	}
	refs := make([][]byte, 0, len(keys))
	for _, key := range keys {
		resp, err := s.store.Get(ctx, key, "")
		if err != nil {
			return nil, keys, err
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, keys, readErr
		}
		if resp.StatusCode/100 != 2 || len(data) == 0 {
			return nil, keys, fmt.Errorf("reference %s unavailable: status %d", key, resp.StatusCode)
		}
		refs = append(refs, data)
	}
	return refs, keys, nil
}

func (s *V1Service) ycyAdapter() (*ycy.Adapter, error) {
	impl, ok := s.upstreamAdapters["ycy"]
	if !ok {
		return nil, errors.New("YCY adapter is not registered")
	}
	result, ok := impl.(*ycy.Adapter)
	if !ok {
		return nil, errors.New("YCY adapter has an unexpected implementation")
	}
	return result, nil
}

// RunYCYVideoWorker advances durable YCY user jobs until the application context
// is cancelled. PostgreSQL claims make this safe to run in every backend replica.
func (s *V1Service) RunYCYVideoWorker(ctx context.Context) {
	ticker := time.NewTicker(ycyWorkerTick)
	defer ticker.Stop()
	sem := make(chan struct{}, ycyWorkerLimit)
	dispatch := func() {
		for len(sem) < cap(sem) {
			owner := "ycyw-" + randomUpper(16)
			item, err := s.events.ClaimDueYCYJob(ctx, owner, time.Now(), ycyWorkerLease)
			if err != nil {
				log.Printf("ycy worker: claim: %v", err)
				return
			}
			if item == nil {
				return
			}
			sem <- struct{}{}
			go func(job *model.EventLog, leaseOwner string) {
				defer func() { <-sem }()
				s.processYCYVideoJob(ctx, job, leaseOwner)
			}(item, owner)
		}
	}
	dispatch()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dispatch()
		}
	}
}

func (s *V1Service) processYCYVideoJob(appCtx context.Context, item *model.EventLog, owner string) {
	if item == nil {
		return
	}
	heartbeatCtx, stopHeartbeat := context.WithCancel(appCtx)
	defer stopHeartbeat()
	go func() {
		ticker := time.NewTicker(ycyWorkerLease / 3)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				_ = s.events.ExtendYCYLease(heartbeatCtx, item.ID, owner, time.Now().Add(ycyWorkerLease))
			}
		}
	}()
	if time.Since(item.TS) > ycyJobDeadline {
		s.failYCYJob(appCtx, item, "YCY generation timed out")
		return
	}

	switch item.JobStage {
	case "queued", "":
		s.submitYCYJob(appCtx, item, owner)
	case "submitting":
		// YCY has no documented idempotency key or client-key lookup. Resubmitting
		// this ambiguous state could create a second charged upstream task.
		s.failYCYJob(appCtx, item, "submission_outcome_unknown: task id was not persisted")
	case "polling":
		s.pollYCYJob(appCtx, item, owner)
	case "storing":
		s.storeYCYJob(appCtx, item, owner)
	default:
		s.failYCYJob(appCtx, item, "invalid YCY job stage: "+item.JobStage)
	}
}

func (s *V1Service) submitYCYJob(ctx context.Context, item *model.EventLog, owner string) {
	user, err := s.users.GetByID(ctx, item.UserID)
	if err != nil {
		s.failYCYJob(ctx, item, "load user: "+err.Error())
		return
	}
	if !s.userAcquire(ctx, user, item.ID) {
		s.scheduleYCYJob(ctx, item, owner, 3*time.Second, false, "")
		return
	}
	accounts, err := s.tokens.ListByPool(ctx, "ycy")
	if err != nil {
		s.scheduleYCYJob(ctx, item, owner, 5*time.Second, true, err.Error())
		return
	}
	active := make([]model.TokenAccount, 0, len(accounts))
	for _, account := range accounts {
		if upstreamAdapterType(&account) == "ycy" && upstreamAccountServes(account, item.Model) {
			active = append(active, account)
		}
	}
	s.rotateRoundRobin("ycy", active)
	var selected *model.TokenAccount
	for i := range active {
		account := &active[i]
		inflight, countErr := s.events.PendingYCYByAccount(ctx, account.ID, item.ID)
		if countErr != nil || inflight >= int64(accountConcurrency(*account)) {
			continue
		}
		if s.acctAcquire(ctx, account.ID, item.ID, accountConcurrency(*account)) {
			selected = account
			break
		}
	}
	if selected == nil {
		s.scheduleYCYJob(ctx, item, owner, 3*time.Second, false, "")
		return
	}
	ok, err := s.events.UpdateYCYClaim(ctx, item.ID, owner, map[string]any{
		"job_stage": "submitting", "account_id": selected.ID,
		"account_email": selected.AccountEmail, "next_poll_at": time.Now(),
	})
	if err != nil || !ok {
		s.acctRelease(ctx, selected.ID, item.ID)
		return
	}
	item.JobStage = "submitting"
	item.AccountID = selected.ID
	item.AccountEmail = selected.AccountEmail
	refs, _, err := s.readYCYReferences(ctx, item)
	if err != nil {
		s.failYCYJob(ctx, item, "load references: "+err.Error())
		return
	}
	impl, err := s.ycyAdapter()
	if err != nil {
		s.failYCYJob(ctx, item, err.Error())
		return
	}
	baseURL := strings.TrimSpace(stringValue(selected.Meta["base_url"]))
	submitCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	taskID, err := impl.CreateVideoTask(submitCtx, baseURL, selected.Value, item.Prompt,
		upstreamVideoSize(item.Ratio, item.Resolution), parseDurationSeconds(item.Duration), refs)
	cancel()
	if err != nil {
		s.failYCYJob(ctx, item, "YCY submit failed: "+err.Error())
		return
	}
	next := time.Now().Add(ycyPollInterval)
	ok, err = s.events.UpdateYCYClaim(ctx, item.ID, owner, map[string]any{
		"upstream_task_id": taskID, "job_stage": "polling", "next_poll_at": next,
		"lease_owner": "", "lease_until": nil, "job_attempts": 0,
	})
	if err != nil || !ok {
		log.Printf("ycy worker: task accepted but task id persistence failed event=%s task_id=%s err=%v", item.ID, taskID, err)
		return
	}
	log.Printf("ycy worker: submitted event=%s task_id=%s account=%s", item.ID, taskID, selected.ID)
}

func (s *V1Service) pollYCYJob(ctx context.Context, item *model.EventLog, owner string) {
	if item.UpstreamTaskID == "" {
		s.failYCYJob(ctx, item, "invalid polling state: missing YCY task id")
		return
	}
	account, err := s.tokens.Get(ctx, "ycy", item.AccountID)
	if err != nil {
		s.failYCYJob(ctx, item, "load YCY account: "+err.Error())
		return
	}
	if user, userErr := s.users.GetByID(ctx, item.UserID); userErr == nil {
		_ = s.userAcquire(ctx, user, item.ID)
	}
	_ = s.acctAcquire(ctx, account.ID, item.ID, accountConcurrency(*account))
	impl, err := s.ycyAdapter()
	if err != nil {
		s.failYCYJob(ctx, item, err.Error())
		return
	}
	baseURL := strings.TrimSpace(stringValue(account.Meta["base_url"]))
	pollCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	status, contentURL, err := impl.GetVideoTask(pollCtx, baseURL, account.Value, item.UpstreamTaskID)
	cancel()
	if err != nil {
		s.scheduleYCYJob(ctx, item, owner, ycyRetryDelay(item.JobAttempts), true, err.Error())
		return
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "SUCCESS":
		if contentURL == "" {
			contentURL = strings.TrimRight(baseURL, "/") + "/v1/videos/" + item.UpstreamTaskID + "/content"
		}
		ok, updateErr := s.events.UpdateYCYClaim(ctx, item.ID, owner, map[string]any{
			"job_stage": "storing", "upstream_result_url": contentURL, "next_poll_at": time.Now(),
		})
		if updateErr != nil || !ok {
			return
		}
		item.JobStage = "storing"
		item.UpstreamResultURL = contentURL
		s.storeYCYJob(ctx, item, owner)
	case "FAILURE", "FAILED":
		s.failYCYJob(ctx, item, "YCY task failed: "+item.UpstreamTaskID)
	default:
		s.scheduleYCYJob(ctx, item, owner, ycyPollInterval, false, "")
	}
}

func (s *V1Service) storeYCYJob(ctx context.Context, item *model.EventLog, owner string) {
	if item.UpstreamTaskID == "" || item.AccountID == "" {
		s.failYCYJob(ctx, item, "invalid storing state: missing task or account")
		return
	}
	if s.store == nil || !s.store.Configured() {
		s.failYCYJob(ctx, item, "storage is not configured")
		return
	}
	account, err := s.tokens.Get(ctx, "ycy", item.AccountID)
	if err != nil {
		s.failYCYJob(ctx, item, "load YCY account: "+err.Error())
		return
	}
	impl, err := s.ycyAdapter()
	if err != nil {
		s.failYCYJob(ctx, item, err.Error())
		return
	}
	contentURL := strings.TrimSpace(item.UpstreamResultURL)
	if contentURL == "" {
		baseURL := strings.TrimRight(strings.TrimSpace(stringValue(account.Meta["base_url"])), "/")
		contentURL = baseURL + "/v1/videos/" + item.UpstreamTaskID + "/content"
	}
	downloadCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	videoBytes, _, err := impl.DownloadVideoTask(downloadCtx, contentURL, account.Value)
	cancel()
	if err != nil {
		s.scheduleYCYJob(ctx, item, owner, ycyRetryDelay(item.JobAttempts), true, err.Error())
		return
	}
	if err := s.store.Put(ctx, item.File, videoBytes, "video/mp4"); err != nil {
		s.scheduleYCYJob(ctx, item, owner, ycyRetryDelay(item.JobAttempts), true, err.Error())
		return
	}
	if thumb, last, frameErr := extractVideoFrames(ctx, videoBytes); frameErr == nil {
		if len(thumb) > 0 {
			_ = s.store.Put(ctx, ThumbKey(item.File), thumb, "image/jpeg")
		}
		if len(last) > 0 {
			_ = s.store.Put(ctx, LastFrameKey(item.File), last, "image/jpeg")
		}
	}
	elapsedMS := int(time.Since(item.TS).Milliseconds())
	transitioned, err := s.events.CompleteYCYJob(ctx, item.ID, elapsedMS)
	if err != nil {
		return
	}
	if transitioned {
		_, _ = s.tokens.Update(ctx, "ycy", account.ID, map[string]any{
			"last_used_at": time.Now(), "success_total": gorm.Expr("success_total + 1"), "fails": 0,
		})
		if user, userErr := s.users.GetByID(ctx, item.UserID); userErr == nil {
			_ = s.maybeGrantInviteReward(ctx, &APIPrincipal{User: user, TokenType: "session"})
		}
	}
	s.finishYCYResources(ctx, item)
	log.Printf("ycy worker: completed event=%s task_id=%s", item.ID, item.UpstreamTaskID)
}

func (s *V1Service) scheduleYCYJob(ctx context.Context, item *model.EventLog, owner string, delay time.Duration, incrementAttempt bool, errMsg string) {
	next := time.Now().Add(delay)
	patch := map[string]any{"next_poll_at": next, "lease_owner": "", "lease_until": nil}
	if incrementAttempt {
		patch["job_attempts"] = gorm.Expr("job_attempts + 1")
	}
	if strings.TrimSpace(errMsg) != "" {
		patch["error"] = strings.TrimSpace(errMsg)
	}
	_, _ = s.events.UpdateYCYClaim(ctx, item.ID, owner, patch)
}

func ycyRetryDelay(attempts int) time.Duration {
	delay := 15 * time.Second
	for i := 0; i < attempts && delay < 60*time.Second; i++ {
		delay *= 2
	}
	if delay > 60*time.Second {
		delay = 60 * time.Second
	}
	return delay
}

func (s *V1Service) failYCYJob(ctx context.Context, item *model.EventLog, message string) {
	if item == nil {
		return
	}
	_, err := s.events.FailYCYJobAndRefund(ctx, item.ID, message, int(time.Since(item.TS).Milliseconds()))
	if err != nil {
		log.Printf("ycy worker: fail transition event=%s: %v", item.ID, err)
		return
	}
	if item.AccountID != "" {
		_ = s.tokens.IncrementFail(ctx, item.AccountID)
	}
	s.finishYCYResources(ctx, item)
	log.Printf("ycy worker: failed event=%s task_id=%s error=%s", item.ID, item.UpstreamTaskID, message)
}

func (s *V1Service) finishYCYResources(ctx context.Context, item *model.EventLog) {
	s.userRelease(ctx, item.UserID, item.ID)
	if item.AccountID != "" {
		s.acctRelease(ctx, item.AccountID, item.ID)
	}
	var keys []string
	_ = json.Unmarshal(item.RefFiles, &keys)
	s.cleanupYCYReferences(ctx, item.ID, keys)
}
