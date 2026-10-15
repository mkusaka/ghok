package native

import (
	"strings"

	"github.com/mkusaka/ghok/internal/model"
)

// extractTexts uses the parsed flag set, including explicitly empty values.
// Generated or interactively edited content is not available before gh runs.
func extractTexts(op *model.Operation, c commandSpec, p parsedArgs, inv model.Invocation) {
	var title, body, notes *model.Text
	switch c.path {
	case "pr create", "issue create":
		if p.enabled("web") || p.enabled("editor") {
			title = unavailableText(op, "title", "editor", "", "title will be edited interactively")
			body = unavailableText(op, "body", "editor", "", "body will be edited interactively")
		} else {
			fill := p.enabled("fill") || p.enabled("fill-first") || p.enabled("fill-verbose")
			title = inlineText(p, "title")
			body = flagText(op, p, inv, "body", "body-file", "body")
			if title == nil {
				source := "editor"
				if fill {
					source = "fill"
				}
				title = unavailableText(op, "title", source, "", "title will be generated or entered when gh runs")
			}
			if body == nil {
				source, path := "editor", ""
				if fill {
					source = "fill"
				} else if p.present("template") {
					source, path = "template", p.value("template")
				}
				body = unavailableText(op, "body", source, path, "final body will be generated or entered when gh runs")
			}
		}
	case "pr edit", "issue edit":
		title = inlineText(p, "title")
		body = flagText(op, p, inv, "body", "body-file", "body")
	case "pr comment", "issue comment":
		if p.enabled("delete-last") {
			return
		}
		if p.enabled("web") || p.enabled("editor") {
			body = unavailableText(op, "body", "editor", "", "comment will be edited interactively")
		} else {
			body = flagText(op, p, inv, "body", "body-file", "body")
			if body == nil {
				body = unavailableText(op, "body", "editor", "", "comment will be entered interactively")
			}
		}
	case "pr review":
		body = flagText(op, p, inv, "body", "body-file", "body")
		// Approval without a body does not open an editor; other review modes do.
		if body == nil && !p.enabled("approve") {
			body = unavailableText(op, "body", "editor", "", "review body will be entered interactively")
		}
	case "pr merge":
		title = inlineText(p, "subject")
		body = flagText(op, p, inv, "body", "body-file", "body")
	case "pr close", "pr reopen", "issue close", "issue reopen":
		body = inlineText(p, "comment")
	case "release create", "release edit":
		title = inlineText(p, "title")
		if c.path == "release create" && (p.enabled("generate-notes") || p.enabled("notes-from-tag")) {
			// Generated notes can be appended to explicit notes, so those bytes
			// alone are not the final release text.
			notes = unavailableText(op, "notes", "fill", "", "release notes will be generated when gh runs")
		} else {
			notes = flagText(op, p, inv, "notes", "notes-file", "notes")
		}
	}
	if title != nil || body != nil || notes != nil {
		op.Texts = &model.Texts{Title: title, Body: body, Notes: notes}
	}
}

func inlineText(p parsedArgs, flag string) *model.Text {
	if !p.present(flag) {
		return nil
	}
	value := p.value(flag)
	return &model.Text{Value: value, Source: "inline", Bytes: int64(len(value))}
}

func unavailableText(op *model.Operation, field, source, path, reason string) *model.Text {
	unresolved(op, "texts."+field, reason, source)
	return &model.Text{Source: source, Path: path}
}

func flagText(op *model.Operation, p parsedArgs, inv model.Invocation, inlineFlag, fileFlag, field string) *model.Text {
	if text := inlineText(p, inlineFlag); text != nil {
		return text
	}
	if !p.present(fileFlag) {
		return nil
	}
	path := p.value(fileFlag)
	if path == "-" {
		if inv.Stdin.Status == model.StdinCaptured {
			return &model.Text{Value: string(inv.Stdin.Data), Source: "stdin", Bytes: int64(len(inv.Stdin.Data))}
		}
		status := inv.Stdin.Status
		if status == "" {
			status = model.StdinNotCaptured
		}
		text := unavailableText(op, field, "stdin", "", "stdin text unavailable: "+string(status))
		text.Status = status
		text.Truncated = status == model.StdinTooLarge
		return text
	}
	if inv.ReadFile == nil {
		return unavailableText(op, field, "file", path, "text file reader unavailable")
	}
	data, err := inv.ReadFile(path)
	if err != nil {
		text := unavailableText(op, field, "file", path, "text file unavailable: "+err.Error())
		reason := strings.ToLower(err.Error())
		text.Truncated = strings.Contains(reason, "too large") || strings.Contains(reason, "too-large") || strings.Contains(reason, "size limit") || strings.Contains(reason, "exceeds")
		return text
	}
	return &model.Text{Value: string(data), Source: "file", Path: path, Bytes: int64(len(data))}
}
