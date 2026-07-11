package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/9level/pg-tui/internal/db"
)

const (
	formNoneKind = iota
	formCreateRole
	formGrant
)

type rolesView struct {
	mgr *db.Manager

	tbl     table.Model
	roles   []db.Role
	dbNames []string

	form     form
	confirm  confirmModal
	alert    alertModal
	formKind int

	pendingDropRole string

	loading bool
	err     error
	status  string

	width, height int
}

func newRolesView(mgr *db.Manager) *rolesView {
	return &rolesView{mgr: mgr, tbl: newTable(), confirm: newConfirmModal(), alert: newAlertModal()}
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
		{Title: "MEMBRO DE", Width: 22},
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
			if strings.HasPrefix(msg.action, "dropar role") {
				body += "\n\n" + dropRoleHint
			}
			v.alert.show(v.width, v.height, "Falha ao "+msg.action, body, true)
			return nil
		}
		v.status = stGood.Render("✓ " + msg.action + " ok")
		return loadRoles(v.mgr)

	case tea.KeyMsg:
		return v.handleKey(msg)
	}
	return nil
}

const dropRoleHint = "COMO RESOLVER: o role possui objetos ou tem privilégios concedidos. " +
	"Reatribua os objetos (REASSIGN OWNED BY \"role\" TO \"outro\") ou remova-os " +
	"(DROP OWNED BY \"role\") em CADA database onde o role tem objetos, e revogue " +
	"privilégios/ownership de databases, antes do DROP ROLE. Rode esses comandos " +
	"pela aba Query (trocando o database alvo com '/')."

func (v *rolesView) handleKey(msg tea.KeyMsg) tea.Cmd {
	if v.alert.active {
		v.alert.update(msg)
		return nil
	}
	if v.confirm.active {
		switch v.confirm.update(msg) {
		case confirmYes:
			return execStatements(v.mgr, "", "dropar role "+v.pendingDropRole, []string{db.BuildDropRole(v.pendingDropRole)})
		case confirmNo:
			v.status = stStatus.Render("cancelado")
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
	return v.form.open("Criar role/usuário", []formField{
		textField("name", "Nome", "ex.: app_user"),
		secretField("password", "Senha"),
		selectField("login", "Pode logar", []string{"sim", "não"}),
		selectField("createdb", "CREATEDB", []string{"não", "sim"}),
		selectField("createrole", "CREATEROLE", []string{"não", "sim"}),
	})
}

func (v *rolesView) openGrantForm() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		v.status = stWarnV.Render("selecione um role primeiro")
		return nil
	}
	if len(v.dbNames) == 0 {
		v.status = stWarnV.Render("lista de databases ainda não carregou")
		return nil
	}
	v.formKind = formGrant
	return v.form.open("Grant · conceder a "+role.Name, []formField{
		selectField("database", "Database", v.dbNames),
		selectField("scope", "Privilégio", []string{
			db.GrantConnect.Label(),
			db.GrantAllDatabase.Label(),
			db.GrantSchemaAll.Label(),
			db.GrantOwner.Label(),
		}),
	})
}

func (v *rolesView) askDropRole() tea.Cmd {
	role, ok := v.selectedRole()
	if !ok {
		return nil
	}
	v.pendingDropRole = role.Name
	body := fmt.Sprintf("Isto vai remover o role %s do cluster.\nDigite o nome para confirmar:",
		stBadV.Render(role.Name))
	return v.confirm.askCritical("⚠  DROP ROLE", body, role.Name)
}

func (v *rolesView) submitForm() tea.Cmd {
	switch v.formKind {
	case formCreateRole:
		name := strings.TrimSpace(v.form.value("name"))
		if name == "" {
			v.status = stWarnV.Render("nome obrigatório")
			return nil
		}
		_, login := v.form.selected("login")
		_, createdb := v.form.selected("createdb")
		_, createrole := v.form.selected("createrole")
		sql := db.BuildCreateRole(name, v.form.value("password"), login == "sim", createdb == "sim", createrole == "sim")
		v.form.close()
		v.formKind = formNoneKind
		return execStatements(v.mgr, "", "criar role "+name, []string{sql})

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
	}
	return nil
}

func (v *rolesView) FooterHints() string {
	return hint("n", "criar") + "   " + hint("g", "grant") + "   " + hint("D", "dropar") + "   " +
		hint("r", "atualizar") + "   " + hint("↑↓", "navegar")
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
		return "\n" + stErr.Render("Erro: "+v.err.Error())
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(colAccent).Render("Roles do cluster")
	meta := stLabel.Render(fmt.Sprintf("  %d roles", len(v.roles)))
	if v.loading {
		meta = stLabel.Render("  carregando…")
	}
	head := "\n" + title + meta
	if v.status != "" {
		head += "   " + v.status
	}
	return head + "\n" + v.tbl.View()
}

func yesno(b bool) string {
	if b {
		return "sim"
	}
	return "·"
}
