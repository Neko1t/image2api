package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"backend/internal/model"
)

var ErrAPIMediaGone = errors.New("media content is no longer available")

const apiMediaDiskReserve = int64(64 * 1024 * 1024)

var apiMediaSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func normalizeV1ImageResponseFormat(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "url", nil
	}
	if value != "url" && value != "b64_json" {
		return "", fmt.Errorf("%w: response_format must be url or b64_json", ErrUnsupportedParams)
	}
	return value, nil
}

// MediaDelivery is either an authenticated redirect or a response whose body
// must be proxied. Exactly one field is populated on success.
type MediaDelivery struct {
	RedirectURL string
	Response    *http.Response
}

func (s *V1Service) OpenAPIVideoContent(ctx context.Context, principal *APIPrincipal, id, rangeHeader string) (*MediaDelivery, error) {
	ev, err := s.apiMediaEventForUser(ctx, principal, id, "video")
	if err != nil {
		return nil, err
	}
	return s.openAPIMediaEvent(ctx, ev, "video", rangeHeader)
}

func (s *V1Service) OpenAPIImageContent(ctx context.Context, principal *APIPrincipal, id, rangeHeader string) (*MediaDelivery, error) {
	ev, err := s.apiMediaEventForUser(ctx, principal, id, "image")
	if err != nil {
		return nil, err
	}
	return s.openAPIMediaEvent(ctx, ev, "image", rangeHeader)
}

func (s *V1Service) apiMediaEventForUser(ctx context.Context, principal *APIPrincipal, id, kind string) (*model.EventLog, error) {
	if principal == nil || principal.User == nil {
		return nil, ErrVideoJobNotFound
	}
	ev, err := s.events.GetByIDForUser(ctx, strings.TrimSpace(id), principal.User.ID)
	if err != nil {
		return nil, err
	}
	if ev == nil || ev.Source != "v1" || ev.Kind != kind {
		return nil, ErrVideoJobNotFound
	}
	if ev.Status != "success" {
		return nil, ErrVideoNotReady
	}
	if strings.TrimSpace(ev.File) == "" {
		return nil, ErrAPIMediaGone
	}
	return ev, nil
}

func (s *V1Service) openAPIMediaEvent(ctx context.Context, ev *model.EventLog, kind, rangeHeader string) (*MediaDelivery, error) {
	file := strings.TrimSpace(ev.File)
	if isHTTPArtifactURL(file) {
		resp, err := s.openProviderArtifact(ctx, ev, file, rangeHeader)
		if err != nil {
			return nil, err
		}
		return &MediaDelivery{Response: resp}, nil
	}

	key, ok := validatedAPIMediaObjectKey(ev, kind)
	if !ok || s.store == nil || !s.store.Configured() {
		return nil, ErrAPIMediaGone
	}
	if s.cfg != nil && s.cfg.APIMediaDirectDelivery && s.store.DirectDeliveryEnabled() {
		exists, err := s.store.Exists(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("%w: check stored media", ErrProviderTemporary)
		}
		if !exists {
			return nil, ErrAPIMediaGone
		}
		signedURL, err := s.store.PresignGet(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("%w: authorize stored media", ErrProviderTemporary)
		}
		return &MediaDelivery{RedirectURL: signedURL}, nil
	}

	resp, err := s.store.Get(ctx, key, strings.TrimSpace(rangeHeader))
	if err != nil {
		return nil, fmt.Errorf("%w: fetch stored media", ErrProviderTemporary)
	}
	if resp.StatusCode == http.StatusNotFound {
		resp.Body.Close()
		return nil, ErrAPIMediaGone
	}
	return &MediaDelivery{Response: resp}, nil
}

