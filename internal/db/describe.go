package db

import "context"

// ColumnDef descreve uma coluna de tabela.
type ColumnDef struct {
	Name     string
	Type     string
	Nullable bool
	Default  string
}

// IndexDef descreve um índice.
type IndexDef struct {
	Name string
	Def  string
}

// ConstraintDef descreve uma constraint (PK/FK/UNIQUE/CHECK).
type ConstraintDef struct {
	Name string
	Type string // p, f, u, c, ...
	Def  string
}

// TableDescription é o "\d" de uma tabela.
type TableDescription struct {
	Schema      string
	Table       string
	Columns     []ColumnDef
	Indexes     []IndexDef
	Constraints []ConstraintDef
}

// DescribeTable retorna colunas, índices e constraints de uma tabela.
func DescribeTable(ctx context.Context, p Pinger, schema, table string) (TableDescription, error) {
	d := TableDescription{Schema: schema, Table: table}
	rel := QuoteQualified(schema, table)

	cols, err := p.Query(ctx, `
		select a.attname,
		       pg_catalog.format_type(a.atttypid, a.atttypmod),
		       not a.attnotnull,
		       coalesce(pg_get_expr(ad.adbin, ad.adrelid), '')
		from pg_attribute a
		left join pg_attrdef ad on ad.adrelid = a.attrelid and ad.adnum = a.attnum
		where a.attrelid = $1::regclass and a.attnum > 0 and not a.attisdropped
		order by a.attnum`, rel)
	if err != nil {
		return d, err
	}
	for cols.Next() {
		var c ColumnDef
		if err := cols.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default); err != nil {
			cols.Close()
			return d, err
		}
		d.Columns = append(d.Columns, c)
	}
	cols.Close()
	if err := cols.Err(); err != nil {
		return d, err
	}

	idx, err := p.Query(ctx, `
		select indexname, indexdef
		from pg_indexes
		where schemaname = $1 and tablename = $2
		order by indexname`, schema, table)
	if err != nil {
		return d, err
	}
	for idx.Next() {
		var i IndexDef
		if err := idx.Scan(&i.Name, &i.Def); err != nil {
			idx.Close()
			return d, err
		}
		d.Indexes = append(d.Indexes, i)
	}
	idx.Close()

	con, err := p.Query(ctx, `
		select conname, contype::text, pg_get_constraintdef(oid)
		from pg_constraint
		where conrelid = $1::regclass
		order by contype, conname`, rel)
	if err != nil {
		return d, err
	}
	for con.Next() {
		var c ConstraintDef
		if err := con.Scan(&c.Name, &c.Type, &c.Def); err != nil {
			con.Close()
			return d, err
		}
		d.Constraints = append(d.Constraints, c)
	}
	con.Close()
	return d, con.Err()
}
