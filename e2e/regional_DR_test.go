package e2e_test

import (
	"fmt"

	"github.com/stretchr/testify/suite"
)

type RegionalDRSuite struct {
	suite.Suite
	testContext *TestContext
}

func (suite *RegionalDRSuite) SetupSuite() {
	// Add setup logic here if needed
}

func (suite *RegionalDRSuite) TearDownSuite() {
	// Add teardown logic here if needed
}

func (suite *RegionalDRSuite) TestRegionalDR() {
	fmt.Println("TestRegionalDR was run")

	// runOnAllOCPClusters(suite.testContext, getDefaultNamespace)
}
