// Read-only ad-hoc query runner: go run . "$DB" "SELECT ..." — prints tab-separated rows.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	db, err := sql.Open("pgx", os.Args[1])
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		panic(err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, os.Args[2])
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Println(strings.Join(cols, "\t"))
	vals := make([]any, len(cols))
	ptrs := make([]any, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			panic(err)
		}
		out := make([]string, len(cols))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				out[i] = string(b)
			} else {
				out[i] = fmt.Sprint(v)
			}
		}
		fmt.Println(strings.Join(out, "\t"))
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
}
