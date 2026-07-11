# pgtui

TUI de administração de **PostgreSQL** para sysadmins — navegação 100% por teclado.
Conecta em um cluster via `DATABASE_URL` e oferece, em seis abas:

| Aba | O que faz |
|-----|-----------|
| **1 · Dashboard** | Saúde do cluster: conexões vs `max_connections`, cache hit ratio, uptime, tamanho total, commits/rollbacks, versão, query ativa mais longa e replicação. Auto-refresh. |
| **2 · Bancos** | Databases (owner, tamanho, conexões) → tabelas → **dados em modo leitura** (scroll horizontal `←→`, busca na coluna `/`, query no topo `e`). Também: **criar** (`n`) e **apagar** (`D`) database e **describe** da tabela (`d`: colunas, tipos, índices, constraints). |
| **3 · Query** | Editor SQL com grid paginado. `x` roda **EXPLAIN** (plano, sem executar). Escrita pede confirmação; destrutivo (`DROP`/`TRUNCATE`/`DELETE`/`UPDATE` sem `WHERE`) exige digitar `sim`. |
| **4 · Locks** | Árvore de bloqueios: quem espera por qual sessão. |
| **5 · Sessões** | `pg_stat_activity`: sessões de cliente com estado/espera/duração/query. **`c`** cancela a query (`pg_cancel_backend`), **`k`** encerra a conexão (`pg_terminate_backend`). |
| **6 · Roles** | Roles do cluster (login, super, createdb/role, membros). **`n`** cria role/usuário (com senha, atributos), **`g`** faz grant a um database (CONNECT / ALL / owner / acesso total ao schema), **`D`** apaga role (confirmação por nome). |

```
┌ pgtui ──────────────────────────── postgres@192.168.1.242:5432 · admin db: postgres ┐
│ 1 Dashboard  2 Bancos  3 Query  4 Locks  5 Sessões  6 Roles                         │
│ ╭ CONEXÕES ─╮ ╭ CACHE HIT ╮ ╭ ARMAZENAMENTO ╮ ╭ UPTIME ─╮                           │
│ │ 40 / 50   │ │ 100.00%   │ │ 217 MB        │ │ 2h 27m  │                           │
│ ╰───────────╯ ╰───────────╯ ╰───────────────╯ ╰─────────╯                           │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

> **Gestão sem superusuário:** criar/dropar role e database e fazer grants
> funcionam com um papel que tenha `CREATEROLE`/`CREATEDB` — não exige
> superusuário. Toda ação destrutiva (apagar role/database) pede que você
> **digite o nome** para confirmar; nada é apagado automaticamente.

## Requisitos

- Go **1.26+** (só para compilar)
- Um PostgreSQL alcançável e um papel com permissão de leitura em `pg_stat_*` /
  `pg_database`. Para o dashboard completo de atividade, um superusuário (ou
  `pg_monitor`) enxerga as queries de todas as sessões.

## Configuração

O `pgtui` lê um arquivo `.env` no diretório atual (ou ao lado do binário):

```bash
cp .env.example .env
$EDITOR .env
```

```dotenv
DATABASE_URL=postgres://USER:PASSWORD@HOST:5432/postgres?sslmode=disable
PGTUI_REFRESH_SECONDS=5
```

- O database na URL é o **admin db** — de onde saem as consultas de cluster
  (`pg_stat_activity`, `pg_database`, replicação, locks). O TUI abre conexões
  adicionais sob demanda ao navegar para as tabelas de outro banco ou ao rodar
  uma query com alvo diferente (`ctrl+t`).
- Também aceita as variáveis padrão `PGHOST`/`PGPORT`/`PGUSER`/`PGPASSWORD`/
  `PGDATABASE`/`PGSSLMODE` caso `DATABASE_URL` não esteja definido.
- Variáveis já exportadas no ambiente têm precedência sobre o `.env`.

## Uso

```bash
make build      # gera ./pgtui
./pgtui         # lê ./.env

