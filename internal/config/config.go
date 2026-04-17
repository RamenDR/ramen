// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/api/v1alpha1"
)

// Merge merges system and user RamenConfig YAML. Fields present in
// userYAML override the corresponding fields in systemYAML; fields
// missing from userYAML retain the system values.
func Merge(systemYAML, userYAML []byte) (v1alpha1.RamenConfig, error) {
	var merged v1alpha1.RamenConfig

	if err := yaml.Unmarshal(systemYAML, &merged); err != nil {
		return v1alpha1.RamenConfig{}, err
	}

	if err := yaml.Unmarshal(userYAML, &merged); err != nil {
		return v1alpha1.RamenConfig{}, err
	}

	return merged, nil
}