func validatedAPIMediaObjectKey(ev *model.EventLog, kind string) (string, bool) {
	if ev == nil || !apiMediaSegmentPattern.MatchString(ev.UserID) || !apiMediaSegmentPattern.MatchString(ev.ID) || (kind != "image" && kind != "video") {
		return "", false
	}
	key := strings.TrimLeft(strings.TrimSpace(ev.File), "/")
	if key == "" || isHTTPArtifactURL(key) || path.Clean(key) != key {
		return "", false
	}
	plural := kind + "s"
	prefix := "api/" + ev.UserID + "/" + plural + "/"
	name := strings.TrimPrefix(key, prefix)
	if !strings.HasPrefix(key, prefix) || name == "" || strings.Contains(name, "/") {
		return "", false
	}
	ext := strings.ToLower(path.Ext(name))
	if strings.TrimSuffix(name, ext) != ev.ID {
		return "", false
	}
	if kind == "image" && ext != ".png" && ext != ".jpg" && ext != ".webp" && ext != ".gif" {
		return "", false
	}
	if kind == "video" && ext != ".mp4" && ext != ".webm" {
		return "", false
	}
	return key, true
}

func isHTTPArtifactURL(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	return err == nil && u.IsAbs() && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func (s *V1Service) openProviderArtifact(ctx context.Context, ev *model.EventLog, rawURL, rangeHeader string) (*http.Response, error) {
	var account *model.TokenAccount
	if s.tokens != nil && strings.TrimSpace(ev.AccountID) != "" {
		account, _ = s.tokens.GetByID(ctx, ev.AccountID)
	}
	pool := strings.ToLower(strings.TrimSpace(ev.Provider))
	if account != nil && account.Pool != "" {
		pool = strings.ToLower(strings.TrimSpace(account.Pool))
	}

	switch pool {
	case "chatgpt":
		if account == nil || strings.TrimSpace(account.Value) == "" || s.chatgpt == nil {
			return nil, fmt.Errorf("%w: chatgpt account no longer available", ErrProviderTemporary)
		}
		if !trustedHTTPSArtifactHost(rawURL, "oaiusercontent.com", "chatgpt.com") {
			return nil, fmt.Errorf("%w: invalid chatgpt asset host", ErrProviderTemporary)
		}
		body, contentType, err := s.chatgpt.OpenAsset(ctx, account.Value, rawURL)
		if err != nil {
			return nil, err
		}
		return syntheticArtifactResponse(body, contentType), nil
	case "grok":
		if account == nil || strings.TrimSpace(account.Value) == "" || s.grok == nil {
			return nil, fmt.Errorf("%w: grok account no longer available", ErrProviderTemporary)
		}
		if !trustedHTTPSArtifactHost(rawURL, "grok.com") {
			return nil, fmt.Errorf("%w: invalid grok asset host", ErrProviderTemporary)
		}
		body, contentType, err := s.grok.OpenAsset(ctx, account.Value, rawURL)
		if err != nil {
			return nil, err
		}
		return syntheticArtifactResponse(body, contentType), nil
	case "custom", "ycy":
		if account == nil {
			return nil, fmt.Errorf("%w: upstream account no longer available", ErrProviderTemporary)
		}
		return openValidatedHTTPArtifact(ctx, rawURL, stringValue(account.Meta["base_url"]), account.Value, rangeHeader)
	default:
		return openValidatedHTTPArtifact(ctx, rawURL, "", "", rangeHeader)
	}
}

func syntheticArtifactResponse(body io.ReadCloser, contentType string) *http.Response {
	header := make(http.Header)
	if contentType = strings.TrimSpace(contentType); contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		Status:        "200 OK",
		StatusCode:    http.StatusOK,
		Header:        header,
		Body:          body,
		ContentLength: -1,
	}
}

func openValidatedHTTPArtifact(ctx context.Context, rawURL, trustedBaseURL, bearerToken, rangeHeader string) (*http.Response, error) {
	target, err := validateArtifactURL(ctx, rawURL, trustedBaseURL)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid artifact URL", ErrProviderTemporary)
	}
	trusted, _ := url.Parse(strings.TrimSpace(trustedBaseURL))
	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			// Artifact URLs are provider-controlled. Disable environment proxies
			// and bind validation to the actual dialed IP to prevent DNS rebinding.
			DialContext:           validatedArtifactDialer(trusted, net.DefaultResolver.LookupIPAddr),
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return errors.New("too many artifact redirects")
			}
			if _, err := validateArtifactURL(req.Context(), req.URL.String(), trustedBaseURL); err != nil {
				return err
			}
			if trusted == nil || !sameOrigin(req.URL, trusted) {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	if trusted != nil && sameOrigin(target, trusted) && strings.TrimSpace(bearerToken) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(strings.TrimPrefix(bearerToken, "Bearer ")))
	}
	if rangeHeader = strings.TrimSpace(rangeHeader); rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: fetch artifact", ErrProviderTemporary)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		return nil, fmt.Errorf("%w: artifact status %d", ErrProviderTemporary, resp.StatusCode)
	}
	return resp, nil
}

