package cache

import (
	"database/sql"
	"fmt"
	"iter"
	"time"
)

var _ Cache[any] = (*Sqlite[any])(nil)

const (
	SqliteListAllKeys    = `SELECT KEY_NAME, KEY_VALUE FROM %q WHERE LIMIT_TTL > ?;`                    // List all keys with value
	SqliteDeleteOutdated = `DELETE FROM %q WHERE LIMIT_TTL < ?;`                                        // Delete rows if outdate
	SqliteDelete         = `DELETE FROM %q WHERE KEY_NAME == ?;`                                        // Delete with KEY_NAME
	SqliteInsert         = `INSERT OR REPLACE INTO %q (KEY_NAME, KEY_VALUE, LIMIT_TTL) VALUES (?,?,?);` // Create key or replace if exists
	SqliteSelect         = `SELECT KEY_VALUE FROM %q WHERE KEY_NAME == ? AND LIMIT_TTL > ?;`            // Select value by KEY_NAME

	// Create table if not exists to cache value
	SqliteCreateTable = `CREATE TABLE IF NOT EXISTS %q (
    ID         INTEGER  PRIMARY KEY AUTOINCREMENT,
    KEY_NAME   TEXT     UNIQUE NOT NULL,
    KEY_VALUE  TEXT     NOT NULL,
    LIMIT_TTL  DATETIME NOT NULL
	);`
)

type Sqlite[T any] struct {
	DBName string
	DB     *sql.DB
}

func OpenSqlite[T any](dataSourceName, dbName string) (Cache[T], error) {
	db, err := sql.Open("sqlite", dataSourceName)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(fmt.Sprintf(SqliteCreateTable, dbName))
	return &Sqlite[T]{DBName: dbName, DB: db}, err
}

func (db Sqlite[T]) Flush() error {
	_, err := db.DB.Exec(fmt.Sprintf(SqliteDeleteOutdated, db.DBName), time.Now())
	if err == sql.ErrNoRows {
		err = nil
	}
	return err
}

func (db Sqlite[T]) Delete(key string) error {
	_, err := db.DB.Exec(fmt.Sprintf(SqliteDelete, db.DBName), key)
	if err == sql.ErrNoRows {
		err = ErrNotExist
	}
	return err
}

func (db Sqlite[T]) Set(ttl time.Duration, key string, value T) error {
	data, err := ToString(value)
	if err != nil {
		return err
	}
	_, err = db.DB.Exec(fmt.Sprintf(SqliteInsert, db.DBName), key, data, sql.NullTime{Time: time.Now().Add(ttl), Valid: true})
	return err
}

func (db Sqlite[T]) Get(key string) (T, error) {
	var value sql.NullString
	if err := db.DB.QueryRow(fmt.Sprintf(SqliteSelect, db.DBName), key, time.Now()).Scan(&value); err != nil || !value.Valid {
		if err == sql.ErrNoRows || !value.Valid {
			err = ErrNotExist
		}
		return *new(T), err
	}

	return FromString[T](value.String)
}

func (db Sqlite[T]) Values() (iter.Seq2[string, T], error) {
	rows, err := db.DB.Query(fmt.Sprintf(SqliteListAllKeys, db.DBName), time.Now())
	if err != nil {
		return nil, err
	}

	return func(yield func(string, T) bool) {
		defer rows.Close()
		for rows.Next() {
			var key, value string
			if err := rows.Scan(&key, &value); err != nil {
				return
			}

			valueData, err := FromString[T](value)
			if err != nil {
				return
			}

			if !yield(key, valueData) {
				return
			}
		}
	}, nil
}
