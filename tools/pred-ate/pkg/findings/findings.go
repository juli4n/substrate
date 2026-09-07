// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package findings

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

type Verdict string

const (
	VerdictConfirmedBug      Verdict = "CONFIRMED_BUG"
	VerdictVerifiedResilient Verdict = "VERIFIED_RESILIENT"
	VerdictInconclusive      Verdict = "INCONCLUSIVE"
)

type FindingMeta struct {
	ID               string   `json:"id"`
	Verdict          Verdict  `json:"verdict"`
	Subsystem        string   `json:"subsystem"`
	Components       []string `json:"components,omitempty"`
	TestedHypothesis string   `json:"tested_hypothesis"`
	Severity         string   `json:"severity,omitempty"`
	Reproducer       string   `json:"reproducer,omitempty"`
	CreatedAt        string   `json:"created_at,omitempty"`

	FilePath string `json:"-"`
}

var frontmatterRegex = regexp.MustCompile(`(?s)^---\r?\n(.*?)\r?\n---\r?\n(.*)$`)

// ParseFinding parses frontmatter and markdown body from file bytes.
func ParseFinding(data []byte) (*FindingMeta, string, error) {
	matches := frontmatterRegex.FindSubmatch(data)
	if len(matches) < 3 {
		return nil, "", fmt.Errorf("missing or invalid YAML frontmatter")
	}

	var meta FindingMeta
	if err := yaml.Unmarshal(matches[1], &meta); err != nil {
		return nil, "", fmt.Errorf("failed to parse YAML frontmatter: %w", err)
	}

	body := string(matches[2])
	return &meta, body, nil
}

// SerializeFinding formats a finding metadata struct and body into Markdown with YAML frontmatter.
func SerializeFinding(meta *FindingMeta, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(meta)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlBytes)
	buf.WriteString("---\n\n")
	buf.WriteString(strings.TrimSpace(body))
	buf.WriteString("\n")

	return buf.Bytes(), nil
}

// ListFindings finds and returns all recorded findings from the findings registry.
func ListFindings(findingsDir string) ([]FindingMeta, error) {
	var results []FindingMeta

	subdirs := []string{"bugs", "verified_resilient", "inconclusive"}
	for _, subdir := range subdirs {
		dir := filepath.Join(findingsDir, subdir)
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") || entry.Name() == "README.md" {
				continue
			}

			fullPath := filepath.Join(dir, entry.Name())
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}

			meta, _, err := ParseFinding(data)
			if err != nil {
				continue
			}
			meta.FilePath = fullPath
			results = append(results, *meta)
		}
	}

	return results, nil
}

// PromoteFinding inspects the output directory of a test run and moves/formats findings into the persistent directory.
func PromoteFinding(runOutputDir, persistentFindingsDir string) (*FindingMeta, error) {
	findingFile := filepath.Join(runOutputDir, "finding.md")
	data, err := os.ReadFile(findingFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No finding recorded in this run
		}
		return nil, fmt.Errorf("failed to read finding from %s: %w", findingFile, err)
	}

	meta, body, err := ParseFinding(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse finding in %s: %w", findingFile, err)
	}

	// Calculate next finding ID
	allFindings, _ := ListFindings(persistentFindingsDir)
	nextNum := len(allFindings) + 1
	meta.ID = fmt.Sprintf("FINDING-%03d", nextNum)
	meta.CreatedAt = time.Now().UTC().Format(time.RFC3339)

	// Check for reproducer script in output directory
	reproducerPath := ""
	matches, _ := filepath.Glob(filepath.Join(runOutputDir, "reproducer.*"))
	for _, m := range matches {
		ext := filepath.Ext(m)
		destFileName := fmt.Sprintf("repro_%03d%s", nextNum, ext)
		destPath := filepath.Join(persistentFindingsDir, "reproducers", destFileName)
		_ = os.MkdirAll(filepath.Dir(destPath), 0755)
		if err := copyFile(m, destPath); err == nil {
			reproducerPath = filepath.Join("reproducers", destFileName)
			break
		}
	}
	if reproducerPath != "" {
		meta.Reproducer = reproducerPath
	}

	// Determine target destination directory based on verdict
	var targetSubdir string
	switch meta.Verdict {
	case VerdictConfirmedBug:
		targetSubdir = "bugs"
	case VerdictVerifiedResilient:
		targetSubdir = "verified_resilient"
	default:
		targetSubdir = "inconclusive"
	}

	slug := slugify(meta.TestedHypothesis)
	if slug == "" {
		slug = "unnamed"
	}
	destFileName := fmt.Sprintf("%s-%s.md", meta.ID, slug)
	destDir := filepath.Join(persistentFindingsDir, targetSubdir)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}

	destPath := filepath.Join(destDir, destFileName)
	outBytes, err := SerializeFinding(meta, body)
	if err != nil {
		return nil, err
	}

	if err := os.WriteFile(destPath, outBytes, 0644); err != nil {
		return nil, fmt.Errorf("failed to write promoted finding to %s: %w", destPath, err)
	}

	meta.FilePath = destPath
	return meta, nil
}

func slugify(s string) string {
	s = strings.ToLower(s)
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	s = reg.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = s[:40]
		s = strings.TrimRight(s, "-")
	}
	return s
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
