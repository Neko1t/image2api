package service

import (
	"bytes"
	"testing"
	"time"

	"backend/internal/model"
	"gorm.io/datatypes"
)

func TestYCYAccountServesMatchesCustomUpstreamRules(t *testing.T) {
	cases := []struct {
		name    string
		account model.TokenAccount
		modelID string
		want    bool
	}{
		{
			name: "active account with empty model list serves all models",
			account: model.TokenAccount{
				Status: "active",
				Value:  "sk-ycy",
				Meta:   datatypes.JSONMap{"base_url": "https://ycyapi.cn"},
			},
			modelID: "ycy-video-a",
			want:    true,
		},
		{
			name: "active account serves listed model case-insensitively",
			account: model.TokenAccount{
				Status: "active",
				Value:  "sk-ycy",
				Meta:   datatypes.JSONMap{"base_url": "https://ycyapi.cn", "models": "other-model, YCY-Video-A "},
			},
			modelID: "ycy-video-a",
			want:    true,
		},
		{
			name: "active account rejects unlisted model",
			account: model.TokenAccount{
				Status: "active",
				Value:  "sk-ycy",
				Meta:   datatypes.JSONMap{"base_url": "https://ycyapi.cn", "models": "other-model"},
			},
			modelID: "ycy-video-a",
			want:    false,
		},
		{
			name: "account without base url is not schedulable",
			account: model.TokenAccount{
				Status: "active",
				Value:  "sk-ycy",
				Meta:   datatypes.JSONMap{},
			},
			modelID: "ycy-video-a",
			want:    false,
		},
		{
			name: "dead account is not schedulable",
			account: model.TokenAccount{
				Status: "active",
				Dead:   true,
				Value:  "sk-ycy",
				Meta:   datatypes.JSONMap{"base_url": "https://ycyapi.cn"},
			},
			modelID: "ycy-video-a",
			want:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ycyAccountServes(tc.account, tc.modelID); got != tc.want {
				t.Fatalf("ycyAccountServes() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHashYCYVideoPayloadIsStableAndReferenceSensitive(t *testing.T) {
	a := hashYCYVideoPayload("video-v1", " move ", "16:9", "720p", "10s", [][]byte{[]byte("frame-a")})
	b := hashYCYVideoPayload("video-v1", "move", "16:9", "720p", "10s", [][]byte{bytes.Clone([]byte("frame-a"))})
	c := hashYCYVideoPayload("video-v1", "move", "16:9", "720p", "10s", [][]byte{[]byte("frame-b")})
	if a != b {
		t.Fatalf("equivalent payload hashes differ: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("different reference bytes produced the same payload hash: %q", a)
	}
}

func TestYCYRetryDelayIsCapped(t *testing.T) {
	if got := ycyRetryDelay(0); got != 15*time.Second {
		t.Fatalf("first retry delay = %s", got)
	}
	if got := ycyRetryDelay(10); got != 60*time.Second {
		t.Fatalf("capped retry delay = %s", got)
	}
}
