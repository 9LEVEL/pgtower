package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pgtui/internal/config"
	"github.com/9level/pgtui/internal/db"
)

const (
	formNoneKind = iota
	formCreateRole
	formGrant
	formForceDrop
	formConnLimit
	formAlterAttrs
	formRevoke
)

// confirm kinds distinguish which pending action the shared confirm modal is
// asking about (they run different statements on "yes").
const (
	confirmNoneKind = iota
	confirmDropRole
	confirmResetPwd
	confirmEscalate
)

// role actions offered by the "Enter → manage" menu (indexes into roleMenuItems).
const (
	menuResetPassword = iota
	menuSetConnLimit
	menuEditAttrs
)

var roleMenuItems = []string{
	"Reset password (generate random)",
	"Set connection limit",
	"Edit attributes (LOGIN, SUPERUSER, BYPASSRLS…)",
}

// newPasswordLen is the length of a generated password (letters + digits).
const newPasswordLen = 32

type rolesView struct {
	cfg *config.Config
	mgr *db.Manager

	tbl     table.Model
	roles   []db.Role
	dbNames []string

	form     form
	confirm  confirmModal
	alert    alertModal
	menu     actionMenu
	finder   finder
	formKind int

	confirmKind      int
	pendingDropRole  string
	pendingForceRole string
	pendingPwdRole   string
	pendingLimitRole string
	pendingAttrsRole string
	pendingAttrsSQL  string
	newPassword      string
	pwdIterations    int

	loading bool
	err     error
	status  string

	width, height int
}

func newRolesView(cfg *config.Config, mgr *db.Manager) *rolesView {
	return &rolesView{cfg: cfg, mgr: mgr, tbl: newTable(), confirm: newConfirmModal(),
		alert: newAlertModal(), finder: newFinder()}
}

func (v *rolesView) Title() string { return "Roles" }

func (v *rolesView) CapturingInput() bool {
	return v.form.active || v.confirm.active || v.alert.active || v.menu.active || v.finder.active
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
	nameW := clampInt(w-76, 16, 40)
	v.tbl.SetColumns([]table.Column{
		{Title: "ROLE", Width: nameW},
		{Title: "LOGIN", Width: 6},
		{Title: "SUPER", Width: 7},
		{Title: "BYPASSRLS", Width: 10},
		{Title: "CREATEDB", Width: 9},
		{Title: "CREATEROLE", Width: 11},
		{Title: "CONN", Width: 5},
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
					r.Name, yesno(r.CanLogin), flagged(r.Super), flagged(r.BypassRLS),
					yesno(r.CreateDB), yesno(r.CreateRole),
					connLimitStr(r.ConnLimit), r.MemberOf,
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
		if strings.HasPrefix(msg.action, "reset password ") && v.newPassword != "" {
			v.showNewPassword(v.pendingPwdRole, v.newPassword)
			v.newPassword = ""
		}
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
	if v.finder.active {
		res, cmd := v.finder.update(msg)
		if res == finderSelect {
			if idx := v.finder.selectedIndex(); idx >= 0 && idx < len(v.roles) {
				v.tbl.SetCursor(idx)
			}
			v.finder.close()
		}
		return cmd
	}
	if v.menu.active {
		if v.menu.update(msg) == menuSelect {
			return v.runMenuAction(v.menu.cursor)
		}
		return nil
	}
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return v.onConfirmYes()
		case confirmNo:
			v.status = stStatus.Render("cancelled")
			v.confirmKind = confirmNoneKind
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
	case "enter":
		return v.openRoleMenu()
	case "/":
		return v.openFinder()
	case "r":
		return v.Init()
	case "n":
		return v.openCreateForm()
	case "g":
		return v.openGrantForm()
	case "R":
		return v.openRevokeForm()
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
	labels := make([]string, len(grantScopes))
	for i, sc := range grantScopes {
		labels[i] = sc.Label()
	}
	v.formKind = formGrant
	return v.form.open("Grant · to "+role.Name, []formField{
		selectField("database", "Database", v.dbNames),
		selectField("scope", "Privilege", labels),
	})
}

// grantScopes maps the privilege select in the grant form to its scope, in the
// order the options are listed. Revoke uses the same order minus ownership.
var grantScopes = []db.GrantScope{
	db.GrantConnect, db.GrantAllDatabase,
	db.GrantSchemaReadOnly, db.GrantSchemaReadWrite, db.GrantSchemaAll,
	db.GrantOwner,
}

// revokeScopes is derived from grantScopes so the two menus can never drift:
// a scope added to one shows up in the other unless it has no REVOKE at all.
var revokeScopes = revocable(grantScopes)

func revocable(scopes []db.GrantScope) []db.GrantScope {
	out := make([]db.GrantScope, 0, len(scopes))
	for _, s := range scopes {
		if s.Revocable() {
			out = append(out, s)
		}
	}
	return out
}

func (v *rolesView) openRevokeForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.status = stWarnV.Render("select a role first")
		return nil
	}
	if len(v.dbNames) == 0 {
		v.status = stWarnV.Render("database list not loaded yet")
		return nil
	}
	labels := make([]string, len(revokeScopes))
	for i, sc := range revokeScopes {
		labels[i] = sc.Label()
	}
	v.formKind = formRevoke
	return v.form.open("Revoke · from "+role.Name, []formField{
		selectField("database", "Database", v.dbNames),
		selectField("scope", "Privilege", labels),
	})
}

