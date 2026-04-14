package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/synapbus/synapbus/internal/goaltasks"
)

// geminiTaskTree calls the `gemini` CLI with the goal brief and asks it
// to emit a JSON task tree. If GEMINI is not configured (env var
// SYNAPBUS_GEMINI_MODEL unset) or the call fails for any reason, it
// returns a nil tree and a non-nil error — callers fall back to the
// fixed buildTaskTree() template.
//
// Why shell out rather than use the Gemini SDK directly?
//  1. Zero extra dependencies keeps the docgardener binary lean.
//  2. Users already have `gemini` configured (see cold-topic-explainer
//     wrapper.sh) and the CLI's auth is persistent.
//  3. The prompt is small enough that subprocess overhead is negligible.
func geminiTaskTree(ctx context.Context, logger *slog.Logger, goalBrief string) (*goaltasks.TreeNode, error) {
	model := os.Getenv("SYNAPBUS_GEMINI_MODEL")
	if model == "" {
		return nil, errors.New("SYNAPBUS_GEMINI_MODEL not set")
	}
	if _, err := exec.LookPath("gemini"); err != nil {
		return nil, fmt.Errorf("gemini CLI not found on PATH: %w", err)
	}

	prompt := geminiPrompt(goalBrief)
	_ = os.WriteFile("gemini_prompt.txt", []byte(prompt), 0o644)

	runCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "gemini",
		"-m", model,
		"--approval-mode", "yolo",
		"-p", prompt,
	)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("gemini exit %d: %s", ee.ExitCode(), string(ee.Stderr))
		}
		return nil, fmt.Errorf("gemini exec: %w", err)
	}

	raw := string(out)
	_ = os.WriteFile("gemini_response.txt", []byte(raw), 0o644)

	// Gemini sometimes prepends diagnostic lines ("MCP issues detected…")
	// and often wraps JSON in a ```json fenced block. Extract the first
	// balanced {...} object.
	jsonBlob, err := extractJSONObject(raw)
	if err != nil {
		return nil, fmt.Errorf("gemini response had no JSON object: %w (raw=%q)", err, truncate(raw, 500))
	}

	var tree goaltasks.TreeNode
	if err := json.Unmarshal([]byte(jsonBlob), &tree); err != nil {
		return nil, fmt.Errorf("parse gemini JSON: %w (raw=%q)", err, truncate(jsonBlob, 500))
	}
	if tree.Title == "" || len(tree.Children) == 0 {
		return nil, fmt.Errorf("gemini returned an empty or rootless tree")
	}

	// Force the leaves to use the billing codes docgardener's dispatcher
	// matches on; if Gemini invents its own leaves we keep the coordinator
	// happy by aligning them with the 3 role slots.
	alignLeafBillingCodes(&tree)

	logger.Info("gemini task tree", "leaves", countLeaves(&tree), "bytes", len(jsonBlob))
	return &tree, nil
}

func geminiPrompt(brief string) string {
	return `You are the doc-gardener coordinator. A human has given you this brief:

"""
` + brief + `
"""

Decompose this into a task tree with exactly 3 leaf tasks for these 3 specialists:

  1. docs-scanner   — scans the documentation website and extracts every CLI flag
                      and config option mentioned; emits #finding messages.
  2. cli-verifier   — runs the target binary and reacts to each #finding with
                      #verified or #missing.
  3. drift-reporter — aggregates the verifier reactions and produces a final
                      drift report with counts of matches, drifts, and patches.

Return ONLY a JSON object with this exact schema (no prose, no markdown fences):

{
  "title": "<root task title>",
  "description": "<root task description>",
  "acceptance_criteria": "<root acceptance criteria>",
  "billing_code": "doc-gardener",
  "children": [
    {
      "title": "Scan docs for CLI flags and config keys",
      "description": "<specifics>",
      "acceptance_criteria": "<specifics>",
      "billing_code": "doc-gardener/scan"
    },
    {
      "title": "Verify flags exist in mcpproxy binary",
      "description": "<specifics>",
      "acceptance_criteria": "<specifics>",
      "billing_code": "doc-gardener/verify"
    },
    {
      "title": "Produce drift report",
      "description": "<specifics>",
      "acceptance_criteria": "<specifics>",
      "billing_code": "doc-gardener/report"
    }
  ]
}

The billing_code values MUST be exactly "doc-gardener", "doc-gardener/scan",
"doc-gardener/verify", "doc-gardener/report" — the coordinator routes tasks to
specialists by matching on those strings. Emit ONLY the JSON object.`
}

// extractJSONObject finds the first balanced {...} object in raw, ignoring
// markdown fences and preamble.
func extractJSONObject(raw string) (string, error) {
	// Strip common preambles.
	raw = strings.TrimPrefix(raw, "MCP issues detected. Run /mcp list for status.")
	start := strings.IndexByte(raw, '{')
	if start < 0 {
		return "", errors.New("no '{' in output")
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(raw); i++ {
		ch := raw[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inStr {
			escape = true
			continue
		}
		if ch == '"' {
			inStr = !inStr
			continue
		}
		if inStr {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return raw[start : i+1], nil
			}
		}
	}
	return "", errors.New("unbalanced braces")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func countLeaves(n *goaltasks.TreeNode) int {
	if len(n.Children) == 0 {
		return 1
	}
	total := 0
	for i := range n.Children {
		total += countLeaves(&n.Children[i])
	}
	return total
}

// alignLeafBillingCodes ensures exactly the 3 expected billing codes are set
// on the leaves so the coordinator's dispatch-by-billing-code logic still
// works even when Gemini drifts slightly. Leaves are aligned in order.
func alignLeafBillingCodes(root *goaltasks.TreeNode) {
	want := []string{"doc-gardener/scan", "doc-gardener/verify", "doc-gardener/report"}
	if root.BillingCode == "" {
		root.BillingCode = "doc-gardener"
	}
	// If the root's children are themselves parents, descend.
	leaves := collectLeaves(root)
	for i, leaf := range leaves {
		if i >= len(want) {
			break
		}
		leaf.BillingCode = want[i]
		if leaf.VerifierConfig == nil {
			leaf.VerifierConfig = &goaltasks.VerifierConfig{Kind: goaltasks.VerifierKindAuto}
		}
	}
}

func collectLeaves(n *goaltasks.TreeNode) []*goaltasks.TreeNode {
	if len(n.Children) == 0 {
		return []*goaltasks.TreeNode{n}
	}
	var out []*goaltasks.TreeNode
	for i := range n.Children {
		out = append(out, collectLeaves(&n.Children[i])...)
	}
	return out
}
