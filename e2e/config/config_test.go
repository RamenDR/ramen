// Disabling testpackage linter to test unexported functions in the config package.
//
//nolint:testpackage
package config

import (
	"testing"

	"github.com/ramendr/ramen/e2e/types"
)

func TestValidatePVCSpecs(t *testing.T) {
	tests := []struct {
		name   string
		config *types.Config
		valid  bool
	}{
		{
			name: "valid",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "fs", StorageClassName: "filesystem", AccessModes: "ReadWriteMany"},
				},
			},
			valid: true,
		},
		{
			name: "empty",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{},
			},
			valid: false,
		},
		{
			name: "duplicate names",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "duplicate content",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "cephfs", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "invalid storage class name",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "not a dns subdomain name", AccessModes: "ReadWriteOnce"},
				},
			},
			valid: false,
		},
		{
			name: "invalid access mode",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "InvalidAccessMode"},
				},
			},
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePVCSpecs(tt.config)
			if tt.valid && err != nil {
				t.Errorf("valid config %+v, failed: %s", tt.config.PVCSpecs, err)
			}

			if !tt.valid && err == nil {
				t.Errorf("invalid config %+v, did not fail", tt.config.PVCSpecs)
			}
		})
	}
}
