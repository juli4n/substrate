// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

type move struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type replacement struct {
	File string `json:"file"` // glob pattern relative to repo root
	Old  string `json:"old"`
	New  string `json:"new"`
}

type rules struct {
	Moves        []move        `json:"moves"`
	Replacements []replacement `json:"replacements"`
}

type migrator struct {
	root         string
	module       string
	moves        []move // sorted longest-from first for specificity
	replacements []replacement
	dryRun       bool
}

func main() {
	dryRun := flag.Bool("dry-run", false, "print what would change without touching files")
	flag.Parse()

	root, err := repoRoot()
	if err != nil {
		log.Fatalf("finding repo root: %v", err)
	}

	module, err := readModule(filepath.Join(root, "go.mod"))
	if err != nil {
		log.Fatalf("reading module name: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "tools", "reorg", "rules.json"))
	if err != nil {
		log.Fatalf("reading rules.json: %v", err)
	}

	var r rules
	if err := json.Unmarshal(data, &r); err != nil {
		log.Fatalf("parsing rules.json: %v", err)
	}

	moves := r.Moves
	sort.Slice(moves, func(i, j int) bool {
		return len(moves[i].From) > len(moves[j].From)
	})

	m := &migrator{root: root, module: module, moves: moves, replacements: r.Replacements, dryRun: *dryRun}
	if err := m.run(); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
}

func (m *migrator) run() error {
	if err := m.moveFiles(); err != nil {
		return fmt.Errorf("moving files: %w", err)
	}
	if err := m.rewriteGoFiles(); err != nil {
		return fmt.Errorf("rewriting Go imports: %w", err)
	}
	if err := m.rewriteProtoFiles(); err != nil {
		return fmt.Errorf("rewriting proto files: %w", err)
	}
	if err := m.applyReplacements(); err != nil {
		return fmt.Errorf("applying replacements: %w", err)
	}
	if !m.dryRun {
		if err := m.removeEmptyDirs(); err != nil {
			return fmt.Errorf("removing empty dirs: %w", err)
		}
	}
	return nil
}

// moveFiles moves every file that matches a rule to its new location.
// Moves are applied most-specific-first so a file in a/b/c uses the a/b/c
// rule rather than the a/b rule.
func (m *migrator) moveFiles() error {
	return filepath.WalkDir(m.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(m.root, path)
		if err != nil {
			return err
		}

		dst, ok := m.applyMove(rel)
		if !ok {
			return nil
		}

		dstAbs := filepath.Join(m.root, dst)
		fmt.Printf("move  %s\n   -> %s\n", rel, dst)

		if m.dryRun {
			return nil
		}

		if err := os.MkdirAll(filepath.Dir(dstAbs), 0o755); err != nil {
			return err
		}
		return os.Rename(path, dstAbs)
	})
}

// applyMove returns the destination path for a given source path using the
// most specific matching rule, or ("", false) if no rule matches.
func (m *migrator) applyMove(relPath string) (string, bool) {
	relPath = filepath.ToSlash(relPath)
	for _, mv := range m.moves {
		from := mv.From
		if relPath == from || strings.HasPrefix(relPath, from+"/") {
			return mv.To + strings.TrimPrefix(relPath, from), true
		}
	}
	return "", false
}

// rewriteGoFiles rewrites import paths in all .go files under the repo root.
func (m *migrator) rewriteGoFiles() error {
	return filepath.WalkDir(m.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return m.rewriteGoFile(path)
	})
}

func (m *migrator) rewriteGoFile(filePath string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", filePath, err)
	}

	rel, _ := filepath.Rel(m.root, filePath)
	changed := false

	// Update package declaration if this file was moved to a directory with a different last segment.
	if mv, ok := m.findAppliedMove(rel); ok {
		oldPkg := path.Base(mv.From)
		newPkg := path.Base(mv.To)
		if oldPkg != newPkg && f.Name.Name == oldPkg {
			fmt.Printf("pkg   %s\n    package %s -> package %s\n", rel, oldPkg, newPkg)
			f.Name.Name = newPkg
			changed = true
		}
	}

	// Rewrite import paths and collect identifier renames for imports with no alias.
	type rename struct{ from, to string }
	var renames []rename

	for _, imp := range f.Imports {
		old := strings.Trim(imp.Path.Value, `"`)
		updated := m.rewriteImport(old)
		if updated == old {
			continue
		}
		fmt.Printf("import %s\n    %s\n -> %s\n", rel, old, updated)
		imp.Path.Value = `"` + updated + `"`
		changed = true

		// No explicit alias means the identifier is the package's declared name.
		// If the last path segment changed, the identifier changes too.
		if imp.Name == nil {
			oldName := path.Base(old)
			newName := path.Base(updated)
			if oldName != newName {
				renames = append(renames, rename{oldName, newName})
			}
		}
	}

	// Rename package identifiers in selector expressions (e.g. ateapipb.Foo → ateapi.Foo).
	for _, r := range renames {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if ident.Name == r.from {
				ident.Name = r.to
			}
			return true
		})
	}

	if !changed || m.dryRun {
		return nil
	}

	var buf bytes.Buffer
	if err := format.Node(&buf, fset, f); err != nil {
		return fmt.Errorf("formatting %s: %w", filePath, err)
	}
	return os.WriteFile(filePath, buf.Bytes(), 0o644)
}

