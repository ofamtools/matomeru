package mdweb

import (
	"regexp"
	"strings"
)

// MermaidOrientationInfo describes the detected diagram family, current layout
// direction, and whether orientation flipping is supported.
type MermaidOrientationInfo struct {
	DiagramType string `json:"diagramType"`
	Direction   string `json:"direction"`
	Flippable   bool   `json:"flippable"`
}

var (
	mermaidFlowHeaderRe = regexp.MustCompile(`(?i)^(?:flowchart|graph)(?:\s+(LR|RL|TD|TB|BT))?(?:\s|;|$)`)
	mermaidFlowSubRe    = regexp.MustCompile(`(?i)^(\s*(?:flowchart|graph))(?:\s+(?:LR|RL|TD|TB|BT))?`)
	mermaidStateRe      = regexp.MustCompile(`(?i)^stateDiagram(?:-v2)?\b`)
	mermaidClassRe      = regexp.MustCompile(`(?i)^classDiagram\b`)
	mermaidERRe         = regexp.MustCompile(`(?i)^erDiagram\b`)
	mermaidDirLineRe    = regexp.MustCompile(`(?i)^direction\s+(LR|RL|TB|TD|BT)\b`)
	mermaidDirSubRe     = regexp.MustCompile(`(?i)^(\s*direction\s+)(?:LR|RL|TB|TD|BT)\b`)
)

func findMermaidHeaderLineIndex(lines []string) int {
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		return i
	}
	return -1
}

func isValidMermaidDirection(dir string) bool {
	switch dir {
	case "LR", "RL", "TD", "TB", "BT":
		return true
	default:
		return false
	}
}

func oppositeMermaidDirection(dir, diagramType string) string {
	vertical := "TB"
	if diagramType == "flowchart" {
		vertical = "TD"
	}
	switch dir {
	case "LR":
		return vertical
	case "RL":
		return "BT"
	case "TD", "TB":
		return "LR"
	case "BT":
		return "RL"
	default:
		return vertical
	}
}

// DetectMermaidOrientation inspects Mermaid source to determine its diagram
// family, current layout direction, and whether orientation can be flipped.
func DetectMermaidOrientation(rawCode string) MermaidOrientationInfo {
	normalized := strings.ReplaceAll(rawCode, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	headerIdx := findMermaidHeaderLineIndex(lines)
	if headerIdx == -1 {
		return MermaidOrientationInfo{DiagramType: "other", Direction: "", Flippable: false}
	}
	header := strings.TrimSpace(lines[headerIdx])

	if m := mermaidFlowHeaderRe.FindStringSubmatch(header); m != nil {
		dir := strings.ToUpper(m[1])
		if dir == "" {
			dir = "TD"
		}
		return MermaidOrientationInfo{
			DiagramType: "flowchart",
			Direction:   dir,
			Flippable:   true,
		}
	}

	diagramType := "other"
	switch {
	case mermaidStateRe.MatchString(header):
		diagramType = "state"
	case mermaidClassRe.MatchString(header):
		diagramType = "class"
	case mermaidERRe.MatchString(header):
		diagramType = "er"
	}

	if diagramType != "other" {
		for i := headerIdx + 1; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == "" || strings.HasPrefix(t, "%%") {
				continue
			}
			if m := mermaidDirLineRe.FindStringSubmatch(t); m != nil {
				d := strings.ToUpper(m[1])
				if d == "TD" {
					d = "TB"
				}
				return MermaidOrientationInfo{
					DiagramType: diagramType,
					Direction:   d,
					Flippable:   true,
				}
			}
			break
		}
		return MermaidOrientationInfo{
			DiagramType: diagramType,
			Direction:   "TB",
			Flippable:   true,
		}
	}

	return MermaidOrientationInfo{DiagramType: "other", Direction: "", Flippable: false}
}

// SetMermaidDirection updates or flips the layout direction of a flippable
// Mermaid diagram while preserving the original line ending style. When target
// is empty, the current direction is flipped to its opposite axis.
func SetMermaidDirection(rawCode string, target string) (string, MermaidOrientationInfo) {
	info := DetectMermaidOrientation(rawCode)
	if !info.Flippable || info.Direction == "" {
		return rawCode, info
	}

	eol := "\n"
	if strings.Contains(rawCode, "\r\n") {
		eol = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(rawCode, "\r\n", "\n"), "\n")
	headerIdx := findMermaidHeaderLineIndex(lines)
	if headerIdx == -1 {
		return rawCode, info
	}

	nextRaw := strings.ToUpper(strings.TrimSpace(target))
	if nextRaw == "" {
		nextRaw = oppositeMermaidDirection(info.Direction, info.DiagramType)
	} else if !isValidMermaidDirection(nextRaw) {
		return rawCode, info
	}

	nextDir := nextRaw
	if info.DiagramType != "flowchart" && nextDir == "TD" {
		nextDir = "TB"
	}

	if info.DiagramType == "flowchart" {
		lines[headerIdx] = mermaidFlowSubRe.ReplaceAllString(lines[headerIdx], "${1} "+nextDir)
		updated := strings.Join(lines, eol)
		return updated, DetectMermaidOrientation(updated)
	}

	for i := headerIdx + 1; i < len(lines); i++ {
		t := strings.TrimSpace(lines[i])
		if t == "" || strings.HasPrefix(t, "%%") {
			continue
		}
		if mermaidDirLineRe.MatchString(t) {
			lines[i] = mermaidDirSubRe.ReplaceAllString(lines[i], "${1}"+nextDir)
			updated := strings.Join(lines, eol)
			return updated, DetectMermaidOrientation(updated)
		}
		break
	}

	out := make([]string, 0, len(lines)+1)
	out = append(out, lines[:headerIdx+1]...)
	out = append(out, "  direction "+nextDir)
	out = append(out, lines[headerIdx+1:]...)
	updated := strings.Join(out, eol)
	return updated, DetectMermaidOrientation(updated)
}
