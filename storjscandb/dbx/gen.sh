#!/bin/sh

dbx schema -d pgx -d pgxcockroach storjscandb.dbx .
dbx golang -d pgx -d pgxcockroach -p dbx -t templates storjscandb.dbx .
( printf '%s\n' '//lint:file-ignore U1000,ST1012 generated file'; cat storjscandb.dbx.go ) > storjscandb.dbx.go.tmp && mv storjscandb.dbx.go.tmp storjscandb.dbx.go
gofmt -r "*sql.Tx -> tagsql.Tx" -w storjscandb.dbx.go
gofmt -r "*sql.Rows -> tagsql.Rows" -w storjscandb.dbx.go
perl -0777 -pi \
  -e 's,\t_ "github.com/jackc/pgx/v5/stdlib"\n\),\t_ "github.com/jackc/pgx/v5/stdlib"\n\n\t"storj.io/private/tagsql"\n\),' \
  storjscandb.dbx.go
perl -0777 -pi \
  -e 's/type DB struct \{\n\t\*sql\.DB/type DB struct \{\n\ttagsql.DB/' \
  storjscandb.dbx.go
perl -0777 -pi \
  -e 's/\tdb = &DB\{\n\t\tDB: sql_db,/\tdb = &DB\{\n\t\tDB: tagsql.Wrap\(sql_db\),/' \
  storjscandb.dbx.go
