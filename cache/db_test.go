package cache

import (
	"fmt"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

type Value struct {
	Title string `json:"title"`
	Msg   string `json:"msg"`
}

func TestDbSqlite(t *testing.T) {
	cache, err := NewOpenDB[Value]("sqlite", "../cache_test.db", "cache")
	if err != nil {
		t.Skip(err)
		return
	}

	fistValue := Value{
		Title: "Google",
		Msg:   "made by golang.",
	}

	if err := cache.Set(time.Hour, "fist1", fistValue); err != nil {
		t.Error(fmt.Errorf("cannot set fist1: %s", err))
		return
	}

	// Invalid method
	if err := cache.Set(0, "fist2", fistValue); err != nil {
		t.Error(fmt.Errorf("cannot set fist2: %s", err))
		return
	}

	recoveryValue, err := cache.Get("fist1")
	if err != nil {
		t.Error(fmt.Errorf("cannot get fist1: %s", err))
		return
	}

	if fistValue.Title != recoveryValue.Title {
		t.Errorf("Title is not same: %q != %q", fistValue.Title, recoveryValue.Title)
		return
	} else if fistValue.Msg != recoveryValue.Msg {
		t.Errorf("Msg is not same: %q != %q", fistValue.Msg, recoveryValue.Msg)
		return
	}

	if err := cache.Flush(); err != nil {
		t.Errorf("cannot flush: %s", err)
		return
	}
}
