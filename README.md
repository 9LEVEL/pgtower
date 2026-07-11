# pgtui

TUI de administração de **PostgreSQL** para sysadmins — navegação 100% por teclado.
Conecta em um cluster via `DATABASE_URL` e oferece, em quatro abas:

| Aba | O que faz |
|-----|-----------|
| **1 · Dashboard** | Saúde do cluster: conexões vs `max_connections`, cache hit ratio, uptime, tamanho total, commits/rollbacks, versão, query ativa mais longa e replicação. Auto-refresh. |
| **2 · Bancos** | Lista os databases (owner, tamanho, conexões) e, ao entrar num banco, suas tabelas com tamanho total / heap / índices e estimativa de linhas. |
| **3 · Query** | Editor SQL com grid de resultados paginado. Statements de **escrita** pedem confirmação; **destrutivos** (`DROP`/`TRUNCATE`/`DELETE`/`UPDATE` sem `WHERE`) exigem digitar `sim`. |
| **4 · Locks** | Árvore de bloqueios: quem está esperando por qual sessão. |

```
┌ pgtui ──────────────────────────── postgres@192.168.1.242:5432 · admin db: postgres ┐
│ 1 Dashboard   2 Bancos   3 Query   4 Locks                                          │
│ ╭ CONEXÕES ─╮ ╭ CACHE HIT ╮ ╭ ARMAZENAMENTO ╮ ╭ UPTIME ─╮                           │
│ │ 40 / 50   │ │ 100.00%   │ │ 217 MB        │ │ 2h 27m  │                           │
│ ╰───────────╯ ╰───────────╯ ╰───────────────╯ ╰─────────╯                           │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

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
| `1`–`4` | trocar de aba |
| `tab` / `shift+tab` | próxima / aba anterior |
| `?` | ajuda (todos os atalhos) |
| `q` / `ctrl+c` | sair |
| **Dashboard** | |
| `r` | atualizar agora (auto a cada `PGTUI_REFRESH_SECONDS`) |
| **Bancos** | |
| `↑`/`↓` `j`/`k` | navegar |
| `enter` | abrir tabelas do banco selecionado |
| `esc` | voltar para a lista de bancos |
| `r` | recarregar |
| **Query** | |
| `i` / `enter` | focar o editor SQL |
| `/` (em navegação) ou `ctrl+t` | trocar o database alvo — abre uma lista filtrável dos bancos do cluster |
| `ctrl+r` / `f5` | executar |
| `esc` | sair do editor (foca os resultados) |
| `↑`/`↓` | rolar o grid de resultados |
| **Locks** | |
| `r` | recarregar a árvore de bloqueios |

### Guarda contra operações destrutivas

O query runner classifica cada statement antes de executar:

- **SAFE** (`SELECT`/`WITH…SELECT`/`EXPLAIN`/`SHOW`) → roda direto.
- **WRITE** (`INSERT`/`UPDATE…WHERE`/`ALTER`/`CREATE`/…) → confirma com `y`.
- **CRITICAL** (`DROP DATABASE`/`DROP TABLE`/`TRUNCATE`/`DELETE` ou `UPDATE`
  **sem** `WHERE`) → exige digitar `sim` e pressionar `Enter`.

Não há nenhum caminho no TUI que apague bancos automaticamente — qualquer
`DROP`/`TRUNCATE` só acontece se você digitá-lo e confirmá-lo.

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

`pgtui` é um binário único e estático — o jeito canônico de rodar é compilar e
copiar o executável:

```bash
make release VERSION=v0.1.0   # valida semver + working tree limpa, cria a tag e faz push
make install                  # instala em /usr/local/bin (sudo)
```

O push da tag `v*.*.*` dispara o CI (`.github/workflows/ci.yml`), que roda os
testes e publica a imagem em `ghcr.io/9level/pg-tui:<tag>` + `:latest` para quem
preferir rodar containerizado (`docker run -it --rm --env-file .env ghcr.io/9level/pg-tui`).
