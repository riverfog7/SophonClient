package internal

// LogLevel represents the severity level of a log message
type LogLevel int

const (
	Info LogLevel = iota
	Warning
	Error
	Debug
)

// LogStruct represents a log entry with a level and message
type LogStruct struct {
	LogLevel LogLevel
	Message  string
}

// LogHandlerFunc defines the function signature for log handlers
type LogHandlerFunc func(sender interface{}, log LogStruct)

// LogHandler is the global event handler for logs
var LogHandler LogHandlerFunc

// PushLogDebug sends a debug log message
// Note: Go doesn't have extension methods like C#, so we pass the sender explicitly
func PushLogDebug(sender interface{}, message string) {
	if LogHandler != nil {
		LogHandler(sender, LogStruct{
			LogLevel: Debug,
			Message:  message,
		})
	}
}

// PushLogInfo sends an info log message
func PushLogInfo(sender interface{}, message string) {
	if LogHandler != nil {
		LogHandler(sender, LogStruct{
			LogLevel: Info,
			Message:  message,
		})
	}
}

// PushLogWarning sends a warning log message
func PushLogWarning(sender interface{}, message string) {
	if LogHandler != nil {
		LogHandler(sender, LogStruct{
			LogLevel: Warning,
			Message:  message,
		})
	}
}

// PushLogError sends an error log message
func PushLogError(sender interface{}, message string) {
	if LogHandler != nil {
		LogHandler(sender, LogStruct{
			LogLevel: Error,
			Message:  message,
		})
	}
}
