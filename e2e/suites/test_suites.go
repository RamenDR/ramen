package suites

import "samples.foo/e2e/util"

type Test func() error

type TestSuite interface {
	SetContext(ctx *util.TestContext)
	SetupSuite() error
	TeardownSuite() error
	Tests() []Test
}
