// Copyright (c) 2026 TangXiangjiang.
// SPDX-License-Identifier: MIT

package editorhost

import (
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/anvil-dev/anvil"
)

// Caps on what a single prompt may carry into the model's context.
//
// These exist because the client is not a trusted size estimator: a mention
// picker that resolves a glob to a directory, or a paste of a minified bundle,
// would otherwise put megabytes into a request the user never sees. Every cap
// that drops something says so in the composed text — the model should know the
// material is partial, and so should the user reading the transcript.
const (
	maxMentions       = 32
	maxQuotes         = 16
	maxMentionBytes   = 64 << 10
	maxContextBytes   = 256 << 10
	maxListingEntries = 200
	maxDiagnostics    = 100
)

// composePromptContext folds the editor's `context` object into one text block.
//
// It is text and not structure because the public SDK has no multi-part text
// message: Agent.Send takes a string. Folding happens here, next to plan mode's
// directive, so a future structured-content API has one place to replace.
func (s *Server) composePromptContext(c *PromptContext) (string, *RPCError) {
	if c == nil {
		return "", nil
	}
	var blocks []string
	// One budget for the whole block, spent in render order: mentions first,
	// because they are what the user explicitly pointed at.
	budget := maxContextBytes

	if block, rpcErr := s.mentionBlocks(s.cwdPath(), c.Mentions, &budget); rpcErr != nil {
		return "", rpcErr
	} else if block != "" {
		blocks = append(blocks, block)
	}
	if block := quoteBlock(c.Quotes, &budget); block != "" {
		blocks = append(blocks, block)
	}
	if block := selectionBlock(c.Selection, &budget); block != "" {
		blocks = append(blocks, block)
	}
	if block := diagnosticsBlock(c.Diagnostics); block != "" {
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return "", nil
	}
	return "<context>\n" + strings.Join(blocks, "") + "</context>", nil
}

// quoteBlock renders messages the user explicitly pointed back to. Quotes share
// the context budget and are labelled by speaker so copied prose cannot lose
// its place in the conversation.
func quoteBlock(quotes []Quote, budget *int) string {
	if len(quotes) == 0 || *budget <= 0 {
		return ""
	}
	kept := quotes
	omitted := 0
	if len(kept) > maxQuotes {
		omitted = len(kept) - maxQuotes
		kept = kept[:maxQuotes]
	}

	var b strings.Builder
	b.WriteString("<quoted_messages>\n")
	for _, quote := range kept {
		if *budget <= 0 {
			omitted++
			continue
		}
		role := "assistant"
		if quote.Role == "user" {
			role = "user"
		}
		text, cut := clip(quote.Text, min(maxMentionBytes, *budget))
		*budget -= len(text)
		fmt.Fprintf(&b, "<quote role=%q>\n", role)
		b.WriteString(text)
		if text != "" && !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
		if cut {
			b.WriteString("(quote truncated)\n")
		}
		b.WriteString("</quote>\n")
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d further quoted messages were left out)\n", omitted)
	}
	b.WriteString("</quoted_messages>\n")
	return b.String()
}

// mentionBlocks renders the referenced paths. It reports how many were left out
// so the caller can say so rather than silently dropping them.
func (s *Server) mentionBlocks(root string, mentions []Mention, budget *int) (string, *RPCError) {
	if len(mentions) == 0 {
		return "", nil
	}
	kept := mentions
	omitted := 0
	if len(kept) > maxMentions {
		omitted = len(kept) - maxMentions
		kept = kept[:maxMentions]
	}

	var b strings.Builder
	b.WriteString("<mentions>\n")
	for i, m := range kept {
		if *budget <= 0 {
			omitted += len(kept) - i
			break
		}
		block, err := s.mentionBlock(root, m.Path, *budget)
		if err != nil {
			// Not a skip: an unreadable mention means the model would answer
			// about a file it never saw, which is worse than a failed prompt.
			return "", errInvalidParams("context.mentions: " + err.Error())
		}
		*budget -= len(block)
		b.WriteString(block)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d further referenced paths were left out to stay within the context budget)\n", omitted)
	}
	b.WriteString("</mentions>\n")
	return b.String(), nil
}

