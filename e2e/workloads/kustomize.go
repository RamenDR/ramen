// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package workloads

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	"github.com/ramendr/ramen/e2e/config"
	"github.com/ramendr/ramen/e2e/util"
)

const (
	// Measured builds of this kustomization finish in about 2s.
	// One minute is 3x that time, rounded up. The same duration is set on the
	// kustomize URL because a remote git fetch otherwise stops after 27s.
	kustomizeBuildTimeout = time.Minute
)

type jsonPatch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

func downloadKustomization(repo config.Repo, workloadPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), kustomizeBuildTimeout)
	defer cancel()

	url := repo.URL + "/" + workloadPath + "?ref=" + repo.Branch + "&timeout=" + kustomizeBuildTimeout.String()

	out, err := util.KubectlKustomize(ctx, url)
	if err != nil {
		return "", fmt.Errorf("failed to build kustomization %q: %w", url, err)
	}

	return string(out), nil
}

// splitDocuments separates YAML documents on a line that is only "---".
// A "---" inside a line, such as "foo---", stays in that document.
func splitDocuments(kustomization string) []string {
	const (
		leadingSeparator  = "---\n"
		trailingSeparator = "\n---"
		separator         = "\n---\n"
	)

	if strings.HasPrefix(kustomization, leadingSeparator) {
		kustomization = kustomization[len(leadingSeparator):]
	}

	if strings.HasSuffix(kustomization, trailingSeparator) {
		kustomization = kustomization[:len(kustomization)-len(trailingSeparator)]
	}

	return strings.Split(kustomization, separator)
}

func pvcNames(manifests []string) ([]string, error) {
	names := make([]string, 0, len(manifests))

	for _, manifest := range manifests {
		pvc, ok, err := unmarshalPVC(manifest)
		if err != nil {
			return nil, err
		}

		if ok {
			names = append(names, pvc.GetName())
		}
	}

	if len(names) == 0 {
		return nil, fmt.Errorf("no persistent volume claims found")
	}

	return names, nil
}

func unmarshalPVC(doc string) (*unstructured.Unstructured, bool, error) {
	obj := &unstructured.Unstructured{}
	if err := yaml.Unmarshal([]byte(doc), obj); err != nil {
		return nil, false, err
	}

	if obj.GetAPIVersion() != corev1.SchemeGroupVersion.String() || obj.GetKind() != "PersistentVolumeClaim" {
		return nil, false, nil
	}

	return obj, true, nil
}
