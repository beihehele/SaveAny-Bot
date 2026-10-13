package storage

import (
	"fmt"

	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/mitchellh/mapstructure"
)

var storageFactories = map[storenum.StorageType]func(raw map[string]any) (StorageConfig, error){
	storenum.Local: decodeLocalConfig,
}

func decodeLocalConfig(raw map[string]any) (StorageConfig, error) {
	config := new(LocalStorageConfig)
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: config, ErrorUnused: true, WeaklyTypedInput: true,
	})
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(raw); err != nil {
		return nil, err
	}
	return config, nil
}

func LoadStorageConfigs(definitions []map[string]any) ([]StorageConfig, error) {
	var configs []StorageConfig
	for _, raw := range definitions {
		var baseCfg BaseConfig
		if err := mapstructure.WeakDecode(raw, &baseCfg); err != nil {
			return nil, fmt.Errorf("decode storage definition: %w", err)
		}
		st, err := storenum.ParseStorageType(baseCfg.Type)
		if err != nil {
			return nil, fmt.Errorf("invalid storage type %s for %s: %w", baseCfg.Type, baseCfg.Name, err)
		}

		factory, ok := storageFactories[st]
		if !ok {
			return nil, fmt.Errorf("unsupported storage type: %s", baseCfg.Type)
		}

		cfg, err := factory(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to create storage config for %s: %w", baseCfg.Name, err)
		}
		if !baseCfg.Enable {
			continue
		}

		if err := cfg.Validate(); err != nil {
			return nil, fmt.Errorf("invalid storage config for %s: %w", baseCfg.Name, err)
		}

		configs = append(configs, cfg)
	}

	return configs, nil
}
