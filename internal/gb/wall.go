package gb

import (
	"database/sql"
	"encoding/json"
	"errors"
)

type Wall struct {
	Size  int      `json:"size"`
	Slots []string `json:"slots"`
}

func (s *Store) Wall(a Account) (Wall, error) {
	var data string
	err := s.db.QueryRow("SELECT data FROM walls WHERE account_id=? AND space_id=?", a.ID, a.SpaceID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Wall{Size: 4, Slots: []string{}}, nil
	}
	if err != nil {
		return Wall{}, err
	}
	var wall Wall
	err = json.Unmarshal([]byte(data), &wall)
	return wall, err
}
func (s *Store) SaveWall(a Account, w Wall) error {
	data, err := json.Marshal(w)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO walls(account_id,space_id,data) VALUES(?,?,?) ON CONFLICT(account_id,space_id) DO UPDATE SET data=excluded.data", a.ID, a.SpaceID, string(data))
	return err
}

func (s *Store) SetPTZ(space int64, device, channel string, enabled bool) error {
	if !s.HasChannel(space, device, channel) {
		return sql.ErrNoRows
	}
	_, err := s.db.Exec("INSERT INTO ptz_capabilities VALUES(?,?,?) ON CONFLICT(device_id,channel_id) DO UPDATE SET enabled=excluded.enabled", device, channel, enabled)
	return err
}

func (s *Store) CanPTZ(space int64, device, channel string) bool {
	var enabled bool
	err := s.db.QueryRow("SELECT p.enabled FROM ptz_capabilities p JOIN devices d ON d.id=p.device_id JOIN channels c ON c.device_id=d.id AND c.id=p.channel_id WHERE d.space_id=? AND d.id=? AND c.id=?", space, device, channel).Scan(&enabled)
	return err == nil && enabled
}