// openAttrsForm edits the role attributes, pre-filled with the current values so
// the operator sees the starting state instead of guessing it.
func (v *rolesView) openAttrsForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.menu.close()
		return nil
	}
	v.menu.close()
	v.pendingAttrsRole = role.Name
	v.formKind = formAlterAttrs
	return v.form.open("Attributes · "+role.Name, []formField{
		boolField("login", "LOGIN", role.CanLogin),
		boolField("createdb", "CREATEDB", role.CreateDB),
		boolField("createrole", "CREATEROLE", role.CreateRole),
		boolField("superuser", "SUPERUSER", role.Super),
		boolField("replication", "REPLICATION", role.Replication),
		boolField("bypassrls", "BYPASSRLS", role.BypassRLS),
	})
}

// boolField is a yes/no select already positioned on the current value.
func boolField(key, label string, on bool) formField {
	f := selectField(key, label, []string{"no", "yes"})
	if on {
		f.sel = 1
	}
	return f
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
	v.confirmKind = confirmDropRole
	body := fmt.Sprintf("This will remove role %s from the cluster.\nType the name to confirm:",
		stBadV.Render(role.Name))
	return v.confirm.askCritical("⚠  DROP ROLE", body, role.Name)
}

// openFinder opens the quick-find overlay to jump to a role by name.
func (v *rolesView) openFinder() tea.Cmd {
	if len(v.roles) == 0 {
		v.status = stWarnV.Render("no roles to search")
		return nil
	}
	items := make([]finderItem, len(v.roles))
	for i, r := range v.roles {
		items[i] = finderItem{index: i, label: r.Name}
	}
	return v.finder.open("Find role", items)
}

// openRoleMenu opens the per-role actions menu for the highlighted role.
func (v *rolesView) openRoleMenu() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.status = stWarnV.Render("select a role first")
		return nil
	}
	v.menu.open("Manage role · "+role.Name, roleMenuItems)
	return nil
}

// runMenuAction dispatches the chosen action from the role menu.
func (v *rolesView) runMenuAction(idx int) tea.Cmd {
	switch idx {
	case menuResetPassword:
		return v.askResetPassword()
	case menuSetConnLimit:
		return v.openConnLimitForm()
	case menuEditAttrs:
		return v.openAttrsForm()
	}
	return nil
}

