package validateenvironment

import (
	"fmt"

	"github.com/ramendr/ramen/e2e/suite"
	"github.com/ramendr/ramen/e2e/utils"
)

var Enabled bool

type TestSuite struct {
	ctx *suite.TestContext
}

func (s *TestSuite) SetContext(ctx *suite.TestContext) {
	s.ctx = ctx
}

func (s *TestSuite) SetupSuite() error {
	s.ctx.Logger.Info("Setting up ValidateEnvironmentTestSuite")

	return nil
}

func (s *TestSuite) TeardownSuite() error {
	s.ctx.Logger.Info("Tearing down ValidateEnvironmentTestSuite")

	return nil
}

func (s *TestSuite) Tests() []suite.Test {
	return []suite.Test{
		s.TestRamenHubOperatorStatus,
		s.TestRamenDROperatorStatus,
	}
}

func (s *TestSuite) Description() string {
	return "ValidateEnvironmentTestSuite is a read-only test suite that validates the environment is set up correctly."
}

func (s *TestSuite) IsEnabled() bool {
	return Enabled
}

func (s *TestSuite) Enable() {
	Enabled = true
}

func (s *TestSuite) Disable() {
	Enabled = false
}

func (s *TestSuite) Name() string {
	return "ValidateEnvironmentTestSuite"
}

// TestRamenHubOperatorStatus tests the running status of the Ramen Hub Operator pod.
func (s *TestSuite) TestRamenHubOperatorStatus() error {
	s.ctx.Logger.Info("Running TestRamenHubOperatorStatus")

	isRunning, podName, err := utils.CheckRamenHubPodRunningStatus(s.ctx.HubClient())
	if err != nil {
		return err
	}

	if isRunning {
		s.ctx.Logger.Info("Ramen Hub Operator is running", "pod", podName)

		return nil
	}

	return fmt.Errorf("no running Ramen Hub Operator pod")
}

// TestRamenDROperatorStatus tests the running status of the Ramen DR Operator pod.
func (s *TestSuite) TestRamenDROperatorStatus() error {
	s.ctx.Logger.Info("Running TestRamenDROperatorStatus")

	for clusterName, cluster := range s.ctx.GetManagedClusters() {
		isRunning, podName, err := utils.CheckRamenDROperatorPodRunningStatus(cluster.GetClient())
		if err != nil {
			return err
		}

		if !isRunning {
			return fmt.Errorf("no running Ramen DR Operator pod found in cluster %s", clusterName)
		}

		if isRunning {
			s.ctx.Logger.Info("Ramen DR Operator is running", "cluster", clusterName, "pod", podName)
		}
	}

	return nil
}
