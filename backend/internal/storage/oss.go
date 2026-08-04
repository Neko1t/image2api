package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
)

const maxOSSSignedURLTTL = 7 * 24 * time.Hour

type OSSConfig struct {
	Region          string
	Endpoint        string
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	SessionToken    string
	UseCName        bool
	DirectDelivery  bool
	SignedURLTTL    time.Duration
}

type ossAPI interface {
	PutObject(context.Context, *alioss.PutObjectRequest, ...func(*alioss.Options)) (*alioss.PutObjectResult, error)
	GetObject(context.Context, *alioss.GetObjectRequest, ...func(*alioss.Options)) (*alioss.GetObjectResult, error)
	HeadObject(context.Context, *alioss.HeadObjectRequest, ...func(*alioss.Options)) (*alioss.HeadObjectResult, error)
	DeleteObject(context.Context, *alioss.DeleteObjectRequest, ...func(*alioss.Options)) (*alioss.DeleteObjectResult, error)
	ListObjectsV2(context.Context, *alioss.ListObjectsV2Request, ...func(*alioss.Options)) (*alioss.ListObjectsV2Result, error)
	Presign(context.Context, any, ...func(*alioss.PresignOptions)) (*alioss.PresignResult, error)
}

type ossUploader interface {
	UploadFrom(context.Context, *alioss.PutObjectRequest, io.Reader, ...func(*alioss.UploaderOptions)) (*alioss.UploadResult, error)
}

type ossDriver struct {
	client   ossAPI
	uploader ossUploader
	region   string
	endpoint string
	bucket   string
	useCName bool
}

func NewOSS(input OSSConfig) (*Client, error) {
	cfg, err := normalizeOSSConfig(input)
	if err != nil {
		return nil, err
	}

	sdkCfg := alioss.LoadDefaultConfig().
		WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.AccessKeyID,
			cfg.AccessKeySecret,
			cfg.SessionToken,
		)).
		WithRegion(cfg.Region).
		WithUseCName(cfg.UseCName).
		WithConnectTimeout(10 * time.Second).
		WithReadWriteTimeout(10 * time.Minute).
		WithRetryMaxAttempts(3)
	if cfg.Endpoint != "" {
		sdkCfg.WithEndpoint(cfg.Endpoint)
	}

	sdkClient := alioss.NewClient(sdkCfg)
	driver := &ossDriver{
		client: sdkClient,
		uploader: alioss.NewUploader(sdkClient, func(opts *alioss.UploaderOptions) {
			opts.PartSize = 8 * 1024 * 1024
			opts.ParallelNum = 2
		}),
		region:   cfg.Region,
		endpoint: cfg.Endpoint,
		bucket:   cfg.Bucket,
		useCName: cfg.UseCName,
	}
	return &Client{
		driver:         driver,
		directDelivery: cfg.DirectDelivery,
		signedURLTTL:   cfg.SignedURLTTL,
	}, nil
}

func normalizeOSSConfig(cfg OSSConfig) (OSSConfig, error) {
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)
	cfg.AccessKeyID = strings.TrimSpace(cfg.AccessKeyID)
	cfg.AccessKeySecret = strings.TrimSpace(cfg.AccessKeySecret)
	cfg.SessionToken = strings.TrimSpace(cfg.SessionToken)

	for name, value := range map[string]string{
		"OSS_REGION":            cfg.Region,
		"OSS_BUCKET":            cfg.Bucket,
		"OSS_ACCESS_KEY_ID":     cfg.AccessKeyID,
		"OSS_ACCESS_KEY_SECRET": cfg.AccessKeySecret,
	} {
		if value == "" {
			return OSSConfig{}, fmt.Errorf("%s is required when STORAGE_DRIVER=oss", name)
		}
	}
	if cfg.UseCName && cfg.Endpoint == "" {
		return OSSConfig{}, errors.New("OSS_ENDPOINT is required when OSS_USE_CNAME=true")
	}
	if cfg.SignedURLTTL <= 0 {
		cfg.SignedURLTTL = time.Hour
	}
	if cfg.SignedURLTTL > maxOSSSignedURLTTL {
		return OSSConfig{}, fmt.Errorf("OSS signed URL TTL must not exceed %s", maxOSSSignedURLTTL)
	}
	return cfg, nil
}

func (d *ossDriver) Configured() bool {
	return d != nil && d.client != nil && d.region != "" && d.bucket != ""
}

func (d *ossDriver) PublicURL(key string) string {
	key = strings.TrimPrefix(key, "/")
	endpoint := d.endpoint
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://oss-%s.aliyuncs.com", d.region)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		return ""
	}
	if !d.useCName && !strings.HasPrefix(parsed.Host, d.bucket+".") {
		parsed.Host = d.bucket + "." + parsed.Host
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/" + key
	return parsed.String()
}

func (d *ossDriver) Put(ctx context.Context, key string, body []byte, contentType string) error {
	req := &alioss.PutObjectRequest{
		Bucket: alioss.Ptr(d.bucket),
		Key:    alioss.Ptr(strings.TrimPrefix(key, "/")),
		Body:   bytes.NewReader(body),
	}
	if contentType = strings.TrimSpace(contentType); contentType != "" {
		req.ContentType = alioss.Ptr(contentType)
	}
	if _, err := d.client.PutObject(ctx, req); err != nil {
		return fmt.Errorf("oss put %q: %w", key, err)
	}
	return nil
}

