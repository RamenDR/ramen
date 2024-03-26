package suites

import (
	"github.com/ramendr/ramen/e2e/deployers"
	"github.com/ramendr/ramen/e2e/dractions"
	"github.com/ramendr/ramen/e2e/util"
	"github.com/ramendr/ramen/e2e/workloads"
)

type BasicSuite struct {
	w   workloads.Workload
	d   deployers.Deployer
	ctx *util.TestContext
	r   dractions.DRActions
}

func (s *BasicSuite) SetContext(ctx *util.TestContext) {
	ctx.Log.Info("enter SetContext")
	s.ctx = ctx
}

func (s *BasicSuite) SetupSuite() error {
	s.ctx.Log.Info("enter SetupSuite")
	s.w = workloads.BusyboxDeployment{Ctx: s.ctx}
	s.d = deployers.Subscription{Ctx: s.ctx}
	s.r = dractions.DRActions{Ctx: s.ctx}

	return nil
}

func (s *BasicSuite) TeardownSuite() error {
	s.ctx.Log.Info("enter TeardownSuite")
	return nil
}

func (s *BasicSuite) Tests() []Test {
	s.ctx.Log.Info("enter Tests")
	return []Test{
		s.TestWorkloadDeployment,
		s.TestEnableProtection,
		s.TestWorkloadFailover,
		s.TestWorkloadRelocation,
		s.TestDisableProtection,
		s.TestWorkloadUndeployment,
	}
}

func (s *BasicSuite) TestWorkloadDeployment() error {
	s.ctx.Log.Info("enter TestWorkloadDeployment")
	s.d.Deploy(s.w)
	return nil
}

func (s *BasicSuite) TestEnableProtection() error {
	s.ctx.Log.Info("enter TestEnableProtection")
	s.r.EnableProtection(s.w, s.d)
	return nil
}

func (s *BasicSuite) TestWorkloadFailover() error {
	s.ctx.Log.Info("enter TestWorkloadFailover")
	s.r.Failover(s.w, s.d)
	return nil
}

func (s *BasicSuite) TestWorkloadRelocation() error {
	s.ctx.Log.Info("enter TestWorkloadRelocation")
	s.r.Relocate(s.w, s.d)
	return nil
}

func (s *BasicSuite) TestDisableProtection() error {
	s.ctx.Log.Info("enter TestDisableProtection")
	return nil
}

func (s *BasicSuite) TestWorkloadUndeployment() error {
	s.ctx.Log.Info("enter TestWorkloadUndeployment")
	s.d.Undeploy(s.w)
	return nil
}
