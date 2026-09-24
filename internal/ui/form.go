package ui

import (
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldSecret
	fieldSelect
)

type formField struct {
	key   string
	label string
	kind  fieldKind
	input textinput.Model
	opts  []string
	sel   int
}

func textField(key, label, placeholder string) formField {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.CharLimit = 200
	ti.Width = 40
	return formField{key: key, label: label, kind: fieldText, input: ti}
}

func secretField(key, label string) formField {
	f := textField(key, label, "")
	f.kind = fieldSecret
	f.input.EchoMode = textinput.EchoPassword
	f.input.EchoCharacter = '•'
	return f
}

func selectField(key, label string, opts []string) formField {
	return formField{key: key, label: label, kind: fieldSelect, opts: opts}
}

type formResult int

const (
	formNone formResult = iota
	formSubmit
	formCancel
)

// form is a simple modal form (text/password/select fields).
type form struct {
	active bool
	title  string
	fields []formField
	focus  int
	err    string // validation message shown under the fields
}

func (f *form) open(title string, fields []formField) tea.Cmd {
	f.active = true
	f.title = title
	f.fields = fields
	f.focus = 0
	f.err = ""
	return f.refocus()
}

func (f *form) close() {
	f.active = false
	for i := range f.fields {
		f.fields[i].input.Blur()
	}
	f.fields = nil
}

// refocus focuses the current text field (selects don't receive cursor focus).
func (f *form) refocus() tea.Cmd {
	var cmd tea.Cmd
	for i := range f.fields {
		if i == f.focus && f.fields[i].kind != fieldSelect {
			cmd = f.fields[i].input.Focus()
		} else {
			f.fields[i].input.Blur()
		}
	}
	return cmd
}

func (f *form) moveFocus(delta int) tea.Cmd {
	n := len(f.fields)
	f.focus = (f.focus + delta + n) % n
	return f.refocus()
}

func (f *form) update(msg tea.KeyMsg) (formResult, tea.Cmd) {
	if !f.active {
		return formNone, nil
	}
	cur := &f.fields[f.focus]
	switch msg.String() {
	case "esc":
		f.close()
		return formCancel, nil
	case "enter":
		return formSubmit, nil
	case "tab", "down":
		return formNone, f.moveFocus(1)
	case "shift+tab", "up":
		return formNone, f.moveFocus(-1)
	case "left":
		if cur.kind == fieldSelect && len(cur.opts) > 0 {
			cur.sel = (cur.sel - 1 + len(cur.opts)) % len(cur.opts)
		}
		return formNone, nil
	case "right", " ":
		if cur.kind == fieldSelect && len(cur.opts) > 0 {
			cur.sel = (cur.sel + 1) % len(cur.opts)
			return formNone, nil
		}
	}
	if cur.kind != fieldSelect {
		var cmd tea.Cmd
		cur.input, cmd = cur.input.Update(msg)
		return formNone, cmd
	}
	return formNone, nil
}

func (f *form) value(key string) string {
	for i := range f.fields {
		if f.fields[i].key == key {
			return f.fields[i].input.Value()
		}
	}
	return ""
}

func (f *form) selected(key string) (int, string) {
	for i := range f.fields {
		if f.fields[i].key == key && len(f.fields[i].opts) > 0 {
			return f.fields[i].sel, f.fields[i].opts[f.fields[i].sel]
		}
	}
	return 0, ""
}

func (f *form) view(w, h int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(colOnDark).Background(colAccent).
		Padding(0, 1).Render(" " + f.title + " ")

	var b []string
	for i := range f.fields {
		fld := &f.fields[i]
		label := stLabel.Render(pad(fld.label, 14))
		if i == f.focus {
			label = stKey.Render(pad("› "+fld.label, 14))
		}
		var val string
		if fld.kind == fieldSelect {
			opt := ""
			if len(fld.opts) > 0 {
				opt = fld.opts[fld.sel]
			}
			marker := stKeyHint.Render("‹ ") + stValue.Render(opt) + stKeyHint.Render(" ›")
			val = marker
		} else {
			val = fld.input.View()
		}
		b = append(b, label+"  "+val)
	}

	hints := stKeyHint.Render("tab/↑↓ fields · ←→ options · enter confirm · esc cancel")
	content := title + "\n\n" + joinLines(b) + "\n\n" + hints
	if f.err != "" {
		content += "\n\n" + stErr.Render(f.err)
	}
	box := stModal.BorderForeground(colAccent).Width(clampInt(w-8, 40, 74)).Render(content)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, box)
}

func joinLines(ls []string) string {
	out := ""
	for i, l := range ls {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
