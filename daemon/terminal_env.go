package main

import "strings"

const defaultTerminalLocale = "en_US.UTF-8"

func terminalEnv(base []string) []string {
	env := setEnvValue(base, "TERM", "xterm-256color")
	env = setEnvValue(env, "COLORTERM", "truecolor")
	env = ensureUTF8Locale(env)
	return env
}

func ensureUTF8Locale(env []string) []string {
	lang := envValue(env, "LANG")
	lcCType := envValue(env, "LC_CTYPE")
	lcAll := envValue(env, "LC_ALL")
	locale := firstUTF8Locale(lcAll, lcCType, lang)
	if locale == "" {
		locale = defaultTerminalLocale
	}
	if lcAll != "" && !isUTF8Locale(lcAll) {
		env = setEnvValue(env, "LC_ALL", locale)
	}
	if !isUTF8Locale(lang) {
		env = setEnvValue(env, "LANG", locale)
	}
	if !isUTF8Locale(lcCType) {
		env = setEnvValue(env, "LC_CTYPE", locale)
	}
	return env
}

func firstUTF8Locale(values ...string) string {
	for _, value := range values {
		if isUTF8Locale(value) {
			return value
		}
	}
	return ""
}

func isUTF8Locale(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "-", ""))
	return strings.Contains(normalized, "utf8")
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	found := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !found {
				next = append(next, prefix+value)
				found = true
			}
			continue
		}
		next = append(next, entry)
	}
	if !found {
		next = append(next, prefix+value)
	}
	return next
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}
