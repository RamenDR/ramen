// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/klog/v2"
	crlog "sigs.k8s.io/controller-runtime/pkg/log"
)

func CreateLogger(logFilePath string) (*zap.SugaredLogger, error) {
	// In the console we don't want details such as file:line or stacktraces.
	consoleConfig := zap.NewProductionEncoderConfig()
	consoleConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	consoleConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	consoleConfig.CallerKey = zapcore.OmitKey
	consoleConfig.StacktraceKey = zapcore.OmitKey
	consoleEncoder := zapcore.NewConsoleEncoder(consoleConfig)

	// In the log file we want everything that can help to debug failure.
	logfileConfig := zap.NewProductionEncoderConfig()
	logfileConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logfileConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	logfileEncoder := zapcore.NewConsoleEncoder(logfileConfig)

	logfile, err := os.Create(logFilePath)
	if err != nil {
		return nil, err
	}

	/*
		The test logger writes to the log file and the console. klog and
		controller-runtime use the library logger, which writes only to the
		same log file:

		testLogger
		├── fileCore
		└── consoleCore

		klog ──┐
		ctrl ──┴── libraryLogger
		           └── fileCore
	*/

	// Both testLogger and libraryLogger use same options.
	options := []zap.Option{
		zap.AddCaller(),
		zap.AddStacktrace(zapcore.ErrorLevel),
	}

	// The testLogger writes to log file and console.
	fileCore := zapcore.NewCore(logfileEncoder, zapcore.Lock(logfile), zapcore.DebugLevel)
	consoleCore := zapcore.NewCore(consoleEncoder, zapcore.Lock(os.Stderr), zapcore.InfoLevel)
	testLogger := zap.New(zapcore.NewTee(fileCore, consoleCore), options...)

	// The libraryLogger writes messages from libraries the log file.
	libraryLogger := zap.New(fileCore, options...)
	klog.SetLogger(zapr.NewLogger(libraryLogger.Named("klog")))
	crlog.SetLogger(zapr.NewLogger(libraryLogger.Named("ctrl")))

	return testLogger.Sugar(), nil
}
