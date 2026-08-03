package bootstrap

import (
	"testing"

	"backend/internal/config"
)

func TestNewStorageClientDefaultsToRustFS(t *testing.T) {
	client, err := newStorageClient(&config.Config{
		RustFSEndpoint:  "http://rustfs:9000",
		RustFSBucket:    "vivid-ai",
		RustFSAccessKey: "id",
		RustFSSecretKey: "secret",
	})
	if err != nil {
		t.Fatalf("new storage client: %v", err)
	}
	if !client.Configured() {
		t.Fatal("expected configured default RustFS client")
	}
	if client.DirectDeliveryEnabled() {
		t.Fatal("RustFS must not enable direct delivery")
	}
}

func TestNewStorageClientRejectsUnknownDriver(t *testing.T) {
	_, err := newStorageClient(&config.Config{StorageDriver: "unknown"})
	if err == nil {
		t.Fatal("expected unknown storage driver to fail")
	}
}

func TestNewStorageClientRejectsIncompleteOSSConfig(t *testing.T) {
	_, err := newStorageClient(&config.Config{StorageDriver: "oss", OSSRegion: "cn-hongkong"})
	if err == nil {
		t.Fatal("expected incomplete OSS configuration to fail")
	}
}
