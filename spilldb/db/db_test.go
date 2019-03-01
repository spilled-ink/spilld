package db_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"spilled.ink/spilldb/db"
)

func TestLog(t *testing.T) {
	now := time.Now()
	l := db.Log{
		Where:    "here",
		What:     "it",
		When:     now,
		Duration: 57 * time.Millisecond,
	}
	data := make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["where"], "here"; got != want {
		t.Errorf("where=%q, want %q", got, want)
	}
	if got, want := data["what"], "it"; got != want {
		t.Errorf("where=%q, want %q", got, want)
	}
	if got, want := data["when"], now.Format(time.RFC3339Nano); got != want {
		t.Errorf("when=%q, want %q", got, want)
	}
	if got, want := data["duration"], "57ms"; got != want {
		t.Errorf("duration=%q, want %q", got, want)
	}

	l.Err = errors.New("an error msg")
	data = make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["err"], l.Err.Error(); got != want {
		t.Errorf("err=%q, want %q", got, want)
	}

	l.Data = map[string]interface{}{"data1": 42}
	data = make(map[string]interface{})
	if err := json.Unmarshal([]byte(l.String()), &data); err != nil {
		t.Fatal(err)
	}
	if got, want := data["data"].(map[string]interface{})["data1"], float64(42); got != want {
		t.Errorf("data=%f, want %f", got, want)
	}
}
