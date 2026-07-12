package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/config"
	"github.com/9level/pg-tui/internal/db"
)

const (
	formNoneKind = iota
	formCreateRole
	formGrant
	formForceDrop
)

type rolesView struct {
	cfg *config.Config
	mgr *db.Manager

	tbl     table.Model
	roles   []db.Role
	dbNames []string

	form     form
	confirm  confirmModal
	alert    alertModal
	formKind int

	pendingDropRole  string
	pendingForceRole string

	loading bool
	err     error
	status  string

	width, height int
}

func newRolesView(cfg *config.Config, mgr *db.Manager) *rolesView {
	return &rolesView{cfg: cfg, mgr: mgr, tbl: newTable(), confirm: newConfirmModal(), alert: newAlertModal()}
}

func (v *rolesView) Title() string { return "Roles" }

func (v *rolesView) CapturingInput() bool {
	return v.form.active || v.confirm.active || v.alert.active
}

func (v *rolesView) Init() tea.Cmd {
	v.loading = true
	v.err = nil
	return tea.Batch(loadRoles(v.mgr), loadDatabases(v.mgr))
}

func (v *rolesView) SetSize(w, h int) {
	v.width, v.height = w, h
	th := h - 2
	if th < 3 {
		th = 3
	}
	v.tbl.SetHeight(th)
	nameW := clampInt(w-58, 16, 40)
	v.tbl.SetColumns([]table.Column{
		{Title: "ROLE", Width: nameW},
		{Title: "LOGIN", Width: 6},
		{Title: "SUPER", Width: 6},
		{Title: "CREATEDB", Width: 9},
		{Title: "CREATEROLE", Width: 11},
		{Title: "MEMBER OF", Width: 22},
	})
}

func (v *rolesView) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case rolesMsg:
		v.loading = false
		v.err = msg.err
		if msg.err == nil {
			v.roles = msg.rows
			rows := make([]table.Row, 0, len(msg.rows))
			for _, r := range msg.rows {
				rows = append(rows, table.Row{
					r.Name, yesno(r.CanLogin), yesno(r.Super), yesno(r.CreateDB), yesno(r.CreateRole), r.MemberOf,
				})
			}
			v.tbl.SetRows(rows)
			if v.tbl.Cursor() >= len(rows) {
				v.tbl.SetCursor(0)
			}
		}
		return nil

	case databasesMsg:
		if msg.err == nil {
			v.dbNames = v.dbNames[:0]
			for _, d := range msg.rows {
				v.dbNames = append(v.dbNames, d.Name)
			}
		}
		return nil

	case execMsg:
		if msg.err != nil {
			v.status = stErr.Render("✗ " + msg.action)
			body := pgErrorText(msg.err)
			if strings.HasPrefix(msg.action, "drop role") {
				body += "\n\n" + dropRoleHint
			}
			v.alert.show(v.width, v.height, "Failed to "+msg.action, body, true)
			return nil
		}
		v.status = stGood.Render("✓ " + msg.action + " ok")
		return loadRoles(v.mgr)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

const dropRoleHint = "HOW TO FIX: the role owns objects or has privileges granted. " +
	"Reassign the objects (REASSIGN OWNED BY \"role\" TO \"other\") or remove them " +
	"(DROP OWNED BY \"role\") in EACH database where the role owns objects, and revoke " +
	"database privileges/ownership, before the DROP ROLE. Run these commands " +
	"from the Query tab (switching the target database with '/')."

func (v *rolesView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.alert.active {
		v.alert.update(msg)
		return nil
	}
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return execStatements(v.mgr, "", "drop role "+v.pendingDropRole, []string{db.BuildDropRole(v.pendingDropRole)})
		case confirmNo:
			v.status = stStatus.Render("cancelled")
		}
		return nil
	}
	if v.form.active {
		res, cmd := v.form.update(msg)
		switch res {
		case formSubmit:
			return v.submitForm()
		case formCancel:
			v.formKind = formNoneKind
		}
		return cmd
	}

	switch msg.String() {
	case "r":
		return v.Init()
	case "n":
		return v.openCreateForm()
	case "g":
		return v.openGrantForm()
	case "D":
		return v.askDropRole()
	case "F":
		return v.openForceDropForm()
	}
	var cmd tea.Cmd
	v.tbl, cmd = v.tbl.Update(msg)
	return cmd
}

func (v *rolesView) selectedRole() (db.Role, bool) {
	i := v.tbl.Cursor()
	if i < 0 || i >= len(v.roles) {
		return db.Role{}, false
	}
	return v.roles[i], true
}

