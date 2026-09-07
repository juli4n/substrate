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

package cmd

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/agent-substrate/substrate/tools/pred-ate/pkg/findings"
	"github.com/spf13/cobra"
)

var filterVerdict string

var findingsCmd = &cobra.Command{
	Use:   "findings",
	Short: "List all findings recorded by pred-ate",
	Long:  `Scan and display all bug reports and verified resilient behaviors in the findings registry.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		findingsAbs, err := filepath.Abs(globalCfg.FindingsDir)
		if err != nil {
			return err
		}

		all, err := findings.ListFindings(findingsAbs)
		if err != nil {
			return fmt.Errorf("failed to list findings: %w", err)
		}

		if len(all) == 0 {
			cmd.Println("No findings recorded yet in", findingsAbs)
			return nil
		}

		cmd.Printf("Found %d findings in %s:\n\n", len(all), findingsAbs)
		cmd.Printf("%-14s %-20s %-16s %s\n", "ID", "VERDICT", "SUBSYSTEM", "TESTED HYPOTHESIS")
		cmd.Println(strings.Repeat("-", 80))

		for _, f := range all {
			if filterVerdict != "" && !strings.EqualFold(string(f.Verdict), filterVerdict) {
				continue
			}
			hyp := f.TestedHypothesis
			if len(hyp) > 40 {
				hyp = hyp[:37] + "..."
			}
			cmd.Printf("%-14s %-20s %-16s %s\n", f.ID, f.Verdict, f.Subsystem, hyp)
		}

		return nil
	},
}

func init() {
	findingsCmd.Flags().StringVar(&filterVerdict, "verdict", "", "Filter by verdict (CONFIRMED_BUG, VERIFIED_RESILIENT, INCONCLUSIVE)")
	rootCmd.AddCommand(findingsCmd)
}
