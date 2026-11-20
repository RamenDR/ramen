// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/util"
)

const (
	// Measured builds of this kustomization finish in about 2s.
	// One minute is 3x that time, rounded up. The same duration is set on the
	// kustomize URL because a remote git fetch otherwise stops after 27s.
	kustomizeBuildTimeout = time.Minute

	// kubectl StreamVisitor uses this buffer to tell a JSON stream from YAML.
	yamlOrJSONBufferSize = 4096
)

type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func downloadKustomization(ctx context.Context, repo config.Repo, workloadPath string) (string, error) {
	buildCtx, cancel := context.WithTimeout(ctx, kustomizeBuildTimeout)
	defer cancel()

	url := repo.URL + "/" + workloadPath + "?ref=" + repo.Branch + "&timeout=" + kustomizeBuildTimeout.String()

	res := util.RunCommand(buildCtx, "kubectl", "kustomize", url)
	if res.Err != nil {
		if len(res.Stderr) > 0 {
			return "", fmt.Errorf("failed to build kustomization %q: %w: stderr=%q", url, res.Err, res.Stderr)
		}

		return "", fmt.Errorf("failed to build kustomization %q: %w", url, res.Err)
	}

	return string(res.Stdout), nil
}

// pvcNames returns PersistentVolumeClaim names from a kubectl kustomize build.
// The decoder is the one kubectl uses for multi-document YAML or JSON input.
func pvcNames(kustomization string) ([]string, error) {
	decoder := k8syaml.NewYAMLOrJSONDecoder(strings.NewReader(kustomization), yamlOrJSONBufferSize)
	names := make([]string, 0)

	for {
		obj := &unstructured.Unstructured{}

		err := decoder.Decode(obj)
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			return nil, err
		}

		if obj.GetAPIVersion() == corev1.SchemeGroupVersion.String() && obj.GetKind() == "PersistentVolumeClaim" {
			names = append(names, obj.GetName())
		}
	}

	if len(names) == 0 {
		return nil, errors.New("no persistent volume claims found")
	}

	return names, nil
}