# ou direto:
make run
```

### Atalhos

| Tecla | Ação |
|-------|------|
| `1`–`6` | trocar de aba |
| `tab` / `shift+tab` | próxima / aba anterior |
| `?` | ajuda (todos os atalhos) |
| `q` / `ctrl+c` | sair |
| **Dashboard** | |
| `r` | atualizar agora (auto a cada `PGTUI_REFRESH_SECONDS`) |
| **Bancos** | |
| `↑`/`↓` `j`/`k` | navegar |
| `enter` | banco → tabelas → **dados da tabela** (leitura) |
| `d` | describe da tabela (colunas, tipos, índices, constraints) |
| `n` / `D` | criar / apagar database (apagar pede o nome) |
| `esc` | voltar um nível |
| `r` | recarregar |
| **Dados da tabela** | |
| `←`/`→` `h`/`l` | navegar entre colunas (scroll horizontal) |
| `/` | buscar na coluna ativa (`ILIKE '%termo%'`) |
| `e` / `:` | editar a query do topo (somente leitura) |
| `r` | resetar para `SELECT *` |
| **Query** | |
| `i` / `enter` | focar o editor SQL |
| `/` (em navegação) ou `ctrl+t` | trocar o database alvo — lista filtrável |
| `x` | EXPLAIN (plano, sem executar) |
| `ctrl+r` / `f5` | executar |
| `esc` | sair do editor (foca os resultados) |
| `↑`/`↓` | rolar o grid de resultados |
| **Locks** | |
| `r` | recarregar a árvore de bloqueios |
| **Sessões** | |
| `c` | cancelar a query da sessão (`pg_cancel_backend`) |
| `k` | encerrar a conexão (`pg_terminate_backend`) |
| `r` | atualizar |
| **Roles** | |
| `n` | criar role/usuário (nome, senha, login, createdb/role) |
| `g` | grant a um database (CONNECT / ALL / owner / schema public) |
| `D` | apagar role (pede o nome para confirmar) |

### Guarda contra operações destrutivas

O query runner classifica cada statement antes de executar:

- **SAFE** (`SELECT`/`WITH…SELECT`/`EXPLAIN`/`SHOW`) → roda direto.
- **WRITE** (`INSERT`/`UPDATE…WHERE`/`ALTER`/`CREATE`/…) → confirma com `y`.
- **CRITICAL** (`DROP DATABASE`/`DROP TABLE`/`TRUNCATE`/`DELETE` ou `UPDATE`
  **sem** `WHERE`) → exige digitar `sim` e pressionar `Enter`.

Não há nenhum caminho no TUI que apague bancos automaticamente — qualquer
`DROP`/`TRUNCATE` só acontece se você digitá-lo e confirmá-lo.

As ações de gestão nas abas **Bancos** e **Roles** seguem a mesma regra:
apagar um database (`D`) ou um role (`D`) abre um diálogo que só confirma
quando você **digita o nome exato** do objeto. Encerrar/cancelar sessão pede
um `y`/`n`. As ações administrativas rodam no protocolo simples do pgx
(necessário para `CREATE`/`DROP DATABASE`) e usam identificadores quotados.

## Desenvolvimento

```bash
make test       # unit + smoke test de integração (precisa de DATABASE_URL)
make vet
```

O smoke test (`internal/db`) e o teste de render da UI (`internal/ui`) rodam
contra um Postgres real quando `DATABASE_URL` está definido; caso contrário são
pulados. Ambos são **read-only**.

Para depurar a UI (o alt-screen ocupa o stdout), aponte `PGTUI_DEBUG` para um
arquivo e acompanhe com `tail -f`:

```bash
PGTUI_DEBUG=/tmp/pgtui.log ./pgtui
```

> **Pegadinha ao testar num PTY headless:** no startup o Bubble Tea/termenv
> emite uma query `OSC 11` (cor de fundo) e *bloqueia esperando a resposta do
> terminal*, engolindo as primeiras teclas nesse meio-tempo. Num terminal real a
> resposta é instantânea; num PTY automatizado, o driver precisa responder à
> `\x1b]11;?` (e a `\x1b[c` / `\x1b[6n`) senão parece que o app "perde" as
> teclas iniciais e trava por alguns segundos.

## Distribuição

`pgtui` é um **utilitário Go compilado** — um binário único, estático
(`CGO_ENABLED=0`), sem runtime nem container. Roda direto no terminal.

```bash
make build                    # gera ./pgtui para a plataforma atual
make install                  # instala em /usr/local/bin (sudo)
make build-all VERSION=v0.3.0 # cross-compila para dist/ (linux/darwin, amd64/arm64)
```

Release por tag:

```bash
make release VERSION=v0.3.0   # valida semver + working tree limpa, cria a tag e faz push
```

Fluxo: `git push` da tag `v*.*.*` → GitHub Actions (testes → `build-all`) →
binários anexados ao **GitHub Release** (`.github/workflows/ci.yml`).

**Rollback:** binário sem estado — basta rodar/instalar uma tag anterior
(baixe o binário do release antigo, ou `git checkout v0.2.0 && make install`).
O `pgtui` não altera schema próprio, então não há migration a reverter.
