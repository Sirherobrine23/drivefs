package cache

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"iter"
	"time"
)

type DBColl struct {
	ID      sql.NullInt64
	Key     sql.NullString
	TimeTTL sql.NullTime
	Value   sql.NullString
}

type Database[T any] struct {
	DBName string
	DB     *sql.DB
}

// Open new connection with [database/sql.Open] and attach
func NewOpenDB[T any](DriveName, dataSourceName, DBName string) (Cache[T], error) {
	db, err := sql.Open(DriveName, dataSourceName)
	if err != nil {
		return nil, err
	}
	ndb := &Database[T]{DBName: DBName, DB: db}
	if err := ndb.CreateTable(); err != nil {
		return nil, err
	}
	return ndb, nil
}

// Attach in db with [database/sql/driver.Connector]
func NewOpenConnectorDB[T any](drive driver.Connector, DBName string) (Cache[T], error) {
	ndb := &Database[T]{DBName: DBName, DB: sql.OpenDB(drive)}
	if err := ndb.CreateTable(); err != nil {
		return nil, err
	}
	return ndb, nil
}

// Attach connection with current [database/sql.DB]
func NewAttachDB[T any](db *sql.DB, DBName string) (Cache[T], error) {
	ndb := &Database[T]{DBName: DBName, DB: db}
	if err := ndb.CreateTable(); err != nil {
		return nil, err
	}
	return ndb, nil
}

func (db *Database[T]) CreateTable() error {
	q, err := db.DB.Query(`SELECT * FROM ?`, db.DBName)
	if err == nil {
		q.Close()
		return nil
	}
	_, err = db.DB.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		Key TEXT UNIQUE NOT NULL,
		TTL DATETIME,
		MsgValue TEXT,
		PRIMARY KEY (Key)
	)`, db.DBName))
	return err
}

func (db *Database[T]) Flush() error {
	_, err := db.DB.Exec(fmt.Sprintf(`DELETE FROM %q WHERE (TTL < ?)`, db.DBName), sql.NullTime{Valid: true, Time: time.Now()})
	if err == sql.ErrNoRows {
		err = nil
	}
	return err
}

func (db *Database[T]) Delete(key string) error {
	_, err := db.DB.Exec(fmt.Sprintf(`DELETE FROM %q WHERE Key == ?`, db.DBName), key)
	return err
}

func (db *Database[T]) Get(key string) (value T, err error) {
	var Value sql.NullString
	if err := db.DB.QueryRow(fmt.Sprintf(`SELECT MsgValue FROM %q WHERE Key == ?`, db.DBName), key).Scan(&Value); err != nil {
		if err == sql.ErrNoRows {
			return *new(T), ErrNotExist
		}
		return *new(T), err
	} else if !Value.Valid {
		return *new(T), ErrNotExist
	}
	return FromString[T](Value.String)
}

func (db *Database[T]) Set(ttl time.Duration, key string, value T) (err error) {
	var (
		KeyStr, MsgValue sql.NullString
		TTL              sql.NullTime
	)

	KeyStr.Valid, MsgValue.Valid, TTL.Valid = true, true, true
	KeyStr.String = key
	TTL.Time = time.Now().Add(ttl)
	if MsgValue.String, err = ToString(value); err != nil {
		return
	}

	if err = db.DB.QueryRow(fmt.Sprintf(`SELECT Key FROM %q WHERE Key == %q`, db.DBName, key)).Scan(&key); err != nil {
		if err != sql.ErrNoRows {
			return err
		}
		if _, err = db.DB.Exec(fmt.Sprintf(`INSERT INTO %q (Key, TTL, MsgValue) VALUES (?,?,?)`, db.DBName), KeyStr, TTL, MsgValue); !(err == nil || err == sql.ErrNoRows) {
			return nil
		}
		return nil
	}

	_, err = db.DB.Exec(fmt.Sprintf(`UPDATE %q SET MsgValue = ?, TTL = ? WHERE Key == ?`, db.DBName), MsgValue, TTL, KeyStr)
	if err != nil {
		return nil
	}
	return nil
}

func (db *Database[T]) Values() iter.Seq2[string, T] {
	return func(yield func(string, T) bool) {
		rows, err := db.DB.Query(fmt.Sprintf(`SELECT (Key, TTL, MsgValue) FROM %s`, db.DBName))
		if err != nil {
			panic(err)
		}
		defer rows.Close()
		for rows.Next() {
			var (
				Key, MsgValue sql.NullString
				TTL           sql.NullTime
			)
			if err = rows.Scan(&Key, &TTL, &MsgValue); err != nil {
				panic(err)
			}
			if TTL.Time.Compare(time.Now()) != 1 {
				continue
			}

			value, err := FromString[T](MsgValue.String)
			if err != nil {
				panic(err)
			}
			if !yield(Key.String, value) {
				return
			}
		}
		if err = rows.Err(); err != nil {
			panic(err)
		}
	}
}
