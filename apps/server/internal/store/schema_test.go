// SPDX-FileCopyrightText: 2026 CubieCode
// SPDX-License-Identifier: AGPL-3.0-only

package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"wardlock.nz/server/migrations"
)

// forbiddenSQL lists constructs that would not run unchanged on Postgres
// (docs/ARCHITECTURE.md, Database; wardlock-go-service, Portable SQL).
var forbiddenSQL = []struct {
	what string
	re   *regexp.Regexp
}{
	{"AUTOINCREMENT", regexp.MustCompile(`(?i)\bAUTOINCREMENT\b`)},
	{"INSERT OR …", regexp.MustCompile(`(?i)\bINSERT\s+OR\s+\w+`)},
	{"REPLACE statement", regexp.MustCompile(`(?i)(^|;)\s*REPLACE\b|\bREPLACE\s+INTO\b`)},
	{"SQLite-only function", regexp.MustCompile(`(?i)\b(datetime|date|time|julianday|strftime|unixepoch|ifnull|iif|glob|printf|instr|group_concat|last_insert_rowid|changes|total_changes|randomblob|zeroblob|hex|char|total|likely|unlikely|typeof|json_\w+|sqlite_\w+)\s*\(`)},
	{"LIMIT -1", regexp.MustCompile(`(?i)\bLIMIT\s+-\s*1\b`)},
	{"GLOB operator", regexp.MustCompile(`(?i)\bGLOB\b`)},
	{"rowid", regexp.MustCompile(`(?i)\b(_?rowid_?|oid)\b`)},
	{"WITHOUT ROWID or STRICT table", regexp.MustCompile(`(?i)\bWITHOUT\s+ROWID\b|\)\s*STRICT\b`)},
	{"PRAGMA", regexp.MustCompile(`(?i)\bPRAGMA\b`)},
	{"positional or SQLite-style parameter", regexp.MustCompile(`\?\d*|\$\d+|[:@]\w+`)},
}

// sqlLiteralOrComment matches whichever starts first, a string literal or a
// line comment, so "--" inside a literal and an apostrophe inside a comment
// are both read correctly.
var sqlLiteralOrComment = regexp.MustCompile(`'(?:[^']|'')*'|--[^\n]*`)

// minMaxCall finds min( and max( calls; scalarMinMax decides which take more
// than one argument (SQLite's scalar form; Postgres spells it LEAST/GREATEST).
var minMaxCall = regexp.MustCompile(`(?i)\b(min|max)\s*\(`)

// unportable returns the forbidden constructs in src, ignoring comments and
// string literals.
func unportable(src string) []string {
	code := sqlLiteralOrComment.ReplaceAllStringFunc(src, func(m string) string {
		if strings.HasPrefix(m, "--") {
			return ""
		}
		return "''"
	})
	var found []string
	for _, f := range forbiddenSQL {
		if m := f.re.FindString(code); m != "" {
			found = append(found, fmt.Sprintf("%s (%q)", f.what, strings.TrimSpace(m)))
		}
	}
	if m := scalarMinMax(code); m != "" {
		found = append(found, fmt.Sprintf("scalar min/max (%q)", m))
	}
	return found
}

// scalarMinMax returns the first min( or max( call in code with a top-level
// comma in its arguments, or "" if every call is the one-argument aggregate.
func scalarMinMax(code string) string {
	for _, loc := range minMaxCall.FindAllStringIndex(code, -1) {
		depth := 1
		for i := loc[1]; i < len(code) && depth > 0; i++ {
			switch code[i] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 1 {
					return code[loc[0]:loc[1]]
				}
			}
		}
	}
	return ""
}

