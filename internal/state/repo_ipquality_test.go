package state

import (
	"fmt"
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

func TestIPQualityRepo_JobsSurviveReopenAndPrune(t *testing.T) {
	path := t.TempDir() + "/cache.db"
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateCacheDB(db); err != nil {
		t.Fatal(err)
	}
	repo := newIPQualityRepo(db)
	for i := 0; i < 130; i++ {
		id := fmt.Sprintf("%03d", i)
		if err := repo.SaveJob(id, []byte(id), time.Unix(int64(i), 0)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := repo.LoadJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 128 {
		t.Fatalf("bounded jobs: %d", len(rows))
	}
	if string(rows[0]) != "002" || string(rows[127]) != "129" {
		t.Fatalf("ordered jobs: %q %q", rows[0], rows[127])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := MigrateCacheDB(db); err != nil {
		t.Fatal(err)
	}
	repo = newIPQualityRepo(db)
	rows, err = repo.LoadJobs()
	if err != nil || len(rows) != 128 {
		t.Fatalf("reopened rows: %d %v", len(rows), err)
	}
	if err := repo.DeleteJob("002"); err != nil {
		t.Fatal(err)
	}
	rows, err = repo.LoadJobs()
	if err != nil || len(rows) != 127 {
		t.Fatalf("deleted job: %d %v", len(rows), err)
	}
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
	if _, found, err := repo.ReadQuality("1.1.1.1"); err != nil || found {
		t.Fatalf("expired read: found=%v err=%v", found, err)
	}
	if payload, found, err := repo.ReadQuality("8.8.8.8"); err != nil || !found || string(payload) != `{"ip":"8.8.8.8","score":10}` {
		t.Fatalf("single row: %q found=%v err=%v", payload, found, err)
	}
	if _, ok := loaded["1.1.1.1"]; ok {
		t.Fatal("expired IP still present")
	}
}
