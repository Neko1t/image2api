package storage

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	alioss "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
)

type fakeOSSUploader struct {
	upload func(context.Context, *alioss.PutObjectRequest, io.Reader, ...func(*alioss.UploaderOptions)) (*alioss.UploadResult, error)
}

func (f *fakeOSSUploader) UploadFrom(ctx context.Context, req *alioss.PutObjectRequest, body io.Reader, opts ...func(*alioss.UploaderOptions)) (*alioss.UploadResult, error) {
	return f.upload(ctx, req, body, opts...)
}

type fakeOSSAPI struct {
	put     func(context.Context, *alioss.PutObjectRequest, ...func(*alioss.Options)) (*alioss.PutObjectResult, error)
	get     func(context.Context, *alioss.GetObjectRequest, ...func(*alioss.Options)) (*alioss.GetObjectResult, error)
	head    func(context.Context, *alioss.HeadObjectRequest, ...func(*alioss.Options)) (*alioss.HeadObjectResult, error)
	delete  func(context.Context, *alioss.DeleteObjectRequest, ...func(*alioss.Options)) (*alioss.DeleteObjectResult, error)
	list    func(context.Context, *alioss.ListObjectsV2Request, ...func(*alioss.Options)) (*alioss.ListObjectsV2Result, error)
	presign func(context.Context, any, ...func(*alioss.PresignOptions)) (*alioss.PresignResult, error)
}

func (f *fakeOSSAPI) PutObject(ctx context.Context, req *alioss.PutObjectRequest, opts ...func(*alioss.Options)) (*alioss.PutObjectResult, error) {
	return f.put(ctx, req, opts...)
}

func (f *fakeOSSAPI) GetObject(ctx context.Context, req *alioss.GetObjectRequest, opts ...func(*alioss.Options)) (*alioss.GetObjectResult, error) {
	return f.get(ctx, req, opts...)
}

func (f *fakeOSSAPI) HeadObject(ctx context.Context, req *alioss.HeadObjectRequest, opts ...func(*alioss.Options)) (*alioss.HeadObjectResult, error) {
	return f.head(ctx, req, opts...)
}

func (f *fakeOSSAPI) DeleteObject(ctx context.Context, req *alioss.DeleteObjectRequest, opts ...func(*alioss.Options)) (*alioss.DeleteObjectResult, error) {
	return f.delete(ctx, req, opts...)
}

func (f *fakeOSSAPI) ListObjectsV2(ctx context.Context, req *alioss.ListObjectsV2Request, opts ...func(*alioss.Options)) (*alioss.ListObjectsV2Result, error) {
	return f.list(ctx, req, opts...)
}

func (f *fakeOSSAPI) Presign(ctx context.Context, req any, opts ...func(*alioss.PresignOptions)) (*alioss.PresignResult, error) {
	return f.presign(ctx, req, opts...)
}

func TestNormalizeOSSConfig(t *testing.T) {
	cfg, err := normalizeOSSConfig(OSSConfig{
		Region:          " cn-hongkong ",
		Endpoint:        " https://media.example.com/ ",
		Bucket:          " bucket ",
		AccessKeyID:     " id ",
		AccessKeySecret: " secret ",
		UseCName:        true,
	})
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}
	if cfg.Endpoint != "https://media.example.com" || cfg.SignedURLTTL != time.Hour {
		t.Fatalf("unexpected normalized config: endpoint=%q ttl=%s", cfg.Endpoint, cfg.SignedURLTTL)
	}

	_, err = normalizeOSSConfig(OSSConfig{Region: "cn-hongkong"})
	if err == nil {
		t.Fatal("expected incomplete OSS config to fail")
	}

	_, err = normalizeOSSConfig(OSSConfig{
		Region: "cn-hongkong", Bucket: "bucket", AccessKeyID: "id", AccessKeySecret: "secret",
		UseCName: true,
	})
	if err == nil {
		t.Fatal("expected CNAME config without endpoint to fail")
	}
}

func TestOSSDriverGetForwardsRangeAndNormalizesNotFound(t *testing.T) {
	var gotRange string
	fake := &fakeOSSAPI{}
	fake.get = func(_ context.Context, req *alioss.GetObjectRequest, _ ...func(*alioss.Options)) (*alioss.GetObjectResult, error) {
		gotRange = alioss.ToString(req.Range)
		return &alioss.GetObjectResult{
			ContentLength: 2,
			Body:          io.NopCloser(strings.NewReader("ok")),
			ResultCommon: alioss.ResultCommon{
				Status:     "206 Partial Content",
				StatusCode: http.StatusPartialContent,
				Headers:    http.Header{"Content-Type": []string{"video/mp4"}},
			},
		}, nil
	}
	driver := &ossDriver{client: fake, region: "cn-hongkong", bucket: "bucket"}

	resp, err := driver.Get(context.Background(), "u/video.mp4", "bytes=10-20")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if gotRange != "bytes=10-20" || resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("range=%q status=%d", gotRange, resp.StatusCode)
	}

	fake.get = func(context.Context, *alioss.GetObjectRequest, ...func(*alioss.Options)) (*alioss.GetObjectResult, error) {
		return nil, &alioss.ServiceError{StatusCode: http.StatusNotFound, Code: "NoSuchKey"}
	}
	resp, err = driver.Get(context.Background(), "missing", "")
	if err != nil {
		t.Fatalf("not found should preserve storage Get contract: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", resp.StatusCode)
	}
}

