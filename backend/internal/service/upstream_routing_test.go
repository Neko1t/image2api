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
