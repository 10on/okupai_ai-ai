package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed defaults/config.json defaults/models.json defaults/services.json
var defaultConfigs embed.FS

type APIConfig struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	APIKey string `json:"api_key"`
}

type LlamaConfig struct {
	CtxSize    int      `json:"ctx_size"`
	Parallel   int      `json:"parallel"`
	NGpuLayers int      `json:"n_gpu_layers"`
	FlashAttn  bool     `json:"flash_attn"`
	ExtraArgs  []string `json:"extra_args"`
}

type PowerConfig struct {
	Caffeinate   bool `json:"caffeinate"`
	WiredLimitMB int  `json:"wired_limit_mb"`
}

type RestartConfig struct {
	MaxRestarts   int `json:"max_restarts"`
	WindowSeconds int `json:"window_seconds"`
}

type Config struct {
	API                 APIConfig     `json:"api"`
	Llama               LlamaConfig   `json:"llama"`
	DefaultModel        string        `json:"default_model"`
	Power               PowerConfig   `json:"power"`
	Restart             RestartConfig `json:"restart"`
	StartTimeoutSeconds int           `json:"start_timeout_seconds"`
}

type Model struct {
	HF          string `json:"hf"`
	Path        string `json:"path"`
	MMProj      string `json:"mmproj"` // vision projector for multimodal (VL) models
	CtxSize     int    `json:"ctx_size"`
	Description string `json:"description"`

	// Per-profile overrides of the global llama settings (zero value = use global).
	Parallel   int      `json:"parallel,omitempty"`
	CacheTypeK string   `json:"cache_type_k,omitempty"` // e.g. "q8_0" — quantized KV cache
	CacheTypeV string   `json:"cache_type_v,omitempty"`
	ExtraArgs  []string `json:"extra_args,omitempty"`
}

type ModelsConfig struct {
	Models map[string]Model `json:"models"`
}

type ServicesConfig struct {
	Apps        []string `json:"apps"`
	LaunchdUser []string `json:"launchd_user"`
	Never       []string `json:"never"`
}

// ensureConfigs copies embedded defaults into ~/.aimode/config on first run,
// so the binary is self-contained yet the user still has editable files.
func ensureConfigs(p Paths) error {
	for _, name := range []string{"config.json", "models.json", "services.json"} {
		dst := filepath.Join(p.ConfigDir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := defaultConfigs.ReadFile("defaults/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func loadJSON[T any](path string) (T, error) {
	var out T
	data, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return out, fmt.Errorf("%s: %w", path, err)
	}
	return out, nil
}

func loadConfig(p Paths) (Config, error) {
	return loadJSON[Config](filepath.Join(p.ConfigDir, "config.json"))
}

func loadModels(p Paths) (ModelsConfig, error) {
	return loadJSON[ModelsConfig](filepath.Join(p.ConfigDir, "models.json"))
}

func loadServices(p Paths) (ServicesConfig, error) {
	return loadJSON[ServicesConfig](filepath.Join(p.ConfigDir, "services.json"))
}