type artifactIPLookup func(context.Context, string) ([]net.IPAddr, error)

func validatedArtifactDialer(trusted *url.URL, lookup artifactIPLookup) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	trustedAddress := artifactURLAddress(trusted)
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid artifact dial address: %w", err)
		}
		if trustedAddress != "" && strings.EqualFold(net.JoinHostPort(strings.TrimSuffix(host, "."), port), trustedAddress) {
			return dialer.DialContext(ctx, network, address)
		}

		if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
			if restrictedArtifactIP(ip) {
				return nil, errors.New("restricted artifact dial address")
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		if lookup == nil {
			return nil, errors.New("artifact DNS resolver unavailable")
		}
		lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		addrs, err := lookup(lookupCtx, host)
		if err != nil || len(addrs) == 0 {
			return nil, errors.New("artifact host did not resolve")
		}
		for _, addr := range addrs {
			if restrictedArtifactIP(addr.IP) {
				return nil, errors.New("restricted artifact dial address")
			}
		}

		var lastErr error
		for _, addr := range addrs {
			if network == "tcp4" && addr.IP.To4() == nil {
				continue
			}
			if network == "tcp6" && addr.IP.To4() != nil {
				continue
			}
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		if lastErr == nil {
			lastErr = errors.New("artifact host has no address for requested network")
		}
		return nil, lastErr
	}
}

func artifactURLAddress(u *url.URL) string {
	if u == nil || u.Hostname() == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return ""
		}
	}
	return net.JoinHostPort(strings.TrimSuffix(u.Hostname(), "."), port)
}

func validateArtifactURL(ctx context.Context, rawURL, trustedBaseURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !u.IsAbs() || u.Host == "" || u.User != nil {
		return nil, errors.New("invalid artifact URL")
	}
	trusted, _ := url.Parse(strings.TrimSpace(trustedBaseURL))
	if trusted != nil && trusted.IsAbs() && sameOrigin(u, trusted) {
		return u, nil
	}
	if u.Scheme != "https" {
		return nil, errors.New("external artifact URL must use https")
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if restrictedArtifactIP(ip) {
			return nil, errors.New("restricted artifact address")
		}
		return u, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil || len(addrs) == 0 {
		return nil, errors.New("artifact host did not resolve")
	}
	for _, addr := range addrs {
		if restrictedArtifactIP(addr.IP) {
			return nil, errors.New("restricted artifact address")
		}
	}
	return u, nil
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func restrictedArtifactIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}

func trustedHTTPSArtifactHost(rawURL string, suffixes ...string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || !u.IsAbs() || !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Hostname() == "" {
		return false
	}
	if port := u.Port(); port != "" && port != "443" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	for _, suffix := range suffixes {
		suffix = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(suffix), "."))
		if suffix != "" && (host == suffix || strings.HasSuffix(host, "."+suffix)) {
			return true
		}
	}
	return false
}

type spooledAPIArtifact struct {
	file        *os.File
	path        string
	size        int64
	contentType string
	sha256      string
}

type apiArtifactPersistenceResult struct {
	ObjectKey       string
	SourceValidated bool
}

func (a *spooledAPIArtifact) closeAndRemove() {
	if a == nil {
		return
	}
	if a.file != nil {
		_ = a.file.Close()
	}
	if a.path != "" {
		_ = os.Remove(a.path)
	}
}

