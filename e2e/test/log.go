// SPDX-FileCopyrightText: The RamenDR authors
// SPDX-License-Identifier: Apache-2.0

package test

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

	core := zapcore.NewTee(
		zapcore.NewCore(logfileEncoder, zapcore.Lock(logfile), zapcore.DebugLevel),
		zapcore.NewCore(consoleEncoder, zapcore.Lock(os.Stderr), zapcore.InfoLevel),
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return logger.Sugar(), nil
}
