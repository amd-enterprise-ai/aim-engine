// MIT License
//
// Copyright (c) 2025 Advanced Micro Devices, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

const (
	inputPath  = "config/helm/values.yaml"
	outputPath = "docs/docs/reference/helm-values.md"
)

type entry struct {
	key         string
	description string
	defaultVal  string
}

type section struct {
	name        string
	description string
	entries     []entry
}

func main() {
	f, err := os.Open(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening %s: %v\n", inputPath, err)
		os.Exit(1)
	}
	defer func() { _ = f.Close() }()

	var sections []section
	var currentSection *section
	var descLines []string
	var overrideDefault string
	var keyPath []string
	var indentStack []int

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Blank line resets description only if we haven't started a doc comment block
		if trimmed == "" {
			descLines = nil
			overrideDefault = ""
			continue
		}

		// Comment line
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimPrefix(trimmed, "#")
			text = strings.TrimSpace(text)

			// Doc comment: "# -- description"
			if strings.HasPrefix(text, "-- ") {
				desc := strings.TrimPrefix(text, "-- ")
				descLines = append(descLines, desc)
				overrideDefault = ""
				continue
			}

			// @default override: "# @default -- `value`"
			if strings.HasPrefix(text, "@default") {
				parts := strings.SplitN(text, "--", 2)
				if len(parts) == 2 {
					overrideDefault = strings.TrimSpace(parts[1])
				}
				continue
			}

			// Continuation of a doc comment (no "-- " prefix, but follows one)
			// Only treat as continuation if the previous line was a doc comment
			// and this doesn't look like commented-out YAML
			if len(descLines) > 0 && !looksLikeYAML(text) {
				descLines = append(descLines, text)
				continue
			}

			// Anything else (structural comment, commented-out YAML) — skip
			continue
		}

		// Non-comment line: parse YAML
		indent := countIndent(line)

		// Must contain a colon to be a key
		if !strings.Contains(trimmed, ":") {
			descLines = nil
			overrideDefault = ""
			continue
		}

		// Skip array items (- key: value)
		if strings.HasPrefix(trimmed, "-") {
			descLines = nil
			overrideDefault = ""
			continue
		}

		parts := strings.SplitN(trimmed, ":", 2)
		key := strings.TrimSpace(parts[0])
		value := ""
		if len(parts) > 1 {
			value = strings.TrimSpace(parts[1])
		}

		// Strip inline comments from value
		if idx := strings.Index(value, " #"); idx >= 0 {
			value = strings.TrimSpace(value[:idx])
		}

		// Update key path based on indentation
		for len(indentStack) > 0 && indent <= indentStack[len(indentStack)-1] {
			indentStack = indentStack[:len(indentStack)-1]
			if len(keyPath) > 0 {
				keyPath = keyPath[:len(keyPath)-1]
			}
		}

		// Top-level key = new section
		if indent == 0 {
			desc := strings.Join(descLines, " ")
			descLines = nil
			overrideDefault = ""

			s := section{name: key, description: desc}
			sections = append(sections, s)
			currentSection = &sections[len(sections)-1]

			keyPath = []string{key}
			indentStack = []int{indent}
			continue
		}

		// Nested key
		indentStack = append(indentStack, indent)
		keyPath = append(keyPath, key)

		fullKey := strings.Join(keyPath, ".")

		// Only add entries that have a doc comment (# -- ...)
		if len(descLines) > 0 && currentSection != nil {
			desc := strings.Join(descLines, " ")
			def := overrideDefault
			if def == "" {
				def = formatDefault(value)
			}
			currentSection.entries = append(currentSection.entries, entry{
				key:         fullKey,
				description: desc,
				defaultVal:  def,
			})
		}

		descLines = nil
		overrideDefault = ""
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", inputPath, err)
		os.Exit(1)
	}

	// Generate markdown
	var out strings.Builder
	out.WriteString("# Helm Chart Values\n\n")
	out.WriteString("Reference for all configurable values in the AIM Engine Helm chart.\n\n")
	out.WriteString(fmt.Sprintf("<!-- Auto-generated from %s by hack/generate-helm-values-docs.go. Do not edit manually. -->\n\n", inputPath))

	for _, sec := range sections {
		out.WriteString(fmt.Sprintf("## %s\n\n", formatSectionName(sec.name)))
		if sec.description != "" {
			out.WriteString(fmt.Sprintf("%s\n\n", sec.description))
		}

		if len(sec.entries) == 0 {
			continue
		}

		out.WriteString("| Parameter | Description | Default |\n")
		out.WriteString("|-----------|-------------|----------|\n")
		for _, e := range sec.entries {
			out.WriteString(fmt.Sprintf("| `%s` | %s | %s |\n", e.key, e.description, e.defaultVal))
		}
		out.WriteString("\n")
	}

	if err := os.WriteFile(outputPath, []byte(out.String()), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error writing %s: %v\n", outputPath, err)
		os.Exit(1)
	}

	fmt.Printf("Generated %s from %s\n", outputPath, inputPath)
}

func countIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// looksLikeYAML returns true if the text looks like a commented-out YAML line
// (e.g., "storage:", "- name: foo", "key: value")
func looksLikeYAML(text string) bool {
	if strings.HasPrefix(text, "-") {
		return true
	}
	if strings.Contains(text, ":") && !strings.Contains(text, "://") {
		return true
	}
	return false
}

func formatDefault(value string) string {
	if value == "" {
		return "" // no scalar value, skip
	}
	if value == "[]" {
		return "`[]`"
	}
	if value == "true" || value == "false" {
		return "`" + value + "`"
	}
	value = strings.Trim(value, `"'`)
	return "`" + value + "`"
}

var sectionNames = map[string]string{
	"manager":              "Controller Manager",
	"rbacHelpers":          "RBAC Helpers",
	"crd":                  "CRDs",
	"metrics":              "Metrics",
	"certManager":          "Cert-Manager",
	"prometheus":           "Prometheus",
	"clusterRuntimeConfig": "Cluster Runtime Configuration",
	"clusterModelSource":   "Cluster Model Source",
}

func formatSectionName(key string) string {
	if name, ok := sectionNames[key]; ok {
		return name
	}
	return key
}