func TestUnportableCatchesForbiddenSQL(t *testing.T) {
	for _, bad := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT);",
		"INSERT OR REPLACE INTO t (id) VALUES (sqlc.arg(id));",
		"insert or ignore into t (id) values (sqlc.arg(id));",
		"REPLACE INTO t (id) VALUES (sqlc.arg(id));",
		"SELECT 1; replace into t values (1);",
		"SELECT datetime('now');",
		"SELECT strftime('%s', 'now');",
		"SELECT ifnull(a, b) FROM t;",
		"SELECT unixepoch();",
		"SELECT * FROM t WHERE name GLOB 'a*';",
		"SELECT rowid FROM t;",
		"CREATE TABLE t (id TEXT PRIMARY KEY) WITHOUT ROWID;",
		"CREATE TABLE t (id TEXT PRIMARY KEY) STRICT;",
		"PRAGMA foreign_keys = ON;",
		"SELECT * FROM t WHERE id = ?;",
		"SELECT * FROM t WHERE id = ?1;",
		"SELECT * FROM t WHERE id = $1;",
		"SELECT * FROM t WHERE id = :id;",
		"SELECT * FROM t WHERE id = @id;",
		"SELECT last_insert_rowid();",
		"SELECT max(a, b) FROM t;",
		"SELECT MIN(length(a), 3) FROM t;",
		"SELECT max(x) AS m, min(a, b) FROM t;",
		"SELECT sqlite_version();",
		"SELECT zeroblob(4);",
		"SELECT hex(id) FROM t;",
		"SELECT char(65);",
		"SELECT total(size) FROM t;",
		"SELECT * FROM t WHERE likely(a = 1);",
		"SELECT * FROM t WHERE unlikely(a = 1);",
		"SELECT * FROM t LIMIT -1 OFFSET 5;",
		"SELECT '--', datetime('now');",
	} {
		if len(unportable(bad)) == 0 {
			t.Errorf("unportable(%q) found nothing", bad)
		}
	}
	for _, ok := range []string{
		"-- INSERT OR REPLACE is forbidden; use ON CONFLICT.\nSELECT 1;",
		"SELECT * FROM t WHERE status IN ('replace', 'datetime(x)', '?');",
		"INSERT INTO blobs (sha256) VALUES (sqlc.arg(sha256)) ON CONFLICT (sha256) DO NOTHING;",
		"SELECT updated_at, created_at FROM t WHERE length(name) > 1 AND name = lower(name);",
		"SELECT max(created_at), min(length(name)) FROM t GROUP BY site_id;",
		"-- the site's default policy\nSELECT 1;",
		"SELECT * FROM t LIMIT sqlc.arg(n);",
	} {
		if got := unportable(ok); len(got) != 0 {
			t.Errorf("unportable(%q) = %v, want none", ok, got)
		}
	}
}

func TestMigrationsAndQueriesArePortable(t *testing.T) {
	files := map[string]fs.FS{"migrations": migrations.FS, "internal/store/queries": os.DirFS("queries")}
	n := 0
	for dir, fsys := range files {
		names, err := fs.Glob(fsys, "*.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			body, err := fs.ReadFile(fsys, name)
			if err != nil {
				t.Fatal(err)
			}
			n++
			for _, bad := range unportable(string(body)) {
				t.Errorf("%s/%s: %s", dir, name, bad)
			}
		}
	}
	if n < 3 {
		t.Fatalf("checked %d SQL files; the globs are probably wrong", n)
	}
}

func TestMigrateAppliesEveryVersionToEmptyDatabase(t *testing.T) {
	all, err := loadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	db := openForTest(t)
	applied, err := db.Migrate(t.Context(), time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != len(all) {
		t.Fatalf("applied %v, want all %d migrations", applied, len(all))
	}
	again, err := db.Migrate(t.Context(), time.Now)
	if err != nil || len(again) != 0 {
		t.Fatalf("second Migrate() = %v, %v; want nothing applied", again, err)
	}
}

// column is one column as both the schema and the diagram describe it.
type column struct {
	typ, name string
	pk, fk    bool
	unique    bool // single-column UNIQUE constraint
}

// fkPair is an unordered pair of tables joined by a foreign key.
type fkPair [2]string

func newPair(a, b string) fkPair {
	if a > b {
		a, b = b, a
	}
	return fkPair{a, b}
}

// schemaOf reads tables, columns and foreign keys from a migrated database.
func schemaOf(t *testing.T, db *DB) (map[string][]column, []fkPair) {
	t.Helper()
	ctx := t.Context()
	rows, err := db.sql.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name <> 'schema_migrations'`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	cols := map[string][]column{}
	var pairs []fkPair
	for _, table := range tables {
		fkCols := map[string]bool{}
		fks, err := db.sql.QueryContext(ctx, `SELECT id, "table", "from" FROM pragma_foreign_key_list(?)`, table)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[int]bool{} // a composite key has one row per column
		for fks.Next() {
			var id int
			var parent, from string
			if err := fks.Scan(&id, &parent, &from); err != nil {
				t.Fatal(err)
			}
			fkCols[from] = true
			if !seen[id] {
				seen[id] = true
				pairs = append(pairs, newPair(table, parent))
			}
		}
		if err := fks.Err(); err != nil {
			t.Fatal(err)
		}

		uniqueCols := map[string]bool{}
		uq, err := db.sql.QueryContext(ctx, `SELECT ii.name FROM pragma_index_list(?) il, pragma_index_info(il.name) ii
			WHERE il."unique" = 1 AND il.origin = 'u' AND (SELECT count(*) FROM pragma_index_info(il.name)) = 1`, table)
		if err != nil {
			t.Fatal(err)
		}
		for uq.Next() {
			var name string
			if err := uq.Scan(&name); err != nil {
				t.Fatal(err)
			}
			uniqueCols[name] = true
		}
		if err := uq.Err(); err != nil {
			t.Fatal(err)
		}

		info, err := db.sql.QueryContext(ctx, `SELECT name, type, pk FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		for info.Next() {
			var c column
			var pk int
			if err := info.Scan(&c.name, &c.typ, &pk); err != nil {
				t.Fatal(err)
			}
			c.pk, c.fk, c.unique = pk > 0, fkCols[c.name], uniqueCols[c.name]
			cols[table] = append(cols[table], c)
		}
		if err := info.Err(); err != nil {
			t.Fatal(err)
		}
	}
	return cols, pairs
}

