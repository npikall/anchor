package main

import "strings"

const gitSHALength = 40

type usesDirective struct {
	prefix   string
	repoPath string
	ref      string
	quote    string
}

func parseUsesLine(line string) (usesDirective, bool, bool) {
	prefix, value, ok := extractUsesValue(line)
	if !ok {
		return usesDirective{}, true, false
	}
	value, quote := unquote(value)

	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "docker://") {
		return usesDirective{}, false, true
	}

	atIdx := strings.LastIndex(value, "@")
	if atIdx == -1 || atIdx == len(value)-1 {
		return usesDirective{}, true, false
	}

	return usesDirective{
		prefix:   prefix,
		repoPath: value[:atIdx],
		ref:      value[atIdx+1:],
		quote:    quote,
	}, false, false
}

func extractUsesValue(line string) (string, string, bool) {
	usesKeyEnd := findUsesKeyEnd(line)
	if usesKeyEnd == -1 {
		return "", "", false
	}

	valueStart := usesKeyEnd
	for valueStart < len(line) && line[valueStart] == ' ' {
		valueStart++
	}
	prefix := line[:valueStart]

	raw := line[valueStart:]
	if ci := strings.Index(raw, " #"); ci != -1 {
		raw = strings.TrimSpace(raw[:ci])
	}
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", "", false
	}
	return prefix, value, true
}

// unquote strips a matching pair of YAML quotes and returns the quote it removed.
func unquote(value string) (string, string) {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1], value[:1]
	}
	return value, ""
}

func findUsesKeyEnd(line string) int {
	if idx := strings.Index(line, "- uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		return idx + len("- uses:")
	}
	if idx := strings.Index(line, "uses:"); idx != -1 && !strings.Contains(line[:idx], "#") {
		return idx + len("uses:")
	}
	return -1
}

func splitOwnerRepo(repoPath string) (string, string) {
	owner, rest, _ := strings.Cut(repoPath, "/")
	repo, _, _ := strings.Cut(rest, "/")
	return owner, repo
}

func isSHA(ref string) bool {
	return len(ref) == gitSHALength
}