// findAppliedMove finds the move rule that placed a file at the given new (to) location.
func (m *migrator) findAppliedMove(relPath string) (move, bool) {
	relPath = filepath.ToSlash(relPath)
	for _, mv := range m.moves {
		if relPath == mv.To || strings.HasPrefix(relPath, mv.To+"/") {
			return mv, true
		}
	}
	return move{}, false
}

func (m *migrator) rewriteImport(importPath string) string {
	prefix := m.module + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return importPath
	}
	rel := strings.TrimPrefix(importPath, prefix)
	for _, mv := range m.moves {
		if rel == mv.From || strings.HasPrefix(rel, mv.From+"/") {
			return prefix + mv.To + strings.TrimPrefix(rel, mv.From)
		}
	}
	return importPath
}

// rewriteProtoFiles rewrites import paths and go_package options in .proto files.
func (m *migrator) rewriteProtoFiles() error {
	return filepath.WalkDir(m.root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".proto") {
			return nil
		}
		return m.rewriteProtoFile(path)
	})
}

func (m *migrator) rewriteProtoFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	content := string(data)
	updated := content

	for _, mv := range m.moves {
		// Proto import paths: import "proto/ateapipb/foo.proto"
		updated = strings.ReplaceAll(updated, `"`+mv.From+`/`, `"`+mv.To+`/`)
		// go_package option: option go_package = "github.com/.../proto/ateapipb"
		updated = strings.ReplaceAll(updated, m.module+"/"+mv.From, m.module+"/"+mv.To)
	}

	if updated == content {
		return nil
	}

	rel, _ := filepath.Rel(m.root, path)
	fmt.Printf("proto %s\n", rel)

	if m.dryRun {
		return nil
	}
	return os.WriteFile(path, []byte(updated), 0o644)
}

// applyReplacements applies all text replacement rules to files matching their glob patterns.
func (m *migrator) applyReplacements() error {
	for _, r := range m.replacements {
		matches, err := m.glob(r.File)
		if err != nil {
			return fmt.Errorf("invalid glob %q: %w", r.File, err)
		}
		for _, absPath := range matches {
			if err := m.applyReplacement(absPath, r); err != nil {
				return err
			}
		}
	}
	return nil
}

// glob resolves a pattern relative to the repo root, supporting ** for recursive matching.
func (m *migrator) glob(pattern string) ([]string, error) {
	if !strings.Contains(pattern, "**") {
		return filepath.Glob(filepath.Join(m.root, filepath.FromSlash(pattern)))
	}

	// Split on the first **: walk the directory rooted at the prefix.
	parts := strings.SplitN(pattern, "**", 2)
	walkRoot := filepath.Join(m.root, filepath.FromSlash(strings.TrimSuffix(parts[0], "/")))
	suffix := strings.TrimPrefix(parts[1], "/")

	var matches []string
	err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		candidate := path
		if !strings.Contains(suffix, string(filepath.Separator)) {
			candidate = filepath.Base(path)
		} else {
			candidate, _ = filepath.Rel(walkRoot, path)
		}
		ok, matchErr := filepath.Match(filepath.FromSlash(suffix), candidate)
		if matchErr != nil {
			return matchErr
		}
		if ok {
			matches = append(matches, path)
		}
		return nil
	})
	return matches, err
}

func (m *migrator) applyReplacement(absPath string, r replacement) error {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}
	content := string(data)
	updated := strings.ReplaceAll(content, r.Old, r.New)
	if updated == content {
		return nil
	}
	rel, _ := filepath.Rel(m.root, absPath)
	fmt.Printf("replace %s\n    %q -> %q\n", rel, r.Old, r.New)
	if m.dryRun {
		return nil
	}
	return os.WriteFile(absPath, []byte(updated), 0o644)
}

func (m *migrator) removeEmptyDirs() error {
	for {
		removed := false
		_ = filepath.WalkDir(m.root, func(path string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() || path == m.root {
				return nil
			}
			if name := d.Name(); name == ".git" || name == "vendor" {
				return filepath.SkipDir
			}
			entries, _ := os.ReadDir(path)
			if len(entries) == 0 {
				rel, _ := filepath.Rel(m.root, path)
				fmt.Printf("rmdir %s\n", rel)
				os.Remove(path)
				removed = true
			}
			return nil
		})
		if !removed {
			break
		}
	}
	return nil
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func readModule(gomodPath string) (string, error) {
	data, err := os.ReadFile(gomodPath)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(after), nil
		}
	}
	return "", fmt.Errorf("no module directive found in %s", gomodPath)
}