func TestOSSDriverListsAllPages(t *testing.T) {
	modified := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	var tokens []string
	fake := &fakeOSSAPI{}
	fake.list = func(_ context.Context, req *alioss.ListObjectsV2Request, _ ...func(*alioss.Options)) (*alioss.ListObjectsV2Result, error) {
		tokens = append(tokens, alioss.ToString(req.ContinuationToken))
		if len(tokens) == 1 {
			return &alioss.ListObjectsV2Result{
				Contents:              []alioss.ObjectProperties{{Key: alioss.Ptr("u/a.png"), Size: 10, LastModified: &modified}},
				IsTruncated:           true,
				NextContinuationToken: alioss.Ptr("next"),
			}, nil
		}
		return &alioss.ListObjectsV2Result{
			Contents: []alioss.ObjectProperties{{Key: alioss.Ptr("u/b.png"), Size: 20}},
		}, nil
	}
	driver := &ossDriver{client: fake, region: "cn-hongkong", bucket: "bucket"}

	objects, err := driver.List(context.Background(), "u/")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(objects) != 2 || objects[0].Key != "u/a.png" || objects[1].Key != "u/b.png" {
		t.Fatalf("unexpected objects: %#v", objects)
	}
	if len(tokens) != 2 || tokens[0] != "" || tokens[1] != "next" {
		t.Fatalf("unexpected continuation tokens: %#v", tokens)
	}
}

func TestOSSClientDirectDeliveryUsesConfiguredTTL(t *testing.T) {
	const signedURL = "https://media.example.com/u/a.png?signature=redacted"
	fake := &fakeOSSAPI{}
	fake.presign = func(_ context.Context, req any, optFns ...func(*alioss.PresignOptions)) (*alioss.PresignResult, error) {
		request, ok := req.(*alioss.GetObjectRequest)
		if !ok || alioss.ToString(request.Key) != "u/a.png" {
			t.Fatalf("unexpected request: %#v", req)
		}
		var opts alioss.PresignOptions
		for _, apply := range optFns {
			apply(&opts)
		}
		if opts.Expires != 45*time.Minute {
			t.Fatalf("expires=%s", opts.Expires)
		}
		return &alioss.PresignResult{URL: signedURL}, nil
	}
	client := &Client{
		driver:         &ossDriver{client: fake, region: "cn-hongkong", bucket: "bucket"},
		directDelivery: true,
		signedURLTTL:   45 * time.Minute,
	}

	got, err := client.PresignGet(context.Background(), "u/a.png")
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if got != signedURL {
		t.Fatalf("url=%q", got)
	}
}

func TestOSSDriverPutStreamUsesSeekableUploader(t *testing.T) {
	const payload = "streamed-video-bytes"
	uploader := &fakeOSSUploader{}
	uploader.upload = func(_ context.Context, req *alioss.PutObjectRequest, body io.Reader, _ ...func(*alioss.UploaderOptions)) (*alioss.UploadResult, error) {
		if alioss.ToString(req.Key) != "api/u/videos/evt.mp4" {
			t.Fatalf("key=%q", alioss.ToString(req.Key))
		}
		if req.ContentLength == nil || *req.ContentLength != int64(len(payload)) {
			t.Fatalf("content length=%v", req.ContentLength)
		}
		if alioss.ToString(req.ContentType) != "video/mp4" {
			t.Fatalf("content type=%q", alioss.ToString(req.ContentType))
		}
		got, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("read upload body: %v", err)
		}
		if string(got) != payload {
			t.Fatalf("body=%q", got)
		}
		return &alioss.UploadResult{}, nil
	}
	driver := &ossDriver{client: &fakeOSSAPI{}, uploader: uploader, region: "cn-hongkong", bucket: "bucket"}

	if err := driver.PutStream(context.Background(), "/api/u/videos/evt.mp4", bytes.NewReader([]byte(payload)), int64(len(payload)), "video/mp4"); err != nil {
		t.Fatalf("put stream: %v", err)
	}
}
