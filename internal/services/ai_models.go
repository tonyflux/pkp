package services

import (
	"context"

	"github.com/tonyflux/pkp/internal/ai"
)

// AIModels lists models using only the saved endpoint and its bound key. Unlike
// model requests it does not require a model to have already been selected.
func (d *DriveService) AIModels() ([]string, error) {
	d.settingsMu.Lock()
	s := d.readSettings()
	key := d.storedAIKey(s)
	d.settingsMu.Unlock()
	if key == "" && !aiKeyless(s) {
		return nil, errAINoKey
	}
	cfg := ai.Config{Provider: ai.Provider(s.AIProvider), APIKey: key}
	if cfg.Provider == ai.OpenAI {
		cfg.BaseURL = s.AIBaseURL
	}
	models, err := ai.ListModels(context.Background(), cfg)
	if err != nil {
		return nil, d.aiFailure(err)
	}
	return models, nil
}
