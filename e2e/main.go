package main

import (
	"fmt"
	"log"
	"os"

	"github.com/go-logr/zapr"
	"github.com/ramendr/ramen/e2e/suite"
	"github.com/ramendr/ramen/e2e/validateenvironment"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	cobra.OnInitialize(initConfig)
}

func initConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
}

var defaultSuites = []suite.TestSuite{
	&validateenvironment.TestSuite{},
}

var allsuites = []suite.TestSuite{
	&validateenvironment.TestSuite{},
}

func setupLogger(testContext *suite.TestContext) {
	encoderConfig := zap.NewDevelopmentEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderConfig),
		zapcore.AddSync(os.Stdout),
		zap.DebugLevel,
	)

	zapLog := zap.New(core, zap.Development())
	testContext.Logger = zapr.NewLogger(zapLog)
}

func runDefaultTestSuites(cmd *cobra.Command, args []string) {
	var exitCode int

	config, err := readConfig()
	if err != nil {
		log.Fatalf("failed to read configuration: %v", err)
	}

	testContext, err := suite.NewTestContext(config)
	if err != nil {
		log.Fatalf("failed to create TestContext: %v\n", err)
	}

	setupLogger(testContext)

	for _, s := range defaultSuites {
		testContext.Logger.Info("Running suite", "suite", s.Name())

		if err := suite.RunSuite(s, testContext); err != nil {
			testContext.Logger.Error(err, "Suite failed", "suite", s.Name())

			exitCode = 1

			break
		}
	}

	testContext.Logger.Info("Exiting", "exitCode", exitCode)
}

func runEnabledTestSuites(cmd *cobra.Command, args []string) {
	var exitCode int

	config, err := readConfig()
	if err != nil {
		log.Fatalf("failed to read configuration: %v", err)
	}

	testContext, err := suite.NewTestContext(config)
	if err != nil {
		log.Fatalf("failed to create TestContext: %v\n", err)
	}

	setupLogger(testContext)

	for _, s := range allsuites {
		if !s.IsEnabled() {
			testContext.Logger.Info("Skipping suite", "suite", s.Name())

			continue
		}

		testContext.Logger.Info("Running suite", "suite", s.Name())

		if err := suite.RunSuite(s, testContext); err != nil {
			testContext.Logger.Error(err, "Suite failed", "suite", s.Name())

			exitCode = 1

			break
		}
	}

	testContext.Logger.Info("Exiting", "exitCode", exitCode)
}

func listTests(cmd *cobra.Command, args []string) {
	for _, s := range allsuites {
		//nolint:forbidigo,revive
		fmt.Println(s.Name(), " - ", s.Description())
	}
}

func main() {
	rootCmd := &cobra.Command{
		Use:   "ramen_e2e",
		Short: "ramen_e2e runs the default ramen end-to-end tests",
	}

	runDefaultSuitesCmd := &cobra.Command{
		Use:   "run-default-suites",
		Short: "run-default-suites runs the default test suites",
		Run:   runDefaultTestSuites,
	}
	rootCmd.AddCommand(runDefaultSuitesCmd)

	runEnabledSuitesCmd := &cobra.Command{
		Use:   "run-enabled-suites",
		Short: "run-enabled-suites runs only the enabled test suites",
		Run:   runEnabledTestSuites,
	}
	rootCmd.AddCommand(runEnabledSuitesCmd)
	runEnabledSuitesCmd.Flags().BoolVar(&validateenvironment.Enabled, "validate-environment", false,
		"Enable the ValidateEnvironmentTestSuite")

	listSuitesCmd := &cobra.Command{
		Use:   "list-suites",
		Short: "List all test suites",
		Run:   listTests,
	}
	rootCmd.AddCommand(listSuitesCmd)

	if err := rootCmd.Execute(); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
