package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	hostreasoning "github.com/QuantumNous/new-api/setting/reasoning"
)

// ResolveChannelModelMapping returns the models traversed by a channel mapping.
// A terminal self-mapping is retained so relay callers can preserve its behavior.
func ResolveChannelModelMapping(mapping, modelName string) ([]string, error) {
	chain := []string{modelName}
	if mapping == "" || mapping == "{}" {
		return chain, nil
	}
	var modelMap map[string]string
	if err := common.Unmarshal([]byte(mapping), &modelMap); err != nil {
		return nil, errors.New("unmarshal_model_mapping_failed")
	}
	visited := map[string]bool{modelName: true}
	current := modelName
	for {
		mapped, exists := modelMap[current]
		base := hostreasoning.BaseModelName(current)
		if (!exists || mapped == "") && base != current {
			mapped, exists = modelMap[base]
		}
		if !exists || mapped == "" {
			return chain, nil
		}
		if visited[mapped] && mapped != current {
			return nil, errors.New("model_mapping_contains_cycle")
		}
		chain = append(chain, mapped)
		if mapped == current {
			return chain, nil
		}
		visited[mapped] = true
		current = mapped
	}
}