func (d *ossDriver) PutStream(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error {
	if body == nil || size < 0 {
		return fmt.Errorf("oss put stream %q: invalid body or size", key)
	}
	if d.uploader == nil {
		return fmt.Errorf("oss put stream %q: uploader unavailable", key)
	}
	if _, err := body.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("oss put stream %q: rewind: %w", key, err)
	}
	req := &alioss.PutObjectRequest{
		Bucket:        alioss.Ptr(d.bucket),
		Key:           alioss.Ptr(strings.TrimPrefix(key, "/")),
		Body:          body,
		ContentLength: alioss.Ptr(size),
	}
	if contentType = strings.TrimSpace(contentType); contentType != "" {
		req.ContentType = alioss.Ptr(contentType)
	}
	if _, err := d.uploader.UploadFrom(ctx, req, body); err != nil {
		return fmt.Errorf("oss put stream %q: %w", key, err)
	}
	return nil
}

func (d *ossDriver) Get(ctx context.Context, key, rangeHeader string) (*http.Response, error) {
	req := &alioss.GetObjectRequest{
		Bucket: alioss.Ptr(d.bucket),
		Key:    alioss.Ptr(strings.TrimPrefix(key, "/")),
	}
	if rangeHeader = strings.TrimSpace(rangeHeader); rangeHeader != "" {
		req.Range = alioss.Ptr(rangeHeader)
	}
	result, err := d.client.GetObject(ctx, req)
	if err != nil {
		if isOSSNotFound(err) {
			return notFoundResponse(), nil
		}
		return nil, fmt.Errorf("oss get %q: %w", key, err)
	}
	headers := result.Headers.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		Status:        result.Status,
		StatusCode:    result.StatusCode,
		Header:        headers,
		Body:          result.Body,
		ContentLength: result.ContentLength,
	}, nil
}

func (d *ossDriver) Delete(ctx context.Context, key string) error {
	_, err := d.client.DeleteObject(ctx, &alioss.DeleteObjectRequest{
		Bucket: alioss.Ptr(d.bucket),
		Key:    alioss.Ptr(strings.TrimPrefix(key, "/")),
	})
	if err != nil && !isOSSNotFound(err) {
		return fmt.Errorf("oss delete %q: %w", key, err)
	}
	return nil
}

func (d *ossDriver) List(ctx context.Context, prefix string) ([]Object, error) {
	prefix = strings.TrimPrefix(prefix, "/")
	var (
		objects []Object
		token   string
	)
	for {
		req := &alioss.ListObjectsV2Request{
			Bucket:  alioss.Ptr(d.bucket),
			MaxKeys: 1000,
		}
		if prefix != "" {
			req.Prefix = alioss.Ptr(prefix)
		}
		if token != "" {
			req.ContinuationToken = alioss.Ptr(token)
		}
		result, err := d.client.ListObjectsV2(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("oss list %q: %w", prefix, err)
		}
		for _, item := range result.Contents {
			if item.Key == nil {
				continue
			}
			var modified time.Time
			if item.LastModified != nil {
				modified = *item.LastModified
			}
			objects = append(objects, Object{
				Key:          *item.Key,
				Size:         item.Size,
				LastModified: modified,
			})
		}
		if !result.IsTruncated {
			break
		}
		next := strings.TrimSpace(alioss.ToString(result.NextContinuationToken))
		if next == "" || next == token {
			return nil, fmt.Errorf("oss list %q: truncated response missing a new continuation token", prefix)
		}
		token = next
	}
	return objects, nil
}

func (d *ossDriver) Exists(ctx context.Context, key string) (bool, error) {
	_, err := d.client.HeadObject(ctx, &alioss.HeadObjectRequest{
		Bucket: alioss.Ptr(d.bucket),
		Key:    alioss.Ptr(strings.TrimPrefix(key, "/")),
	})
	if err == nil {
		return true, nil
	}
	if isOSSNotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("oss head %q: %w", key, err)
}

func (d *ossDriver) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	result, err := d.client.Presign(ctx, &alioss.GetObjectRequest{
		Bucket: alioss.Ptr(d.bucket),
		Key:    alioss.Ptr(strings.TrimPrefix(key, "/")),
	}, alioss.PresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("oss presign get %q: %w", key, err)
	}
	if strings.TrimSpace(result.URL) == "" {
		return "", fmt.Errorf("oss presign get %q: empty URL", key)
	}
	return result.URL, nil
}

func isOSSNotFound(err error) bool {
	var serviceErr *alioss.ServiceError
	return errors.As(err, &serviceErr) &&
		(serviceErr.StatusCode == http.StatusNotFound || serviceErr.Code == "NoSuchKey" || serviceErr.Code == "NoSuchObject")
}

func notFoundResponse() *http.Response {
	return &http.Response{
		Status:     http.StatusText(http.StatusNotFound),
		StatusCode: http.StatusNotFound,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}
