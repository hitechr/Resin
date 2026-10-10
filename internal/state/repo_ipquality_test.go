package state

import (
	"testing"
	"time"
)

func newTestIPQualityRepo(t *testing.T) *IPQualityRepo {
	t.Helper()
	dir := t.TempDir()
	db, err := OpenDB(dir + "/cache.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateCacheDB(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return newIPQualityRepo(db)
}

func TestIPQualityRepo_SaveLoadAndExpire(t *testing.T) {
	repo := newTestIPQualityRepo(t)

	fresh := time.Now().Add(time.Hour).Truncate(0)
	expired := time.Now().Add(-time.Hour).Truncate(0)
	if err := repo.SaveQuality("8.8.8.8", []byte(`{"ip":"8.8.8.8"}`), fresh); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuality("1.1.1.1", []byte(`{"ip":"1.1.1.1"}`), expired); err != nil {
		t.Fatal(err)
	}
	// Upsert the same IP to prove the primary key replaces the payload.
	if err := repo.SaveQuality("8.8.8.8", []byte(`{"ip":"8.8.8.8","score":10}`), fresh); err != nil {
		t.Fatal(err)
	}

	loaded, err := repo.LoadQuality()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || string(loaded["8.8.8.8"]) != `{"ip":"8.8.8.8","score":10}` {
		t.Fatalf("loaded rows: %#v", loaded)
	}

	if err := repo.DeleteExpiredIPQuality(time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	loaded, err = repo.LoadQuality()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expired rows must be removed: %#v", loaded)
	}
	if _, ok := loaded["1.1.1.1"]; ok {
		t.Fatal("expired IP still present")
	}
}