func (s *V1Service) persistAPIArtifact(ctx context.Context, ev *model.EventLog, rawURL, kind string) (apiArtifactPersistenceResult, error) {
	if ev == nil || s.store == nil || !s.store.Configured() {
		return apiArtifactPersistenceResult{}, errors.New("API media storage is unavailable")
	}
	select {
	case s.mediaIngest <- struct{}{}:
		defer func() { <-s.mediaIngest }()
	case <-ctx.Done():
		return apiArtifactPersistenceResult{}, ctx.Err()
	}

	var artifact *spooledAPIArtifact
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		artifact, err = s.downloadAPIArtifact(ctx, ev, rawURL, kind)
		if err == nil {
			break
		}
		log.Printf("api media ingest: stage=download_failed event_id=%s kind=%s attempt=%d error=%v", ev.ID, kind, attempt, err)
		if attempt < 3 && !sleepAPIMediaRetry(ctx, time.Duration(attempt)*500*time.Millisecond) {
			return apiArtifactPersistenceResult{}, ctx.Err()
		}
	}
	if err != nil {
		return apiArtifactPersistenceResult{}, err
	}
	defer artifact.closeAndRemove()
	result := apiArtifactPersistenceResult{SourceValidated: true}

	key, err := apiMediaObjectKey(ev.UserID, kind, ev.ID, artifact.contentType)
	if err != nil {
		return result, err
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if _, err = artifact.file.Seek(0, io.SeekStart); err == nil {
			err = s.store.PutStream(ctx, key, artifact.file, artifact.size, artifact.contentType)
		}
		if err == nil {
			log.Printf("api media ingest: stage=stored event_id=%s kind=%s bytes=%d content_type=%s sha256=%s", ev.ID, kind, artifact.size, artifact.contentType, artifact.sha256)
			result.ObjectKey = key
			return result, nil
		}
		log.Printf("api media ingest: stage=upload_failed event_id=%s kind=%s attempt=%d bytes=%d error=%v", ev.ID, kind, attempt, artifact.size, err)
		if attempt < 3 && !sleepAPIMediaRetry(ctx, time.Duration(attempt)*time.Second) {
			return result, ctx.Err()
		}
	}
	return result, err
}