// openConnLimitForm opens the connection-limit editor for the selected role,
// pre-filled with the current limit (-1 = unlimited).
func (v *rolesView) openConnLimitForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.menu.close()
		return nil
	}
	v.menu.close()
	v.pendingLimitRole = role.Name
	v.formKind = formConnLimit
	f := textField("limit", "Connection limit", "-1 = unlimited")
	f.input.SetValue(strconv.Itoa(role.ConnLimit))
	return v.form.open("Connection limit · "+role.Name, []formField{f})
}

// askResetPassword confirms before generating and applying a new password.
func (v *rolesView) askResetPassword() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.menu.close()
		return nil
	}
	v.menu.close()
	v.pendingPwdRole = role.Name
	v.confirmKind = confirmResetPwd
	body := fmt.Sprintf("Generate a new random %d-character password for %s?\n"+
		"The current password is replaced immediately; the new one is shown once.",
		newPasswordLen, stValue.Render(role.Name))
	return v.confirm.ask("Reset password", body)
}

// onConfirmYes runs the statement for whichever confirmation was pending.
func (v *rolesView) onConfirmYes() tea.Cmd {
	kind := v.confirmKind
	v.confirmKind = confirmNoneKind
	switch kind {
	case confirmEscalate:
		sql := v.pendingAttrsSQL
		v.pendingAttrsSQL = ""
		return execStatements(v.mgr, "", "alter role "+v.pendingAttrsRole, []string{sql})

	case confirmResetPwd:
		role := v.pendingPwdRole
		pwd, err := db.GeneratePassword(newPasswordLen)
		if err != nil {
			v.status = stBadV.Render("could not generate password: " + err.Error())
			return nil
		}
		// Hash client-side and send only the SCRAM-SHA-256 verifier — the
		// plaintext never reaches the server (nor its logs). The user still
		// gets the plaintext on screen to log in with.
		secret, rounds, err := db.SCRAMSHA256Secret(pwd, v.cfg.SCRAMIterations)
		if err != nil {
			v.status = stBadV.Render("could not hash password: " + err.Error())
			return nil
		}
		v.newPassword = pwd
		v.pwdIterations = rounds
		return execStatements(v.mgr, "", "reset password "+role, []string{db.BuildAlterRolePassword(role, secret)})
	default: // confirmDropRole
		return execStatements(v.mgr, "", "drop role "+v.pendingDropRole, []string{db.BuildDropRole(v.pendingDropRole)})
	}
}

