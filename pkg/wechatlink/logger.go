package wechatlink

// Logger 日志接口（与 wecomaibot.Logger 一致，由接入方实现）。
type Logger interface {
	Debug(format string, v ...any)
	Info(format string, v ...any)
	Warn(format string, v ...any)
	Error(format string, v ...any)
}

// LoggerFunc 将 (level, format, args) 回调适配为 Logger。
type LoggerFunc func(level string, format string, v ...any)

func (f LoggerFunc) Debug(format string, v ...any) { f("DEBUG", format, v...) }
func (f LoggerFunc) Info(format string, v ...any)  { f("INFO", format, v...) }
func (f LoggerFunc) Warn(format string, v ...any)  { f("WARN", format, v...) }
func (f LoggerFunc) Error(format string, v ...any) { f("ERROR", format, v...) }

// NewLoggerFunc 由 level + format 回调构造 Logger。
func NewLoggerFunc(fn func(level, format string, v ...any)) Logger {
	return LoggerFunc(fn)
}

type nopLogger struct{}

func (nopLogger) Debug(string, ...any) {}
func (nopLogger) Info(string, ...any)  {}
func (nopLogger) Warn(string, ...any)  {}
func (nopLogger) Error(string, ...any) {}
