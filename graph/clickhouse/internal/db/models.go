// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.28.0

package db

import (
	"database/sql"
)

type AtgraphProfile struct {
	Did      string
	Type     string
	Handle   sql.NullString
	Created  interface{}
	Ingested interface{}
	Updated  interface{}
	Rev      sql.NullString
	Sig      sql.NullString
	Version  interface{}
}
