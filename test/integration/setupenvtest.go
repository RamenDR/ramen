// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package integration

import (
	"os"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func ConfigureSetupEnvTest() (testEnv *envtest.Environment, kconfig *rest.Config, namespaceDeletionSupported bool,
	err error,
) {
	if _, set := os.LookupEnv("KUBEBUILDER_ASSETS"); !set {
		// read content of the testassets.txt file
		// and set the content as the value of KUBEBUILDER_ASSETS
		// this is to avoid the need to set KUBEBUILDER_ASSETS
		// when running the test suite
		content, err := os.ReadFile(testassets)
		if err != nil {
			return nil, nil, false, err
		}

		if err := os.Setenv("KUBEBUILDER_ASSETS", string(content)); err != nil {
			return nil, nil, false, err
		}
	}

	testEnv = &envtest.Environment{
		CRDDirectoryPaths: crdDirectoryPaths,
	}

	if testEnv.UseExistingCluster != nil && *testEnv.UseExistingCluster {
		namespaceDeletionSupported = true
	}

	kconfig, err = testEnv.Start()

	return testEnv, kconfig, namespaceDeletionSupported, err
}

func CleanupSetupEnvTest(te *envtest.Environment) error {
	return te.Stop()
}
