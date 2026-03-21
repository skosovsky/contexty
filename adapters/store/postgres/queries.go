package postgres

import (
	"embed"
	"fmt"
	"text/template"
)

//go:embed queries/*.sql
var queryFS embed.FS

type queryTables struct {
	Table     string
	MetaTable string
}

type renderedQueries struct {
	load        string
	append      string
	clear       string
	loadVersion string
	ensureMeta  string
	bumpVersion string
}

func renderQueries(table string) renderedQueries {
	tables := queryTables{
		Table:     table,
		MetaTable: table + "_meta",
	}
	return renderedQueries{
		load:        mustRenderQuery("queries/load.sql", tables),
		append:      mustRenderQuery("queries/append.sql", tables),
		clear:       mustRenderQuery("queries/clear.sql", tables),
		loadVersion: mustRenderQuery("queries/load_version.sql", tables),
		ensureMeta:  mustRenderQuery("queries/ensure_meta.sql", tables),
		bumpVersion: mustRenderQuery("queries/bump_version.sql", tables),
	}
}

func mustRenderQuery(path string, tables queryTables) string {
	raw, err := queryFS.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("contexty/postgres: read query %s: %v", path, err))
	}

	tpl, err := template.New(path).Parse(string(raw))
	if err != nil {
		panic(fmt.Sprintf("contexty/postgres: parse query %s: %v", path, err))
	}

	var rendered stringBuilder
	if err := tpl.Execute(&rendered, tables); err != nil {
		panic(fmt.Sprintf("contexty/postgres: render query %s: %v", path, err))
	}

	return rendered.String()
}

type stringBuilder struct {
	bytes []byte
}

func (b *stringBuilder) Write(p []byte) (int, error) {
	b.bytes = append(b.bytes, p...)
	return len(p), nil
}

func (b *stringBuilder) String() string {
	return string(b.bytes)
}
