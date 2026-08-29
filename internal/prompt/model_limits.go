package prompt

// DefaultModelLimits contains conservative context-window token caps for the
// default models documented in config and README. These values deliberately
// leave headroom below the providers' advertised maximum context windows.
var DefaultModelLimits = map[string]int{
	"claude-3-5-sonnet-20241022": 180_000,
	"gemini-2.5-pro":             1_000_000,
	"gpt-5":                      360_000,
}
