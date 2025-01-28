// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const logFilePath = "ramen-e2e.log"

func CreateLogger() (*zap.SugaredLogger, error) {
	// Console encoder config
	consoleConfig := zap.NewProductionEncoderConfig()
	consoleConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	consoleConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	consoleConfig.CallerKey = zapcore.OmitKey
	consoleEncoder := zapcore.NewConsoleEncoder(consoleConfig)

	// Logfile encoder config
	logfileConfig := zap.NewProductionEncoderConfig()
	logfileConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logfileConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	logfileEncoder := zapcore.NewConsoleEncoder(logfileConfig)

	logfile, err := os.Create(logFilePath)
	if err != nil {
		return nil, err
	}

	core := zapcore.NewTee(
		zapcore.NewCore(logfileEncoder, zapcore.AddSync(logfile), zapcore.DebugLevel),
		zapcore.NewCore(consoleEncoder, zapcore.AddSync(os.Stderr), zapcore.InfoLevel),
	)
	logger := zap.New(core).Sugar()

	return logger, nil
}