func (v *rolesView) openCreateForm() tea.Cmd {
	v.formKind = formCreateRole
	return v.form.open("Create role/user", []formField{
		textField("name", "Name", "e.g. app_user"),
		secretField("password", "Password"),
		selectField("login", "Can login", []string{"yes", "no"}),
		selectField("createdb", "CREATEDB", []string{"no", "yes"}),
		selectField("createrole", "CREATEROLE", []string{"no", "yes"}),
	})
}

func (v *rolesView) openGrantForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.status = stWarnV.Render("select a role first")
		return nil
	}
	if len(v.dbNames) == 0 {
		v.status = stWarnV.Render("database list not loaded yet")
		return nil
	}
	v.formKind = formGrant
	return v.form.open("Grant · to "+role.Name, []formField{
		selectField("database", "Database", v.dbNames),
		selectField("scope", "Privilege", []string{
			db.GrantConnect.Label(),
			db.GrantAllDatabase.Label(),
			db.GrantSchemaAll.Label(),
			db.GrantOwner.Label(),
		}),
	})
}

func (v *rolesView) openForceDropForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		return nil
	}
	successor := "postgres"
	if v.cfg != nil && v.cfg.User != "" {
		successor = v.cfg.User
	}
	v.pendingForceRole = role.Name
	v.formKind = formForceDrop
	succ := textField("successor", "Reassign to", successor)
	succ.input.SetValue(successor)
	return v.form.open("⚠ Force-drop "+role.Name+" (reassigns ownership, keeps data)", []formField{
		succ,
		textField("confirm", "Confirm", "type the role name"),
	})
}

func (v *rolesView) askDropRole() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		return nil
	}
	v.pendingDropRole = role.Name
	body := fmt.Sprintf("This will remove role %s from the cluster.\nType the name to confirm:",
		stBadV.Render(role.Name))
	return v.confirm.askCritical("⚠  DROP ROLE", body, role.Name)
}

func (v *rolesView) submitForm() tea.Cmd {
	switch v.formKind {
	case formCreateRole:
		name := strings.TrimSpace(v.form.value("name"))
		if name == "" {
			v.status = stWarnV.Render("name is required")
			return nil
		}
		_, login := v.form.selected("login")
		_, createdb := v.form.selected("createdb")
		_, createrole := v.form.selected("createrole")
		sql := db.BuildCreateRole(name, v.form.value("password"), login == "yes", createdb == "yes", createrole == "yes")
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, "", "create role "+name, []string{sql})

	case formGrant:
		role, ok := v.selectedRole()
		if !ok {
			v.form.close()
			v.formKind = formNoneKind
			return nil
		}
		_, database := v.form.selected("database")
		scopeIdx, _ := v.form.selected("scope")
		scope := []db.GrantScope{db.GrantConnect, db.GrantAllDatabase, db.GrantSchemaAll, db.GrantOwner}[scopeIdx]
		targetDB, stmts := db.BuildGrant(scope, database, role.Name)
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, targetDB, fmt.Sprintf("grant %s → %s", role.Name, database), stmts)

	case formForceDrop:
		successor := strings.TrimSpace(v.form.value("successor"))
		confirm := strings.TrimSpace(v.form.value("confirm"))
		if confirm != v.pendingForceRole {
			v.status = stWarnV.Render("wrong confirmation — type the exact role name")
			return nil
		}
		if successor == "" {
			v.status = stWarnV.Render("enter the successor role")
			return nil
		}
		if successor == v.pendingForceRole {
			v.status = stWarnV.Render("successor cannot be the role itself")
			return nil
		}
		doomed := v.pendingForceRole
		v.form.close()
		v.formKind = formNoneKind
		v.status = stWarnV.Render("reassigning ownership and removing " + doomed + "…")
		return forceDropRole(v.mgr, doomed, successor)
	}
	return nil
}

func (v *rolesView) FooterHints() string {
	return hint("n", "create") + "  " + hint("g", "grant") + "  " + hint("D", "drop") + "  " +
		hint("F", "force-drop") + "  " + hint("r", "refresh") + "  " + hint("↑↓", "navigate")
}

func (v *rolesView) View() string {
	if v.alert.active {
		return v.alert.view(v.width, v.height)
	}
	if v.form.active {
		return v.form.view(v.width, v.height)
	}
	if v.confirm.active {
		return v.confirm.view(v.width, v.height)
	}
	if v.err != nil {
		return "\n" + stErr.Render("Error: "+v.err.Error())
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Cluster roles")
	meta := stLabel.Render(fmt.Sprintf("  %d roles", len(v.roles)))
	if v.loading {
		meta = stLabel.Render("  loading…")
	}
	head := "\n" + title + meta
	if v.status != "" {
		head += "   " + v.status
	}
	return head + "\n" + v.tbl.View()
}

func yesno(b bool) string {
	if b {
		return "yes"
	}
	return "·"
}