// showNewPassword displays the freshly generated password in a modal so the
// user can copy it. It is not stored anywhere and cannot be shown again.
func (v *rolesView) showNewPassword(role, pwd string) {
	body := stLabel.Render("New password for ") + stValue.Render(role) + "\n\n" +
		stGood.Render(pwd) + "\n\n" +
		stKeyHint.Render("Copy it now — it is not stored and cannot be shown again.\n"+
			"Only letters and digits: safe to paste in any terminal or connection string.\n"+
			fmt.Sprintf("Sent as a SCRAM-SHA-256 hash (%d rounds) — the plaintext never left this session.", v.pwdIterations))
	v.alert.show(v.width, v.height, "Password reset", body, false)
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
		// Hash the password client-side (SCRAM-SHA-256) when possible so the
		// plaintext never reaches the server; a non-ASCII password falls back
		// to plaintext so the server can SASLprep and hash it correctly.
		secret, _, err := db.PasswordSecret(v.form.value("password"), v.cfg.SCRAMIterations)
		if err != nil {
			v.status = stBadV.Render("could not hash password: " + err.Error())
			return nil
		}
		sql := db.BuildCreateRole(name, secret, login == "yes", createdb == "yes", createrole == "yes")
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
		targetDB, stmts := db.BuildGrant(grantScopes[scopeIdx], database, role.Name)
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, targetDB, fmt.Sprintf("grant %s → %s", role.Name, database), stmts)

	case formRevoke:
		role, ok := v.selectedRole()
		if !ok {
			v.form.close()
			v.formKind = formNoneKind
			return nil
		}
		_, database := v.form.selected("database")
		scopeIdx, _ := v.form.selected("scope")
		targetDB, stmts := db.BuildRevoke(revokeScopes[scopeIdx], database, role.Name)
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, targetDB, fmt.Sprintf("revoke %s → %s", role.Name, database), stmts)

	case formAlterAttrs:
		role, ok := v.selectedRole()
		if !ok || role.Name != v.pendingAttrsRole {
			v.form.close()
			v.formKind = formNoneKind
			v.status = stWarnV.Render("selection changed — reopen the form")
			return nil
		}
		// selected(), not value(): a select field keeps its choice in `sel`, and
		// value() reads the (empty) text input. Reading the wrong one made every
		// attribute look like "no", which turns an untouched form into an
		// ALTER ROLE that switches everything off.
		on := func(key string) bool {
			_, v := v.form.selected(key)
			return v == "yes"
		}
		want := db.RoleAttrs{
			Login:       on("login"),
			CreateDB:    on("createdb"),
			CreateRole:  on("createrole"),
			Superuser:   on("superuser"),
			Replication: on("replication"),
			BypassRLS:   on("bypassrls"),
		}
		have := role.Attrs()
		sql := db.BuildAlterRoleAttrs(role.Name, have, want)
		v.form.close()
		v.formKind = formNoneKind
		if sql == "" {
			v.status = stStatus.Render("nothing changed")
			return nil
		}
		// Turning on SUPERUSER or BYPASSRLS hands the role every row of every
		// table, RLS included. It is guarded like a drop: type the exact name.
		if want.Escalates(have) {
			v.pendingAttrsSQL = sql
			v.confirmKind = confirmEscalate
			body := fmt.Sprintf("%s will be able to read and write every row of every table,\n"+
				"ignoring row-level security.\n\n%s\n\nType the role name to confirm:",
				stBadV.Render(role.Name), stLabel.Render(sql))
			return v.confirm.askCritical("⚠  PRIVILEGE ESCALATION", body, role.Name)
		}
		return execStatements(v.mgr, "", "alter role "+role.Name, []string{sql})

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

	case formConnLimit:
		raw := strings.TrimSpace(v.form.value("limit"))
		n, err := strconv.Atoi(raw)
		if err != nil || n < -1 {
			v.status = stWarnV.Render("enter an integer ≥ -1 (-1 = unlimited)")
			return nil
		}
		role := v.pendingLimitRole
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, "", "set connection limit "+role, []string{db.BuildRoleConnLimit(role, n)})
	}
	return nil
}

func (v *rolesView) FooterHints() string {
	return hint("enter", "manage") + "  " + hint("/", "find") + "  " + hint("n", "create") + "  " +
		hint("g", "grant") + "  " + hint("R", "revoke") + "  " + hint("D", "drop") + "  " +
		hint("F", "force-drop") + "  " + hint("r", "refresh")
}

func (v *rolesView) View() string {
	if v.finder.active {
		return v.finder.view(v.width, v.height)
	}
	if v.alert.active {
		return v.alert.view(v.width, v.height)
	}
	if v.menu.active {
		return v.menu.view(v.width, v.height)
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

// flagged renders an attribute that defeats row-level security. SUPERUSER and
// BYPASSRLS both let a role read every tenant's rows, and on a database whose
// isolation rests on RLS that is the one thing an operator must not overlook in
// a list of thirty roles.
func flagged(b bool) string {
	if b {
		return "⚠ yes"
	}
	return "·"
}

// connLimitStr renders a role's connection limit; -1 (unlimited) shows as ∞.
func connLimitStr(n int) string {
	if n < 0 {
		return "∞"
	}
	return strconv.Itoa(n)
}