func TestSchemaConventions(t *testing.T) {
	db := openForTest(t)
	if _, err := db.Migrate(t.Context(), time.Now); err != nil {
		t.Fatal(err)
	}
	cols, _ := schemaOf(t, db)
	if len(cols) == 0 {
		t.Fatal("no tables found")
	}
	for table, cs := range cols {
		hasPK := false
		pkCols := 0
		for _, c := range cs {
			if c.pk {
				pkCols++
			}
		}
		for _, c := range cs {
			hasPK = hasPK || c.pk
			switch {
			case (c.name == "id" || strings.HasSuffix(c.name, "_id")) && c.typ != "TEXT":
				t.Errorf("%s.%s is %s; IDs are TEXT", table, c.name, c.typ)
			case strings.HasSuffix(c.name, "_at") && c.typ != "INTEGER":
				t.Errorf("%s.%s is %s; timestamps are INTEGER Unix milliseconds", table, c.name, c.typ)
			// Only a one-column INTEGER primary key aliases rowid; a
			// composite key (such as a rollup's site and hour) does not.
			case c.pk && pkCols == 1 && c.typ == "INTEGER":
				t.Errorf("%s.%s is an INTEGER primary key; that aliases rowid, use a TEXT ID", table, c.name)
			}
		}
		if !hasPK {
			t.Errorf("%s has no primary key", table)
		}
	}
}

var (
	erRelation  = regexp.MustCompile(`^(\w+)\s+[|o}{]{2}(--|\.\.)[|o}{]{2}\s+(\w+)\s*:`)
	erEntity    = regexp.MustCompile(`^(\w+)\s*\{$`)
	erAttribute = regexp.MustCompile(`^(\w+)\s+(\w+)((?:\s+(?:PK|FK|UK),?)*)(?:\s+"[^"]*")?$`)
)

// diagramOf parses the data model erDiagram in docs/ARCHITECTURE.md.
func diagramOf(t *testing.T) (map[string][]column, []fkPair) {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "docs", "ARCHITECTURE.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(doc), "```mermaid\nerDiagram\n")
	if !ok {
		t.Fatal("docs/ARCHITECTURE.md has no erDiagram block")
	}
	block, _, _ = strings.Cut(block, "```")

	cols := map[string][]column{}
	var pairs []fkPair
	entity := ""
	for i, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		switch m := erRelation.FindStringSubmatch(line); {
		case line == "":
		case entity == "" && m != nil:
			if m[2] == "--" { // dotted lines are references without a foreign key
				pairs = append(pairs, newPair(m[1], m[3]))
			}
		case entity == "" && erEntity.MatchString(line):
			entity = erEntity.FindStringSubmatch(line)[1]
			cols[entity] = nil
		case entity != "" && line == "}":
			entity = ""
		case entity != "" && erAttribute.MatchString(line):
			a := erAttribute.FindStringSubmatch(line)
			c := column{typ: a[1], name: a[2]}
			for k := range strings.FieldsFuncSeq(a[3], func(r rune) bool { return r == ',' || r == ' ' }) {
				switch k {
				case "PK":
					c.pk = true
				case "FK":
					c.fk = true
				case "UK":
					c.unique = true
				}
			}
			cols[entity] = append(cols[entity], c)
		default:
			t.Fatalf("erDiagram line %d not understood: %q", i+1, line)
		}
	}
	return cols, pairs
}

func TestDataModelDiagramMatchesSchema(t *testing.T) {
	db := openForTest(t)
	if _, err := db.Migrate(t.Context(), time.Now); err != nil {
		t.Fatal(err)
	}
	schemaCols, schemaFKs := schemaOf(t, db)
	docCols, docFKs := diagramOf(t)

	for table, want := range schemaCols {
		got, ok := docCols[table]
		if !ok {
			t.Errorf("diagram is missing table %s", table)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("diagram table %s:\n got %+v\nwant %+v", table, got, want)
		}
	}
	for table := range docCols {
		if _, ok := schemaCols[table]; !ok {
			t.Errorf("diagram has table %s, which the schema does not", table)
		}
	}
	less := func(a, b fkPair) int { return strings.Compare(a[0]+" "+a[1], b[0]+" "+b[1]) }
	slices.SortFunc(schemaFKs, less)
	slices.SortFunc(docFKs, less)
	if !slices.Equal(docFKs, schemaFKs) {
		t.Errorf("diagram foreign keys (solid lines):\n got %v\nwant %v", docFKs, schemaFKs)
	}
}

func openForTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(t.Context(), filepath.Join(t.TempDir(), "wardlock.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
