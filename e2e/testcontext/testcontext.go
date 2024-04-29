// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package testcontext

import (
	"fmt"
	"strings"

	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/workloads"
)

type TestContext struct {
	Workload workloads.Workload
	Deployer deployers.Deployer
}

var testContextMap = make(map[string]TestContext)

// Based on name passed, Init the deployer and Workload and stash in a map[string]TestContext
func AddTestContext(name string, w workloads.Workload, d deployers.Deployer) {
	testContextMap[name] = TestContext{w, d}
}

func DeleteTestContext(name string, w workloads.Workload, d deployers.Deployer) {
	delete(testContextMap, name)
}

// Search name in map for a TestContext to return, if not found go backward
// - drop the last /<name> suffix form name and search
// - e.g If name passed is "TestSuites/Exhaustive/DaemonSet/Subscription/Undeploy"
//   - Search for above name first (it will not be found as we create context at a point where we have a d+w)
//   - Search for "TestSuites/Exhaustive/DaemonSet/Subscription" (should be found)
func GetTestContext(name string) (TestContext, error) {
	testCtx, ok := testContextMap[name]
	if !ok {
		i := strings.LastIndex(name, "/")
		if i < 1 {
			return TestContext{}, fmt.Errorf("not a valid name in TestContext: %v", name)
		}

		testCtx, ok = testContextMap[name[0:i]]
		if !ok {
			return TestContext{}, fmt.Errorf("can not find testContext with name: %v", name)
		}
	}

	return testCtx, nil
}
