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
	"os"
	"path/filepath"
	"testing"
)

func TestParseAndSerializeFinding(t *testing.T) {
	content := `---
id: FINDING-001
verdict: CONFIRMED_BUG
subsystem: cmd/ateapi
components:
- store
- scheduler
tested_hypothesis: Rapid ResumeActor before snapshot upload finishes
severity: HIGH
created_at: 2026-09-06T16:00:00Z
---

## Summary
The worker remained in BUSY state.
`

	meta, body, err := ParseFinding([]byte(content))
	if err != nil {
		t.Fatalf("ParseFinding failed: %v", err)
	}

	if meta.ID != "FINDING-001" {
		t.Errorf("expected ID 'FINDING-001', got '%s'", meta.ID)
	}
	if meta.Verdict != VerdictConfirmedBug {
		t.Errorf("expected verdict '%s', got '%s'", VerdictConfirmedBug, meta.Verdict)
	}
	if meta.Severity != "HIGH" {
		t.Errorf("expected severity 'HIGH', got '%s'", meta.Severity)
	}

	serialized, err := SerializeFinding(meta, body)
	if err != nil {
		t.Fatalf("SerializeFinding failed: %v", err)
	}

	meta2, _, err := ParseFinding(serialized)
	if err != nil {
		t.Fatalf("ParseFinding on serialized failed: %v", err)
	}
	if meta2.ID != meta.ID || meta2.Verdict != meta.Verdict {
		t.Errorf("mismatch after roundtrip: %+v vs %+v", meta, meta2)
	}
}

func TestPromoteFinding(t *testing.T) {
	tempBase := t.TempDir()
	runOutput := filepath.Join(tempBase, "run-output")
	findingsDir := filepath.Join(tempBase, "persistent-findings")

	if err := os.MkdirAll(runOutput, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(findingsDir, 0755); err != nil {
		t.Fatal(err)
	}

	findingContent := `---
verdict: CONFIRMED_BUG
subsystem: cmd/ateapi
tested_hypothesis: Worker assignment race on burst
severity: CRITICAL
---

## Result
Deadlock detected in postgres store.
`
	if err := os.WriteFile(filepath.Join(runOutput, "finding.md"), []byte(findingContent), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runOutput, "reproducer.sh"), []byte("#!/bin/bash\necho fail"), 0755); err != nil {
		t.Fatal(err)
	}

	promoted, err := PromoteFinding(runOutput, findingsDir)
	if err != nil {
		t.Fatalf("PromoteFinding failed: %v", err)
	}

	if promoted == nil {
		t.Fatal("expected promoted finding, got nil")
	}
	if promoted.ID != "FINDING-001" {
		t.Errorf("expected ID 'FINDING-001', got '%s'", promoted.ID)
	}
	if promoted.Verdict != VerdictConfirmedBug {
		t.Errorf("expected verdict CONFIRMED_BUG, got '%s'", promoted.Verdict)
	}
	if promoted.Reproducer == "" {
		t.Errorf("expected reproducer to be linked, got empty")
	}

	// Verify it shows up in ListFindings
	list, err := ListFindings(findingsDir)
	if err != nil {
		t.Fatalf("ListFindings failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 finding in list, got %d", len(list))
	}
	if list[0].ID != "FINDING-001" {
		t.Errorf("expected list item ID 'FINDING-001', got '%s'", list[0].ID)
	}
}
