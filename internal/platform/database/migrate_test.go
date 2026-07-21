package database

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadMigrationsOrdersAndChecksums(t *testing.T) {
	fsys := fstest.MapFS{
		"0002_rbac.up.sql":    {Data: []byte("CREATE TABLE roles ();")},
		"0001_users.up.sql":   {Data: []byte("CREATE TABLE users ();")},
		"0001_users.down.sql": {Data: []byte("DROP TABLE users;")},
		"README.md":           {Data: []byte("ignored")},
		"0003_servers.up.sql": {Data: []byte("CREATE TABLE servers ();")},
	}
	ms, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(ms) != 3 {
		t.Fatalf("got %d migrations, want 3", len(ms))
	}
	for i, want := range []string{"users", "rbac", "servers"} {
		if ms[i].Version != i+1 || ms[i].Name != want {
			t.Errorf("migration %d = %04d_%s, want %04d_%s", i, ms[i].Version, ms[i].Name, i+1, want)
		}
		if len(ms[i].Checksum) != 64 {
			t.Errorf("migration %d checksum length %d", i, len(ms[i].Checksum))
		}
	}
}

func TestLoadMigrationsRejectsGaps(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_users.up.sql":   {Data: []byte("x")},
		"0003_servers.up.sql": {Data: []byte("y")},
	}
	if _, err := loadMigrations(fsys); err == nil || !strings.Contains(err.Error(), "contiguous") {
		t.Errorf("gap not rejected: %v", err)
	}
}

func TestLoadMigrationsRejectsDuplicates(t *testing.T) {
	fsys := fstest.MapFS{
		"0001_users.up.sql": {Data: []byte("x")},
		"0001_dupes.up.sql": {Data: []byte("y")},
	}
	if _, err := loadMigrations(fsys); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("duplicate not rejected: %v", err)
	}
}
