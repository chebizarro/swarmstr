package agent

import (
	"regexp"
	"strings"
)

var identityStatementPattern = regexp.MustCompile("(?im)\\bi am\\b\\s+(?:\\*\\*|__|`)?([A-Z][A-Za-z0-9 ._'-]{1,60})(?:\\*\\*|__|`)?")

// ResolveWorkspaceIdentityName attempts to derive a human-readable agent name
// from workspace bootstrap files. IDENTITY.md is authoritative; SOUL.md is a
// fallback when no explicit identity name is present.
func ResolveWorkspaceIdentityName(workspaceDir string) string {
	files, _ := LoadWorkspaceBootstrapFiles(workspaceDir, []string{"IDENTITY.md", "SOUL.md"})
	if len(files) == 0 {
		return ""
	}
	var identityContent, soulContent string
	for _, file := range files {
		switch strings.TrimSpace(file.Name) {
		case "IDENTITY.md":
			identityContent = file.Content
		case "SOUL.md":
			soulContent = file.Content
		}
	}
	if name := extractIdentityName(identityContent); name != "" {
		return name
	}
	if name := extractSoulName(soulContent); name != "" {
		return name
	}
	return ""
}

func extractIdentityName(content string) string {
	if name := extractMarkdownFieldValue(content, "name"); name != "" {
		return name
	}
	return extractHeadingIdentityName(content)
}

func extractSoulName(content string) string {
	if name := extractMarkdownFieldValue(content, "name"); name != "" {
		return name
	}
	if name := extractHeadingIdentityName(content); name != "" {
		return name
	}
	matches := identityStatementPattern.FindStringSubmatch(content)
	if len(matches) < 2 {
		return ""
	}
	return cleanIdentityName(matches[1])
}

func extractMarkdownFieldValue(content, key string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	needle := strings.ToLower(strings.TrimSpace(key)) + ":"
	for i := 0; i < len(lines); i++ {
		line := normalizeMarkdownLabel(lines[i])
		if !strings.HasPrefix(strings.ToLower(line), needle) {
			continue
		}
		value := cleanIdentityName(strings.TrimSpace(line[len(needle):]))
		if value != "" {
			return value
		}
		for j := i + 1; j < len(lines); j++ {
			next := strings.TrimSpace(lines[j])
			if next == "" {
				continue
			}
			if strings.HasPrefix(next, "-") || strings.HasPrefix(next, "*") || strings.HasPrefix(next, "#") {
				break
			}
			value = cleanIdentityName(next)
			if value != "" {
				return value
			}
			break
		}
	}
	return ""
}

func extractHeadingIdentityName(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if heading == "" {
			continue
		}
		candidates := []string{
			trimHeadingPrefix(heading, "IDENTITY.md"),
			trimHeadingPrefix(heading, "SOUL.md"),
			heading,
		}
		for _, candidate := range candidates {
			if name := cleanHeadingIdentityName(candidate); name != "" {
				return name
			}
		}
	}
	return ""
}

func trimHeadingPrefix(heading, prefix string) string {
	heading = strings.TrimSpace(heading)
	if !strings.HasPrefix(strings.ToLower(heading), strings.ToLower(prefix)) {
		return heading
	}
	trimmed := strings.TrimSpace(heading[len(prefix):])
	trimmed = strings.TrimLeft(trimmed, " -—:\t")
	return strings.TrimSpace(trimmed)
}

func cleanHeadingIdentityName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	switch lower {
	case "who am i?", "who am i", "who i am", "who you are", "the soul", "identity", "soul":
		return ""
	}
	for _, prefix := range []string{"the soul of ", "soul of "} {
		if strings.HasPrefix(lower, prefix) {
			return cleanIdentityName(strings.TrimSpace(value[len(prefix):]))
		}
	}
	if strings.Contains(lower, "template") || strings.HasPrefix(lower, "identity.md") || strings.HasPrefix(lower, "soul.md") {
		return ""
	}
	return cleanIdentityName(value)
}

func normalizeMarkdownLabel(line string) string {
	line = strings.TrimSpace(line)
	line = strings.TrimLeft(line, "-* \t")
	replacer := strings.NewReplacer("**", "", "__", "", "`", "")
	return strings.TrimSpace(replacer.Replace(line))
}

func cleanIdentityName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "*_`\"'")
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "_(") || strings.HasPrefix(lower, "(") {
		return ""
	}
	for _, marker := range []string{
		"pick something",
		"fill this in",
		"your signature",
		"workspace-relative path",
		"your nostr pubkey",
		"what are you here to do",
		"how do you come across",
	} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	return value
}
