package gb

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestOpenStoreClearsRetiredWallSlots(t *testing.T) {
	path := filepath.Join(t.TempDir(), "viewhub.db")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { store.Close() }()
	if err := store.EnsureAdmin("admin", "fixture-password"); err != nil {
		t.Fatal(err)
	}
	actor := Account{SpaceID: DefaultSpaceID}
	if err := store.db.QueryRow("SELECT id FROM accounts WHERE username = ?", "admin").Scan(&actor.ID); err != nil {
		t.Fatal(err)
	}
	const first = "gb/34020000001320000001/34020000001310000001"
	const last = "gb/34020000001320000002/34020000001310000002"
	if err := store.SaveWall(actor, Wall{Size: 9, Slots: []string{first, "edge/site/channel", "", last}}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store = reopened
	wall, err := store.Wall(actor)
	if err != nil {
		t.Fatal(err)
	}
	if wall.Size != 9 || !slices.Equal(wall.Slots, []string{first, "", "", last}) {
		t.Fatalf("unexpected wall after upgrade: %+v", wall)
	}
}
