package storage

import (
	"context"
	"errors"
	"net/http"
	"time"
)

var ErrDirectDeliveryUnavailable = errors.New("storage direct delivery is unavailable")

// Object is one entry returned by List.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

type driver interface {
	Configured() bool
	PublicURL(key string) string
	Put(ctx context.Context, key string, body []byte, contentType string) error
	Get(ctx context.Context, key, rangeHeader string) (*http.Response, error)
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]Object, error)
	Exists(ctx context.Context, key string) (bool, error)
}

type signedGetDriver interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Client keeps the storage surface stable while delegating to the selected
// RustFS or OSS implementation.
type Client struct {
	driver         driver
	directDelivery bool
	signedURLTTL   time.Duration
}

// New preserves the original RustFS constructor for existing call sites.
func New(endpoint, bucket, accessKey, secretKey string) *Client {
	return NewRustFS(endpoint, bucket, accessKey, secretKey)
}

func NewRustFS(endpoint, bucket, accessKey, secretKey string) *Client {
	return &Client{driver: newRustFSDriver(endpoint, bucket, accessKey, secretKey)}
}

func (c *Client) Configured() bool {
	return c != nil && c.driver != nil && c.driver.Configured()
}

func (c *Client) PublicURL(key string) string {
	if !c.Configured() {
		return ""
	}
	return c.driver.PublicURL(key)
}

func (c *Client) Put(ctx context.Context, key string, body []byte, contentType string) error {
	return c.driver.Put(ctx, key, body, contentType)
}

func (c *Client) Get(ctx context.Context, key, rangeHeader string) (*http.Response, error) {
	return c.driver.Get(ctx, key, rangeHeader)
}

func (c *Client) Delete(ctx context.Context, key string) error {
	return c.driver.Delete(ctx, key)
}

func (c *Client) List(ctx context.Context, prefix string) ([]Object, error) {
	return c.driver.List(ctx, prefix)
}

func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	return c.driver.Exists(ctx, key)
}

func (c *Client) DirectDeliveryEnabled() bool {
	if c == nil || !c.directDelivery {
		return false
	}
	_, ok := c.driver.(signedGetDriver)
	return ok
}

func (c *Client) PresignGet(ctx context.Context, key string) (string, error) {
	if !c.DirectDeliveryEnabled() {
		return "", ErrDirectDeliveryUnavailable
	}
	return c.driver.(signedGetDriver).PresignGet(ctx, key, c.signedURLTTL)
}
