package service

import (
	"testing"

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
