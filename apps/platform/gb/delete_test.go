package gb

import (
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
)

func TestDeleteDeviceIsolatedAndClearsReferences(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "delete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.EnsureAdmin("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	a := Account{SpaceID: DefaultSpaceID}
	if err = s.db.QueryRow("SELECT id FROM accounts WHERE username='admin'").Scan(&a.ID); err != nil {
		t.Fatal(err)
	}
	const id = "34020000001320000001"
	const ch = "34020000001320000002"
	if err = s.Provision(a.SpaceID, id, "test", "camera", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	if err = s.saveChannels(id, []Channel{{ID: ch, Name: "channel", PTZType: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPTZ(a.SpaceID, id, ch, true); err != nil {
		t.Fatal(err)
	}
	key := "gb/" + id + "/" + ch
	other := "gb/34020000001320000003/" + ch
	if err = s.SaveWall(a, Wall{Size: 4, Slots: []string{key, "", other, key}}); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteDevice(a.SpaceID+1, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign delete: %v", err)
	}
	if !s.HasChannel(a.SpaceID, id, ch) {
		t.Fatal("foreign delete removed channel")
	}
	if err = s.DeleteDevice(a.SpaceID, id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"devices", "channels", "ptz_catalog", "ptz_capabilities"} {
		var n int
		if err = s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("%s: count=%d err=%v", table, n, err)
		}
	}
	w, err := s.Wall(a)
	if err != nil || w.Size != 4 || !slices.Equal(w.Slots, []string{"", "", other, ""}) {
		t.Fatalf("wall: %+v %v", w, err)
	}
	if err = s.DeleteDevice(a.SpaceID, id); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("repeat: %v", err)
	}
}
