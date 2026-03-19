package terminal

import "strings"

const openLobsterPrefix = "MYPAL_"

// FilterMyPalFromEnv returns a copy of env with all MYPAL_*
// variables removed. Used when spawning subprocesses (terminal_exec,
// terminal_spawn) so the encryption key and other secrets are never leaked.
// Also use for user-provided env so MYPAL_* is stripped even if the
// LLM requests it.
func FilterMyPalFromEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if idx := strings.IndexByte(e, '='); idx > 0 {
			if !strings.HasPrefix(strings.ToUpper(e[:idx]), openLobsterPrefix) {
				out = append(out, e)
			}
		} else {
			out = append(out, e)
		}
	}
	return out
}