// mentionBlock renders one mentioned path: a file's body, or a directory's
// listing. Either is clipped to the remaining budget.
func (s *Server) mentionBlock(root, path string, budget int) (string, error) {
	abs, err := resolveWorkspacePath(root, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	if info.IsDir() {
		listing, truncated, err := workspaceListing(abs, maxListingEntries)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "--- %s (directory listing)\n", strings.TrimSpace(path))
		body, cut := clip(listing, budget)
		b.WriteString(body)
		if truncated {
			fmt.Fprintf(&b, "(first %d entries only)\n", maxListingEntries)
		} else if cut {
			b.WriteString("(listing truncated)\n")
		}
		b.WriteString("\n")
		return b.String(), nil
	}

	content, truncated, err := readCapped(abs, min(maxMentionBytes, budget))
	if err != nil {
		return "", err
	}
	if truncated {
		fmt.Fprintf(&b, "--- %s (first %d of %d bytes)\n", strings.TrimSpace(path), len(content), info.Size())
	} else {
		fmt.Fprintf(&b, "--- %s\n", strings.TrimSpace(path))
	}
	b.WriteString(content)
	if content != "" && !strings.HasSuffix(content, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String(), nil
}

// selectionBlock renders the editor's active selection.
//
// The path is not resolved against the workspace and no file is read: the text
// is already in hand, and an editor legitimately selects from files outside the
// workspace root (a settings file, a dependency). Containment matters only for
// what the host reads, which is the mentions above.
func selectionBlock(sel *Selection, budget *int) string {
	if sel == nil || strings.TrimSpace(sel.Text) == "" {
		return ""
	}
	text, cut := clip(sel.Text, min(maxMentionBytes, *budget))
	*budget -= len(text)

	var b strings.Builder
	fmt.Fprintf(&b, "<selection path=%q lines=%q>\n", sel.Path, rangeLabel(sel.Range))
	b.WriteString(text)
	if text != "" && !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	if cut {
		b.WriteString("(selection truncated)\n")
	}
	b.WriteString("</selection>\n")
	return b.String()
}

// rangeLabel renders an editor range the way the editor shows it, so the model
// and the user read the same numbers.
func rangeLabel(r Range) string {
	if r.EndLine == 0 && r.StartLine == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d:%d-%d:%d", r.StartLine, r.StartColumn, r.EndLine, r.EndColumn)
}

// diagnosticsBlock renders the client's diagnostics verbatim. The host neither
// computes them nor judges them: the editor already has them from its language
// server, and re-deriving anything here would risk disagreeing with what the
// user sees in the Problems panel.
func diagnosticsBlock(diags []Diagnostic) string {
	if len(diags) == 0 {
		return ""
	}
	kept := diags
	omitted := 0
	if len(kept) > maxDiagnostics {
		omitted = len(kept) - maxDiagnostics
		kept = kept[:maxDiagnostics]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<diagnostics count=%q>\n", strconv.Itoa(len(kept)))
	for _, d := range kept {
		severity := d.Severity
		if severity == "" {
			severity = "error"
		}
		// One diagnostic per line, so a multi-line message cannot be mistaken
		// for a second entry.
		message := strings.Join(strings.Fields(d.Message), " ")
		fmt.Fprintf(&b, "%s:%d %s: %s\n", d.Path, d.Line, severity, message)
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "(%d more not shown)\n", omitted)
	}
	b.WriteString("</diagnostics>\n")
	return b.String()
}

// ---------------------------------------------------------------------------
// attachments
// ---------------------------------------------------------------------------

// attachmentsFor converts client attachments into SDK ones.
//
// In-memory data is decoded here rather than written to a temporary file, so a
// pasted screenshot never touches the disk. A path is passed through unaltered:
// the SDK resolves and loads it under its own sandbox, which is the same party
// that reads it, so a second containment check here would only risk disagreeing
// with the one that actually governs the read.
func attachmentsFor(in []Attachment) ([]anvil.ImageAttachment, *RPCError) {
	out := make([]anvil.ImageAttachment, 0, len(in))
	for i, a := range in {
		detail := strings.ToLower(strings.TrimSpace(a.Detail))
		switch {
		case strings.TrimSpace(a.Data) != "":
			raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(a.Data))
			if err != nil {
				return nil, errInvalidParams(fmt.Sprintf("prompt.attachments[%d]: data is not valid base64", i))
			}
			if len(raw) == 0 {
				return nil, errInvalidParams(fmt.Sprintf("prompt.attachments[%d]: data decodes to no bytes", i))
			}
			out = append(out, anvil.ImageAttachment{Bytes: raw, MIME: a.MIME, Detail: detail})
		case strings.TrimSpace(a.Path) != "":
			out = append(out, anvil.ImageAttachment{Path: strings.TrimSpace(a.Path), MIME: a.MIME, Detail: detail})
		default:
			return nil, errInvalidParams(fmt.Sprintf("prompt.attachments[%d]: path or data is required", i))
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// readCapped reads at most limit bytes and reports whether the file had more.
// A partial trailing rune is dropped: a mangled last character is a strange
// thing to show a model.
func readCapped(path string, limit int) (string, bool, error) {
	if limit <= 0 {
		return "", true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, int64(limit)))
	if err != nil {
		return "", false, err
	}
	var probe [1]byte
	n, err := f.Read(probe[:])
	if err != nil && err != io.EOF {
		return "", false, err
	}
	return strings.ToValidUTF8(string(data), ""), n > 0, nil
}

// clip cuts text to at most limit bytes on a rune boundary, reporting whether
// anything was dropped.
func clip(text string, limit int) (string, bool) {
	if limit <= 0 {
		return "", text != ""
	}
	if len(text) <= limit {
		return text, false
	}
	return strings.ToValidUTF8(text[:limit], ""), true
}
