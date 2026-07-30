package service

import (
	"testing"

	"backend/internal/model"
	"gorm.io/datatypes"
)

func TestUpstreamAccountServesLegacyModelsCSV(t *testing.T) {
	account := model.TokenAccount{
		Status: "active",
		Value:  "sk-test",
		Meta: datatypes.JSONMap{
			"base_url": "https://vividai.run",
			"models":   "other-model, gpt-image-2 ",
		},
	}

	if !upstreamAccountServes(account, "gpt-image-2") {
		t.Fatalf("expected legacy meta.models CSV account to serve gpt-image-2")
	}
}

func TestUpstreamAccountServesEmptyModelsAsAllModels(t *testing.T) {
	account := model.TokenAccount{
		Status: "active",
		Value:  "sk-test",
		Meta: datatypes.JSONMap{
			"base_url": "https://vividai.run",
		},
	}

	if !upstreamAccountServes(account, "any-model") {
		t.Fatalf("expected empty model list to serve all models")
	}
}

func TestUpstreamAccountServesServedModelsArray(t *testing.T) {
	account := model.TokenAccount{
		Status: "active",
		Value:  "sk-test",
		Meta: datatypes.JSONMap{
			"base_url":      "https://vividai.run",
			"served_models": []any{"other-model", "gpt-image-2"},
		},
	}

	if !upstreamAccountServes(account, "gpt-image-2") {
		t.Fatalf("expected meta.served_models array account to serve gpt-image-2")
	}
}

func TestProviderHasUpstreamAccountPrecheck(t *testing.T) {
	if !providerHasUpstreamAccountPrecheck("upstream") {
		t.Fatalf("expected unified upstream provider to skip native token precheck")
	}
	if providerHasUpstreamAccountPrecheck("custom") {
		t.Fatalf("legacy custom provider name should not be treated as the unified upstream result")
	}
}

func TestSelectUpstreamCandidatesNormalRequestRequiresActiveAccount(t *testing.T) {
	accounts := []model.TokenAccount{
		{
			ID:     "disabled-upstream",
			Status: "disabled",
			Value:  "sk-disabled",
			Meta: datatypes.JSONMap{
				"base_url": "https://disabled.example.com",
				"models":   "firefly-gpt-image-2",
			},
		},
		{
			ID:     "active-upstream",
			Status: "active",
			Value:  "sk-active",
			Meta: datatypes.JSONMap{
				"base_url": "https://active.example.com",
				"models":   "firefly-gpt-image-2",
			},
		},
	}

	got := selectUpstreamCandidates(accounts, "firefly-gpt-image-2", "")
	if len(got) != 1 || got[0].ID != "active-upstream" {
		t.Fatalf("expected only active account, got %#v", got)
	}
}

func TestSelectUpstreamCandidatesPinnedAdminMayProbeInactiveAccount(t *testing.T) {
	accounts := []model.TokenAccount{
		{
			ID:     "disabled-upstream",
			Status: "disabled",
			Dead:   true,
			Value:  "sk-disabled",
			Meta: datatypes.JSONMap{
				"base_url": "https://disabled.example.com",
				"models":   "firefly-gpt-image-2",
			},
		},
	}

	got := selectUpstreamCandidates(accounts, "firefly-gpt-image-2", "disabled-upstream")
	if len(got) != 1 || got[0].ID != "disabled-upstream" {
		t.Fatalf("expected pinned inactive account, got %#v", got)
	}
}

func TestSelectUpstreamCandidatesPinnedAccountMustDeclareModel(t *testing.T) {
	accounts := []model.TokenAccount{
		{
			ID:     "upstream-1",
			Status: "active",
			Value:  "sk-test",
			Meta: datatypes.JSONMap{
				"base_url": "https://upstream.example.com",
				"models":   "another-model",
			},
		},
	}

	if got := selectUpstreamCandidates(accounts, "firefly-gpt-image-2", "upstream-1"); len(got) != 0 {
		t.Fatalf("expected model mismatch to reject pinned account, got %#v", got)
	}
}