func (s *V1Service) persistAPIBytes(ctx context.Context, ev *model.EventLog, data []byte, kind string) (string, error) {
	if ev == nil || s.store == nil || !s.store.Configured() {
		return "", errors.New("API media storage is unavailable")
	}
	maxBytes := int64(1024 * 1024 * 1024)
	if s.cfg != nil && s.cfg.APIMediaMaxBytes > 0 {
		maxBytes = s.cfg.APIMediaMaxBytes
	}
	if len(data) == 0 || int64(len(data)) > maxBytes {
		return "", fmt.Errorf("API media bytes exceed configured bounds")
	}
	contentType, err := detectAPIMediaType(bytes.NewReader(data), "", kind)
	if err != nil {
		return "", err
	}
	key, err := apiMediaObjectKey(ev.UserID, kind, ev.ID, contentType)
	if err != nil {
		return "", err
	}
	select {
	case s.mediaIngest <- struct{}{}:
		defer func() { <-s.mediaIngest }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	for attempt := 1; attempt <= 3; attempt++ {
		err = s.store.Put(ctx, key, data, contentType)
		if err == nil {
			sum := sha256.Sum256(data)
			log.Printf("api media ingest: stage=stored event_id=%s kind=%s bytes=%d content_type=%s sha256=%s", ev.ID, kind, len(data), contentType, hex.EncodeToString(sum[:]))
			return key, nil
		}
		log.Printf("api media ingest: stage=upload_failed event_id=%s kind=%s attempt=%d bytes=%d error=%v", ev.ID, kind, attempt, len(data), err)
		if attempt < 3 && !sleepAPIMediaRetry(ctx, time.Duration(attempt)*time.Second) {
			return "", ctx.Err()
		}
	}
	return "", err
}

func (s *V1Service) downloadAPIArtifact(ctx context.Context, ev *model.EventLog, rawURL, kind string) (*spooledAPIArtifact, error) {
	resp, err := s.openProviderArtifact(ctx, ev, rawURL, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("artifact download status %d", resp.StatusCode)
	}
	maxBytes := int64(1024 * 1024 * 1024)
	spoolDir := os.TempDir()
	if s.cfg != nil {
		if s.cfg.APIMediaMaxBytes > 0 {
			maxBytes = s.cfg.APIMediaMaxBytes
		}
		if strings.TrimSpace(s.cfg.APIMediaSpoolDir) != "" {
			spoolDir = s.cfg.APIMediaSpoolDir
		}
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("artifact exceeds maximum size of %d bytes", maxBytes)
	}
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		return nil, fmt.Errorf("create media spool directory: %w", err)
	}
	required := maxBytes + apiMediaDiskReserve
	if resp.ContentLength > 0 {
		required = resp.ContentLength + apiMediaDiskReserve
	}
	if free, err := availableDiskBytes(spoolDir); err != nil {
		return nil, fmt.Errorf("check media spool free space: %w", err)
	} else if free < uint64(required) {
		return nil, fmt.Errorf("insufficient media spool space: need %d bytes", required)
	}

	file, err := os.CreateTemp(spoolDir, "image2api-api-media-*.part")
	if err != nil {
		return nil, fmt.Errorf("create media spool file: %w", err)
	}
	artifact := &spooledAPIArtifact{file: file, path: file.Name()}
	failed := true
	defer func() {
		if failed {
			artifact.closeAndRemove()
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, fmt.Errorf("secure media spool file: %w", err)
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("download artifact body: %w", err)
	}
	if written == 0 {
		return nil, errors.New("downloaded artifact is empty")
	}
	if written > maxBytes {
		return nil, fmt.Errorf("artifact exceeds maximum size of %d bytes", maxBytes)
	}
	contentType, err := detectAPIMediaType(file, resp.Header.Get("Content-Type"), kind)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("rewind media spool file: %w", err)
	}
	artifact.size = written
	artifact.contentType = contentType
	artifact.sha256 = hex.EncodeToString(hash.Sum(nil))
	failed = false
	return artifact, nil
}

func detectAPIMediaType(file io.ReadSeeker, hint, kind string) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	header := make([]byte, 512)
	n, err := io.ReadFull(file, header)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", err
	}
	header = header[:n]
	detected := http.DetectContentType(header)
	if len(header) >= 12 && string(header[4:8]) == "ftyp" {
		detected = "video/mp4"
	} else if len(header) >= 4 && header[0] == 0x1a && header[1] == 0x45 && header[2] == 0xdf && header[3] == 0xa3 {
		detected = "video/webm"
	}
	_ = hint // The provider header is diagnostic only; magic bytes are authoritative.
	switch kind {
	case "image":
		if !strings.HasPrefix(detected, "image/") {
			return "", fmt.Errorf("artifact content type %q is not an image", detected)
		}
	case "video":
		if detected != "video/mp4" && detected != "video/webm" {
			return "", fmt.Errorf("artifact content type %q is not a supported video", detected)
		}
	default:
		return "", fmt.Errorf("unsupported API media kind %q", kind)
	}
	return detected, nil
}

func apiMediaObjectKey(userID, kind, eventID, contentType string) (string, error) {
	if !apiMediaSegmentPattern.MatchString(userID) || !apiMediaSegmentPattern.MatchString(eventID) {
		return "", errors.New("invalid API media owner or event ID")
	}
	ext := ""
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/png":
		ext = ".png"
	case "image/jpeg":
		ext = ".jpg"
	case "image/webp":
		ext = ".webp"
	case "image/gif":
		ext = ".gif"
	case "video/mp4":
		ext = ".mp4"
	case "video/webm":
		ext = ".webm"
	default:
		return "", fmt.Errorf("unsupported API media content type %q", contentType)
	}
	if kind != "image" && kind != "video" {
		return "", fmt.Errorf("unsupported API media kind %q", kind)
	}
	return path.Join("api", userID, kind+"s", eventID+ext), nil
}

func sleepAPIMediaRetry(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
