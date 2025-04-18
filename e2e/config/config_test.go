package config_test

import (
	"strings"
	"testing"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/types"
)

func TestValidatePVCSpecs(t *testing.T) {
	// storageClass name exceeding 253 chars
	longSCName := strings.Repeat("a", 254)

	tests := []struct {
		name    string
		config  *types.Config
		wantErr bool
	}{
		{
			name: "empty PVCSpecs list",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{},
			},
			wantErr: true,
		},
		{
			name: "valid PVCSpecs",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: false,
		},
		{
			name: "duplicate PVCSpec names",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "duplicate PVCSpec content (same StorageClassName and AccessModes)",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
					{Name: "cephfs", StorageClassName: "standard", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid storageClassName format - uppercase",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "Standard", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid storageClassName format - contains underscore",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard_class", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid storageClassName format - contains dot and dash",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard-class.name", AccessModes: "ReadWriteOnce"},
				},
			},
			// This is actually allowed per RFC 1123
			wantErr: false,
		},
		{
			name: "invalid storageClassName format - starts with dash",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "-standard", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid storageClassName format - ends with dash",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard-", AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid storageClassName format - more than 253 characters",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: longSCName, AccessModes: "ReadWriteOnce"},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid access mode",
			config: &types.Config{
				PVCSpecs: []types.PVCSpecConfig{
					{Name: "rbd", StorageClassName: "standard", AccessModes: "InvalidAccessMode"},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := config.ValidatePVCSpecs(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePVCSpecs() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
