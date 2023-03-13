package e2e_test

import (
	"context"

	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InfraValidationSuite struct {
	suite.Suite
	testContext *TestContext
}

func (suite *InfraValidationSuite) SetupSuite() {
	// Add setup logic here if needed
}

func (suite *InfraValidationSuite) TearDownSuite() {
	// Add teardown logic here if needed
}

// TestConnectionToCluster is a test that validates that the e2e test suite can connect to the
// clusters specified in the configuration file. It does so by querying the default namespace of
// each cluster.
func (suite *InfraValidationSuite) TestConnectionToCluster() {
	assert := suite.Assert()

	hubNamespace, err := suite.testContext.HubClient().CoreV1().Namespaces().Get(context.Background(),
		"default", metav1.GetOptions{})
	assert.NoError(err)

	c1Namespace, err := suite.testContext.C1Client().CoreV1().Namespaces().Get(context.Background(),
		"default", metav1.GetOptions{})
	assert.NoError(err)

	c2Namespace, err := suite.testContext.C2Client().CoreV1().Namespaces().Get(context.Background(),
		"default", metav1.GetOptions{})
	assert.NoError(err)

	assert.NotNil(hubNamespace)
	assert.NotNil(c1Namespace)
	assert.NotNil(c2Namespace)
}
