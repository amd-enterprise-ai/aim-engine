package utils

import "github.com/go-logr/logr"

const (
	DebugLogLevel = 1
	//TraceLogLevel = 2
)

func Debug(logger logr.Logger, fmt string, keysAndValues ...any) {
	logger.V(DebugLogLevel).Info(fmt, keysAndValues...)
}
